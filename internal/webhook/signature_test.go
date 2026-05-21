package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"testing"
	"time"
)

// signFixture produces a valid Svix-style signature header for the given content.
func signFixture(secret []byte, id, ts string, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(fmt.Sprintf("%s.%s.%s", id, ts, body)))
	return "v1," + base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func TestVerifier_Verify(t *testing.T) {
	secret := []byte("super-secret-bytes")
	key := "whsec_" + base64.StdEncoding.EncodeToString(secret)
	v := NewVerifier(key)

	id := "msg_123"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	body := []byte(`{"type":"session.status_run_started"}`)
	sig := signFixture(secret, id, ts, body)

	if err := v.Verify(id, ts, body, sig); err != nil {
		t.Fatalf("expected valid signature, got %v", err)
	}

	t.Run("tampered body", func(t *testing.T) {
		if err := v.Verify(id, ts, []byte(`{"type":"other"}`), sig); err == nil {
			t.Fatal("expected failure for tampered body")
		}
	})

	t.Run("missing headers", func(t *testing.T) {
		if err := v.Verify("", ts, body, sig); err == nil {
			t.Fatal("expected failure for missing id")
		}
	})

	t.Run("stale timestamp", func(t *testing.T) {
		old := fmt.Sprintf("%d", time.Now().Add(-time.Hour).Unix())
		staleSig := signFixture(secret, id, old, body)
		if err := v.Verify(id, old, body, staleSig); err == nil {
			t.Fatal("expected failure for stale timestamp")
		}
	})

	t.Run("multiple signatures one valid", func(t *testing.T) {
		header := "v1,AAAA " + sig
		if err := v.Verify(id, ts, body, header); err != nil {
			t.Fatalf("expected one valid signature to pass, got %v", err)
		}
	})
}

func TestNewVerifier_PlainSecretFallback(t *testing.T) {
	// A key whose remainder is not valid base64 should fall back to raw bytes.
	v := NewVerifier("whsec_not*base64*")
	if string(v.secret) != "not*base64*" {
		t.Fatalf("expected raw fallback, got %q", v.secret)
	}
}
