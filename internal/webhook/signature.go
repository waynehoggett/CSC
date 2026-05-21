package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Signature header names (Svix-compatible; Anthropic webhook keys are whsec_…).
const (
	HeaderWebhookID        = "webhook-id"
	HeaderWebhookTimestamp = "webhook-timestamp"
	HeaderWebhookSignature = "webhook-signature"
)

// DefaultTolerance bounds how far a webhook timestamp may drift to mitigate replay.
const DefaultTolerance = 5 * time.Minute

// Verifier validates inbound webhook signatures against a signing key.
type Verifier struct {
	secret    []byte
	tolerance time.Duration
	now       func() time.Time
}

// NewVerifier parses a signing key (optionally whsec_-prefixed and base64) and
// returns a Verifier. The remainder after the prefix is base64-decoded; if that
// fails the raw bytes are used, supporting plain secrets too.
func NewVerifier(signingKey string) *Verifier {
	raw := strings.TrimPrefix(signingKey, "whsec_")
	secret, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		secret = []byte(raw)
	}
	return &Verifier{secret: secret, tolerance: DefaultTolerance, now: time.Now}
}

// Verify checks the signature over the canonical "id.timestamp.body" content.
func (v *Verifier) Verify(id, timestamp string, body []byte, signatureHeader string) error {
	if id == "" || timestamp == "" || signatureHeader == "" {
		return fmt.Errorf("missing webhook signature headers")
	}
	if err := v.checkTimestamp(timestamp); err != nil {
		return err
	}

	signed := fmt.Sprintf("%s.%s.%s", id, timestamp, body)
	mac := hmac.New(sha256.New, v.secret)
	mac.Write([]byte(signed))
	expected := mac.Sum(nil)

	for _, tok := range strings.Fields(signatureHeader) {
		// Each token looks like "v1,<base64sig>"; bare base64 is also accepted.
		sig := tok
		if i := strings.IndexByte(tok, ','); i >= 0 {
			sig = tok[i+1:]
		}
		got, err := base64.StdEncoding.DecodeString(sig)
		if err != nil {
			continue
		}
		if hmac.Equal(got, expected) {
			return nil
		}
	}
	return fmt.Errorf("no matching signature")
}

func (v *Verifier) checkTimestamp(ts string) error {
	secs, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return fmt.Errorf("invalid webhook timestamp %q", ts)
	}
	delta := v.now().Sub(time.Unix(secs, 0))
	if delta < 0 {
		delta = -delta
	}
	if delta > v.tolerance {
		return fmt.Errorf("webhook timestamp outside tolerance (%s)", delta)
	}
	return nil
}
