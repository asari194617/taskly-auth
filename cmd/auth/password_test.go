package main

import "testing"

func TestValidNewPassword(t *testing.T) {
	for _, test := range []struct {
		value string
		want  bool
	}{
		{"short", false},
		{"        ", false},
		{"password123", true},
		{"日本語パスワード", true},
		{"1234567\xff", false},
		{string(make([]byte, 73)), false},
	} {
		if got := validNewPassword(test.value); got != test.want {
			t.Errorf("validNewPassword(%q) = %v, want %v", test.value, got, test.want)
		}
	}
}
