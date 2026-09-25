package main

import "testing"

func TestTOTPEncryptionUsesLegacyFormat(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	const key = "test-encryption-key"
	encrypted, err := encryptTOTP(secret, key)
	if err != nil {
		t.Fatal(err)
	}
	if encrypted == secret {
		t.Fatal("secret was not encrypted")
	}
	decrypted, err := decryptTOTP(encrypted, key)
	if err != nil {
		t.Fatal(err)
	}
	if decrypted != secret {
		t.Fatalf("decryptTOTP() = %q, want %q", decrypted, secret)
	}
}

func TestValidTOTPCode(t *testing.T) {
	for _, test := range []struct {
		code string
		want bool
	}{
		{"123456", true},
		{"000000", true},
		{"12345", false},
		{"1234567", false},
		{"12 456", false},
		{"１２３４５６", false},
	} {
		if got := validTOTPCode(test.code); got != test.want {
			t.Errorf("validTOTPCode(%q) = %v, want %v", test.code, got, test.want)
		}
	}
}
