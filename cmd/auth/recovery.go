package main

import (
	"crypto/rand"
	"math/big"
	"net/http"
	"strconv"
)

const recoveryAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

func newRecoveryCode() (string, error) {
	code := make([]byte, 0, 19)
	for i := 0; i < 16; i++ {
		choice, err := rand.Int(rand.Reader, big.NewInt(int64(len(recoveryAlphabet))))
		if err != nil {
			return "", err
		}
		code = append(code, recoveryAlphabet[choice.Int64()])
		if i == 3 || i == 7 || i == 11 {
			code = append(code, '-')
		}
	}
	return string(code), nil
}

func (a *app) recoveryStatus(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.sessionUser(r)
	if !ok {
		oauthError(w, http.StatusUnauthorized, "login_required")
		return
	}
	var total, remaining int
	if err := a.db.QueryRowContext(r.Context(), `SELECT COUNT(*),COUNT(CASE WHEN is_used=0 THEN 1 END) FROM recovery_codes WHERE user_id=?`, id).Scan(&total, &remaining); err != nil {
		oauthError(w, http.StatusServiceUnavailable, "temporarily_unavailable")
		return
	}
	jsonResponse(w, http.StatusOK, map[string]any{"issued": total > 0, "remaining": remaining})
}

func (a *app) generateRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	if !a.requireUI(w, r) {
		return
	}
	id, ok := a.securitySettingsUser(w, r)
	if !ok {
		return
	}
	var input struct{}
	if !parseJSON(w, r, &input) {
		return
	}
	limited, err := a.rateLimited(r.Context(), "recovery-generate:"+strconv.FormatUint(id, 10))
	if err != nil {
		a.recoveryStorageError(w)
		return
	}
	if limited {
		oauthError(w, 429, "rate_limited")
		return
	}
	codes := make([]string, 10)
	for i := range codes {
		codes[i], err = newRecoveryCode()
		if err != nil {
			a.recoveryStorageError(w)
			return
		}
	}
	tx, err := a.db.BeginTx(r.Context(), nil)
	if err != nil {
		a.recoveryStorageError(w)
		return
	}
	defer tx.Rollback()
	var active bool
	if err := tx.QueryRowContext(r.Context(), `SELECT is_deleted=0 FROM users WHERE id=? FOR UPDATE`, id).Scan(&active); err != nil || !active {
		a.recoveryStorageError(w)
		return
	}
	if !a.securitySettingsGrantInTx(w, r, tx, id) {
		return
	}
	if _, err := tx.ExecContext(r.Context(), `DELETE FROM recovery_codes WHERE user_id=? AND is_used=0`, id); err != nil {
		a.recoveryStorageError(w)
		return
	}
	for _, code := range codes {
		if _, err := tx.ExecContext(r.Context(), `INSERT INTO recovery_codes (user_id,hashed_code,is_used) VALUES (?,?,0)`, id, digest(code)); err != nil {
			a.recoveryStorageError(w)
			return
		}
	}
	if err := tx.Commit(); err != nil {
		a.recoveryStorageError(w)
		return
	}
	jsonResponse(w, 200, map[string]any{"codes": codes})
}

func (a *app) recoveryStorageError(w http.ResponseWriter) {
	a.alerts.send("Taskly auth recovery code generation failed")
	oauthError(w, 503, "temporarily_unavailable")
}
