package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const storedWebauthnCredentialPrefix = "webauthn:v1:"

type passkeyUser struct {
	id          uint64
	email       string
	displayName string
	credentials []webauthn.Credential
}

type passkeyQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

// Keep the original decimal user handle: changing it would break existing keys.
func (u *passkeyUser) WebAuthnID() []byte   { return []byte(strconv.FormatUint(u.id, 10)) }
func (u *passkeyUser) WebAuthnName() string { return u.email }
func (u *passkeyUser) WebAuthnDisplayName() string {
	if u.displayName != "" {
		return u.displayName
	}
	return u.email
}
func (u *passkeyUser) WebAuthnCredentials() []webauthn.Credential { return u.credentials }

func (a *app) loadPasskeyUser(ctx context.Context, id uint64, backupEligible *bool) (*passkeyUser, error) {
	return loadPasskeyUser(ctx, a.db, id, backupEligible, false)
}

func loadPasskeyUser(ctx context.Context, db passkeyQueryer, id uint64, backupEligible *bool, lock bool) (*passkeyUser, error) {
	u := &passkeyUser{id: id}
	var name sql.NullString
	userQuery := `SELECT email,display_name FROM users WHERE id=? AND is_deleted=0`
	credentialQuery := `SELECT credential_id,public_key,sign_count FROM passkeys WHERE user_id=?`
	if lock {
		userQuery += ` FOR UPDATE`
		credentialQuery += ` FOR UPDATE`
	}
	if err := db.QueryRowContext(ctx, userQuery, id).Scan(&u.email, &name); err != nil {
		return nil, err
	}
	u.displayName = name.String
	rows, err := db.QueryContext(ctx, credentialQuery, id)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var credentialID, publicKey string
		var signCount int64
		if err := rows.Scan(&credentialID, &publicKey, &signCount); err != nil {
			rows.Close()
			return nil, err
		}
		credential, err := decodeStoredWebauthnCredential(credentialID, publicKey, signCount, backupEligible)
		if err != nil {
			rows.Close()
			return nil, err
		}
		u.credentials = append(u.credentials, credential)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	return u, nil
}

func encodeStoredWebauthnCredential(credential *webauthn.Credential) (string, error) {
	if credential == nil {
		return "", fmt.Errorf("credential is nil")
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return "", err
	}
	return storedWebauthnCredentialPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeStoredWebauthnCredential(credentialID, publicKey string, signCount int64, legacyBackupEligible *bool) (webauthn.Credential, error) {
	if strings.HasPrefix(publicKey, storedWebauthnCredentialPrefix) {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(publicKey, storedWebauthnCredentialPrefix))
		if err != nil {
			return webauthn.Credential{}, fmt.Errorf("decode stored credential: %w", err)
		}
		var credential webauthn.Credential
		if err := json.Unmarshal(raw, &credential); err != nil {
			return webauthn.Credential{}, fmt.Errorf("unmarshal stored credential: %w", err)
		}
		credential.Flags = normalizedCredentialFlags(credential.Flags)
		return credential, nil
	}
	id, err := decodeBase64(credentialID)
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("decode credential ID: %w", err)
	}
	key, err := decodeBase64(publicKey)
	if err != nil {
		return webauthn.Credential{}, fmt.Errorf("decode public key: %w", err)
	}
	if signCount < 0 || signCount > math.MaxUint32 {
		return webauthn.Credential{}, fmt.Errorf("invalid sign count")
	}
	flags := webauthn.CredentialFlags{}
	if legacyBackupEligible != nil {
		flags.BackupEligible = *legacyBackupEligible
	}
	return webauthn.Credential{
		ID: id, PublicKey: key, Flags: normalizedCredentialFlags(flags),
		Authenticator: webauthn.Authenticator{SignCount: uint32(signCount)},
	}, nil
}

func normalizedCredentialFlags(flags webauthn.CredentialFlags) webauthn.CredentialFlags {
	var raw protocol.AuthenticatorFlags
	if flags.UserPresent {
		raw |= protocol.FlagUserPresent
	}
	if flags.UserVerified {
		raw |= protocol.FlagUserVerified
	}
	if flags.BackupEligible {
		raw |= protocol.FlagBackupEligible
	}
	if flags.BackupState {
		raw |= protocol.FlagBackupState
	}
	return webauthn.NewCredentialFlags(raw)
}

func decodeBase64(value string) ([]byte, error) {
	for _, encoding := range []*base64.Encoding{base64.RawStdEncoding, base64.RawURLEncoding, base64.StdEncoding, base64.URLEncoding} {
		if decoded, err := encoding.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	return nil, fmt.Errorf("unsupported base64 encoding")
}
