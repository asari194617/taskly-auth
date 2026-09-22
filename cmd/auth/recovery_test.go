package main

import (
	"strings"
	"testing"
)

func TestRecoveryCodeLegacyFormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		code, err := newRecoveryCode()
		if err != nil {
			t.Fatal(err)
		}
		if len(code) != 19 || code[4] != '-' || code[9] != '-' || code[14] != '-' {
			t.Fatalf("invalid code format: %q", code)
		}
		for index, character := range code {
			if index == 4 || index == 9 || index == 14 {
				continue
			}
			if !strings.ContainsRune(recoveryAlphabet, character) {
				t.Fatalf("invalid code character: %q", code)
			}
		}
		if seen[code] {
			t.Fatalf("duplicate recovery code")
		}
		seen[code] = true
	}
}
