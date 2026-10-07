package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/pquerna/otp/totp"
)

const securityGrantLifetime = 5 * time.Minute

func securityGrantValid(verified sql.NullTime, now time.Time) bool {
	return verified.Valid && verified.Time.After(now.Add(-securityGrantLifetime)) && !verified.Time.After(now)
}

func (a *app) securityCodeDigest(kind string, id uint64, sessionHash, email, code string) string {
	mac := hmac.New(sha256.New, []byte(a.internalToken))
	_, _ = fmt.Fprintf(mac, "%s:%d:%s:%s:%s", kind, id, sessionHash, email, code)
	return hex.EncodeToString(mac.Sum(nil))
}

func securityMethods(totpEnabled bool, recoveryCount int) []string {
	if !totpEnabled {
		return []string{"email"}
	}
	methods := []string{"totp"}
	if recoveryCount > 0 {
		methods = append(methods, "recovery")
	}
	return methods
}

func (a *app) securitySession(w http.ResponseWriter, r *http.Request) (uint64, string, bool) {
	id, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return 0, "", false
	}
	cookie, err := r.Cookie("taskly_auth_session")
	if err != nil {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return 0, "", false
	}
	return id, digest(cookie.Value), true
}

// The rollout gate preserves existing settings endpoints until the matching UI
// has been deployed. Once enabled, every settings mutation needs a recent grant
// bound to this exact auth session; the age of the original login is irrelevant.
func (a *app) securitySettingsUser(w http.ResponseWriter, r *http.Request) (uint64, bool) {
	if !a.securityStepUpRequired {
		id, ok := a.recentSessionUser(r)
		if !ok {
			oauthError(w, http.StatusUnauthorized, "login_required")
		}
		return id, ok
	}
	id, sessionHash, ok := a.securitySession(w, r)
	if !ok {
		return 0, false
	}
	var verified sql.NullTime
	err := a.db.QueryRowContext(r.Context(), `SELECT security_verified_at FROM auth_sessions WHERE token_hash=? AND user_id=? AND expires_at>UTC_TIMESTAMP()`, sessionHash, id).Scan(&verified)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return 0, false
	}
	if !securityGrantValid(verified, time.Now().UTC()) {
		oauthError(w, http.StatusForbidden, "step_up_required")
		return 0, false
	}
	return id, true
}

// Call after locking the users row and before any settings write. Holding both
// locks until commit prevents a factor transition from invalidating the grant
// between the preliminary request check and the actual mutation.
func (a *app) securitySettingsGrantInTx(w http.ResponseWriter, r *http.Request, tx *sql.Tx, id uint64) bool {
	if !a.securityStepUpRequired {
		return true
	}
	cookie, err := r.Cookie("taskly_auth_session")
	if err != nil || cookie.Value == "" {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return false
	}
	var verified sql.NullTime
	err = tx.QueryRowContext(r.Context(), `SELECT security_verified_at FROM auth_sessions WHERE token_hash=? AND user_id=? AND expires_at>UTC_TIMESTAMP() FOR UPDATE`, digest(cookie.Value), id).Scan(&verified)
	if errors.Is(err, sql.ErrNoRows) {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return false
	}
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return false
	}
	if !securityGrantValid(verified, time.Now().UTC()) {
		oauthError(w, http.StatusForbidden, "step_up_required")
		return false
	}
	return true
}

func (a *app) securityStepUpStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, sessionHash, ok := a.securitySession(w, r)
	if !ok {
		return
	}
	var verified sql.NullTime
	if err := a.db.QueryRowContext(r.Context(), `SELECT security_verified_at FROM auth_sessions WHERE token_hash=? AND user_id=? AND expires_at>UTC_TIMESTAMP()`, sessionHash, id).Scan(&verified); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	var totpCount, recoveryCount int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM totp_secrets WHERE user_id=? AND is_active=1`, id).Scan(&totpCount); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	if totpCount > 0 {
		if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM recovery_codes WHERE user_id=? AND is_used=0`, id).Scan(&recoveryCount); err != nil {
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
	}
	jsonResponse(w, http.StatusOK, map[string]any{
		"required": a.securityStepUpRequired,
		"verified": securityGrantValid(verified, time.Now().UTC()),
		"methods":  securityMethods(totpCount > 0, recoveryCount),
	})
}

func (a *app) requestSecurityStepUpCode(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, sessionHash, ok := a.securitySession(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !parseJSON(w, r, &input) {
		return
	}
	limited, err := a.rateLimited(r.Context(), "security-code:"+strconv.FormatUint(id, 10))
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	if limited {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	var email, locale string
	if err := a.db.QueryRowContext(r.Context(), `SELECT email,notification_lang FROM users WHERE id=? AND is_deleted=0`, id).Scan(&email, &locale); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	var totpCount int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*) FROM totp_secrets WHERE user_id=? AND is_active=1`, id).Scan(&totpCount); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	if totpCount > 0 {
		oauthError(w, http.StatusConflict, "totp_required")
		return
	}
	if _, err := a.db.ExecContext(r.Context(), `DELETE FROM auth_security_codes WHERE expires_at<UTC_TIMESTAMP()`); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	code := fmt.Sprintf("%06d", n.Int64())
	_, err = a.db.ExecContext(r.Context(), `INSERT INTO auth_security_codes (session_hash,user_id,email,hashed_code,expires_at,failed_attempts) VALUES (?,?,?,?,?,0) ON DUPLICATE KEY UPDATE user_id=VALUES(user_id),email=VALUES(email),hashed_code=VALUES(hashed_code),expires_at=VALUES(expires_at),failed_attempts=0,created_at=UTC_TIMESTAMP()`, sessionHash, id, email, a.securityCodeDigest("email", id, sessionHash, email, code), time.Now().UTC().Add(10*time.Minute))
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	if err := sendMailCode(r.Context(), email, code, securityCodePurpose, locale); err != nil {
		_, _ = a.db.ExecContext(r.Context(), `DELETE FROM auth_security_codes WHERE session_hash=? AND hashed_code=?`, sessionHash, a.securityCodeDigest("email", id, sessionHash, email, code))
		a.alerts.send("Taskly auth security verification email delivery failed")
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	w.WriteHeader(http.StatusAccepted)
}

func (a *app) verifySecurityStepUp(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, sessionHash, ok := a.securitySession(w, r)
	if !ok {
		return
	}
	var input struct {
		Method string `json:"method"`
		Code   string `json:"code"`
	}
	if !parseJSON(w, r, &input) {
		return
	}
	if input.Method != "email" && input.Method != "totp" && input.Method != "recovery" {
		oauthError(w, http.StatusBadRequest, "invalid_request")
		return
	}
	limited, err := a.rateLimited(r.Context(), "security-verify:"+strconv.FormatUint(id, 10))
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	if limited {
		oauthError(w, http.StatusTooManyRequests, "rate_limited")
		return
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	defer tx.Rollback()
	var email string
	if err := tx.QueryRowContext(r.Context(), `SELECT email FROM users WHERE id=? AND is_deleted=0 FOR UPDATE`, id).Scan(&email); err != nil {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	var sessionID uint64
	if err := tx.QueryRowContext(r.Context(), `SELECT user_id FROM auth_sessions WHERE token_hash=? AND user_id=? AND expires_at>UTC_TIMESTAMP() FOR UPDATE`, sessionHash, id).Scan(&sessionID); err != nil {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	var encrypted sql.NullString
	err = tx.QueryRowContext(r.Context(), `SELECT secret FROM totp_secrets WHERE user_id=? AND is_active=1 FOR UPDATE`, id).Scan(&encrypted)
	if err != nil && err != sql.ErrNoRows {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	totpEnabled := err == nil
	if (totpEnabled && input.Method == "email") || (!totpEnabled && input.Method != "email") {
		oauthError(w, http.StatusConflict, "method_unavailable")
		return
	}
	switch input.Method {
	case "email":
		if !validTOTPCode(input.Code) {
			oauthError(w, http.StatusUnauthorized, "invalid_code")
			return
		}
		var storedEmail, expected string
		var expires time.Time
		var failures int
		err := tx.QueryRowContext(r.Context(), `SELECT email,hashed_code,expires_at,failed_attempts FROM auth_security_codes WHERE session_hash=? AND user_id=? FOR UPDATE`, sessionHash, id).Scan(&storedEmail, &expected, &expires, &failures)
		if err != nil || storedEmail != email || !expires.After(time.Now().UTC()) || failures >= 5 {
			oauthError(w, http.StatusUnauthorized, "invalid_code")
			return
		}
		if !equal(expected, a.securityCodeDigest("email", id, sessionHash, email, input.Code)) {
			if _, err := tx.ExecContext(r.Context(), `UPDATE auth_security_codes SET failed_attempts=failed_attempts+1 WHERE session_hash=?`, sessionHash); err != nil || tx.Commit() != nil {
				oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
				return
			}
			oauthError(w, http.StatusUnauthorized, "invalid_code")
			return
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM auth_security_codes WHERE session_hash=?`, sessionHash); err != nil {
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
	case "totp":
		if !validTOTPCode(input.Code) {
			oauthError(w, http.StatusUnauthorized, "invalid_code")
			return
		}
		secret, err := decryptTOTP(encrypted.String, osEnv("TOTP_ENC_KEY"))
		if err != nil {
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
		if !totp.Validate(input.Code, secret) {
			oauthError(w, http.StatusUnauthorized, "invalid_code")
			return
		}
		if _, err := tx.ExecContext(r.Context(), `DELETE FROM auth_security_totp_uses WHERE expires_at<UTC_TIMESTAMP()`); err != nil {
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
		_, err = tx.ExecContext(r.Context(), `INSERT INTO auth_security_totp_uses (user_id,code_hash,expires_at) VALUES (?,?,?)`, id, a.securityCodeDigest("totp", id, "", "", input.Code), time.Now().UTC().Add(2*time.Minute))
		if err != nil {
			var mysqlError *mysql.MySQLError
			if errors.As(err, &mysqlError) && mysqlError.Number == 1062 {
				oauthError(w, http.StatusUnauthorized, "invalid_code")
			} else {
				oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			}
			return
		}
	case "recovery":
		if len(input.Code) != 19 {
			oauthError(w, http.StatusUnauthorized, "invalid_code")
			return
		}
		result, err := tx.ExecContext(r.Context(), `UPDATE recovery_codes SET is_used=1 WHERE user_id=? AND hashed_code=? AND is_used=0`, id, digest(input.Code))
		if err != nil {
			oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
			return
		}
		count, err := result.RowsAffected()
		if err != nil || count != 1 {
			oauthError(w, http.StatusUnauthorized, "invalid_code")
			return
		}
	}
	if _, err := tx.ExecContext(r.Context(), `UPDATE auth_sessions SET security_verified_at=UTC_TIMESTAMP() WHERE token_hash=? AND user_id=?`, sessionHash, id); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	if err := tx.Commit(); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
