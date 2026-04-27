package oidc

import (
	"errors"
	"testing"
)

func TestValidateReturnTo(t *testing.T) {
	hosts := []string{"app.example.com", "test.example.com"}

	cases := []struct {
		name      string
		raw       string
		want      string
		wantError bool
	}{
		{"empty allowed", "", "", false},
		{"relative path allowed", "/home", "/home", false},
		{"relative with query", "/home?x=1", "/home?x=1", false},
		{"protocol-relative rejected", "//evil.com/x", "", true},
		{"https in whitelist allowed", "https://app.example.com/foo", "https://app.example.com/foo", false},
		{"http in whitelist allowed", "http://test.example.com/foo", "http://test.example.com/foo", false},
		{"case insensitive host", "https://APP.EXAMPLE.COM/foo", "https://APP.EXAMPLE.COM/foo", false},
		{"host not in whitelist", "https://evil.com/foo", "", true},
		{"javascript scheme rejected", "javascript:alert(1)", "", true},
		{"data scheme rejected", "data:text/html,<script>", "", true},
		{"ftp scheme rejected", "ftp://app.example.com/foo", "", true},
		{"explicit https port allowed", "https://app.example.com:443/foo", "https://app.example.com:443/foo", false},
		{"explicit non-default port allowed", "https://app.example.com:8443/foo", "https://app.example.com:8443/foo", false},
		{"port-only spoof not bypass", "https://evil.com:80/foo", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ValidateReturnTo(tc.raw, hosts)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				if !errors.Is(err, ErrReturnToRejected) {
					t.Fatalf("expected ErrReturnToRejected, got %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestValidateReturnTo_EmptyWhitelist(t *testing.T) {
	// 空白名单 + 绝对 URL → 一律拒
	_, err := ValidateReturnTo("https://app.example.com/foo", nil)
	if !errors.Is(err, ErrReturnToRejected) {
		t.Fatalf("expected ErrReturnToRejected, got %v", err)
	}
	// 空白名单 + 相对路径仍允许(host 由前端决定)
	got, err := ValidateReturnTo("/home", nil)
	if err != nil || got != "/home" {
		t.Fatalf("relative path should pass with empty whitelist, got %q err=%v", got, err)
	}
}
