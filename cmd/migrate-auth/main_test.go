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
