package main

import "testing"

func TestSameColumnsRejectsSchemaDrift(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source []string
		target []string
		want   bool
	}{
		{"matching", []string{"id", "is_deleted", "deleted_at"}, []string{"id", "is_deleted", "deleted_at"}, true},
		{"missing deletion time", []string{"id", "is_deleted", "deleted_at"}, []string{"id", "is_deleted"}, false},
		{"different order", []string{"id", "is_deleted", "deleted_at"}, []string{"id", "deleted_at", "is_deleted"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := sameColumns(tc.source, tc.target); got != tc.want {
				t.Fatalf("sameColumns(%v, %v) = %v, want %v", tc.source, tc.target, got, tc.want)
			}
		})
	}
}

func TestNormalizeLegacyBlockExpiryPreservesPermanentAndConvertsJST(t *testing.T) {
	columns := []string{"id", "expires_at", "reason"}
	values := []any{[]byte("7"), []byte("2026-11-17 10:00:00"), []byte("test")}
	if err := normalizeBlockExpiry(columns, values); err != nil {
		t.Fatal(err)
	}
	if got := string(values[1].([]byte)); got != "2026-11-17 01:00:00" {
		t.Fatalf("normalized expiry = %s", got)
	}
	permanent := []any{[]byte("8"), nil, []byte("test")}
	if err := normalizeBlockExpiry(columns, permanent); err != nil || permanent[1] != nil {
		t.Fatalf("permanent block changed: %v, %v", permanent, err)
	}
}

func TestMigrationAcceptsOnlyDefaultedAuthEpochSchemaAddition(t *testing.T) {
	legacy := []string{"id", "email_code_allowed", "is_deleted"}
	current := []string{"id", "email_code_allowed", "auth_epoch", "is_deleted"}
	if !compatibleMigrationColumns("users", legacy, current) {
		t.Fatal("legacy users cannot be copied into new auth schema")
	}
	if compatibleMigrationColumns("blocks", legacy, current) || compatibleMigrationColumns("users", legacy, []string{"id", "email_code_allowed", "other", "is_deleted"}) {
		t.Fatal("unrelated schema drift was accepted")
	}
}
