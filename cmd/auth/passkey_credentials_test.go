package main

import (
	"bytes"
	"encoding/base64"
	"testing"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

func TestImportedLegacyPasskeyKeepsIDKeyCounterAndBackupHint(t *testing.T) {
	credentialID := []byte("legacy-credential-id")
	publicKey := []byte{0xa5, 0x01, 0x02, 0x03, 0x26}
	eligible := true
	credential, err := decodeStoredWebauthnCredential(
		base64.RawStdEncoding.EncodeToString(credentialID),
		base64.RawStdEncoding.EncodeToString(publicKey), 7, &eligible,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(credential.ID, credentialID) || !bytes.Equal(credential.PublicKey, publicKey) || credential.Authenticator.SignCount != 7 || !credential.Flags.BackupEligible {
		t.Fatalf("imported credential changed: %+v", credential)
	}
	if got := string((&passkeyUser{id: 42}).WebAuthnID()); got != "42" {
		t.Fatalf("existing user handle changed: %q", got)
	}
}

func TestImportedCurrentPasskeyKeepsStoredFlags(t *testing.T) {
	want := &webauthn.Credential{
		ID: []byte("credential-id"), PublicKey: []byte{0xa5, 0x01, 0x02, 0x03, 0x26},
		Flags:         webauthn.NewCredentialFlags(protocol.FlagUserPresent | protocol.FlagUserVerified | protocol.FlagBackupEligible | protocol.FlagBackupState),
		Authenticator: webauthn.Authenticator{SignCount: 9},
	}
	stored, err := encodeStoredWebauthnCredential(want)
	if err != nil {
		t.Fatal(err)
	}
	falseHint := false
	got, err := decodeStoredWebauthnCredential(base64.RawStdEncoding.EncodeToString(want.ID), stored, 9, &falseHint)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got.ID, want.ID) || !bytes.Equal(got.PublicKey, want.PublicKey) || got.Authenticator.SignCount != 9 || !got.Flags.BackupEligible || !got.Flags.BackupState {
		t.Fatalf("stored credential metadata changed: %+v", got)
	}
}

func TestExistingPasskeyRequestKeepsParentRPID(t *testing.T) {
	service, err := webauthn.New(&webauthn.Config{
		RPDisplayName: "Taskly",
		RPID:          "taskly.iy0306.com",
		RPOrigins:     []string{"https://auth.taskly.iy0306.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	user := &passkeyUser{id: 42, email: "test@example.com", credentials: []webauthn.Credential{{ID: []byte("old-passkey")}}}
	assertion, _, err := service.BeginLogin(user)
	if err != nil {
		t.Fatal(err)
	}
	if assertion.Response.RelyingPartyID != "taskly.iy0306.com" {
		t.Fatalf("passkey RP ID changed: %q", assertion.Response.RelyingPartyID)
	}
	if len(assertion.Response.AllowedCredentials) != 1 || !bytes.Equal(assertion.Response.AllowedCredentials[0].CredentialID, []byte("old-passkey")) {
		t.Fatalf("old passkey not offered: %+v", assertion.Response.AllowedCredentials)
	}
	creation, _, err := service.BeginRegistration(user, webauthn.WithExclusions(webauthn.Credentials(user.WebAuthnCredentials()).CredentialDescriptors()))
	if err != nil {
		t.Fatal(err)
	}
	userHandle, ok := creation.Response.User.ID.(protocol.URLEncodedBase64)
	if creation.Response.RelyingParty.ID != "taskly.iy0306.com" || !ok || !bytes.Equal(userHandle, []byte("42")) {
		t.Fatalf("new passkey registration changed RP or user handle: %+v", creation.Response)
	}
	if len(creation.Response.CredentialExcludeList) != 1 || !bytes.Equal(creation.Response.CredentialExcludeList[0].CredentialID, []byte("old-passkey")) {
		t.Fatalf("old passkey not excluded during registration: %+v", creation.Response.CredentialExcludeList)
	}
}
