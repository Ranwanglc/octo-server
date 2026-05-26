package trigger

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func sign(body []byte, secret string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write(body)
	return "sha256=" + hex.EncodeToString(m.Sum(nil))
}

func TestVerifyWebhookSignature_OK(t *testing.T) {
	body := []byte(`{"x":1}`)
	if err := VerifyWebhookSignature(body, "s3cret", sign(body, "s3cret"), "hmac-sha256"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestVerifyWebhookSignature_RawHex(t *testing.T) {
	body := []byte(`{"x":1}`)
	full := sign(body, "k")
	if err := VerifyWebhookSignature(body, "k", full[len("sha256="):], "sha256"); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestVerifyWebhookSignature_Mismatch(t *testing.T) {
	if err := VerifyWebhookSignature([]byte(`x`), "k", "sha256=000000", ""); err == nil {
		t.Fatalf("expected mismatch")
	}
}

func TestVerifyWebhookSignature_NoSecret(t *testing.T) {
	if err := VerifyWebhookSignature([]byte(`x`), "", "", ""); err != nil {
		t.Fatalf("err: %v", err)
	}
}

func TestVerifyWebhookSignature_MissingHeader(t *testing.T) {
	if err := VerifyWebhookSignature([]byte(`x`), "k", "", ""); err == nil {
		t.Fatalf("expected missing header err")
	}
}

func TestVerifyWebhookSignature_BadAlgo(t *testing.T) {
	if err := VerifyWebhookSignature([]byte(`x`), "k", "x", "md5"); err == nil {
		t.Fatalf("expected unsupported algo")
	}
}
