package webhook

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type fakeSpawner struct {
	calls   int
	session string
	work    string
	err     error
}

func (f *fakeSpawner) Spawn(_ *http.Request, sessionID, workID string) (string, error) {
	f.calls++
	f.session = sessionID
	f.work = workID
	if f.err != nil {
		return "", f.err
	}
	return "session-" + sessionID, nil
}

func newTestHandler(spawner Spawner, secret []byte) *Handler {
	key := "whsec_" + base64.StdEncoding.EncodeToString(secret)
	return &Handler{
		Verifier: NewVerifier(key),
		Spawner:  spawner,
		Log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func signedRequest(secret, body []byte) *http.Request {
	id := "msg_1"
	ts := fmt.Sprintf("%d", time.Now().Unix())
	req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
	req.Header.Set(HeaderWebhookID, id)
	req.Header.Set(HeaderWebhookTimestamp, ts)
	req.Header.Set(HeaderWebhookSignature, signFixture(secret, id, ts, body))
	return req
}

func TestHandler_RunStartedSpawnsJob(t *testing.T) {
	secret := []byte("topsecret")
	spawner := &fakeSpawner{}
	h := newTestHandler(spawner, secret)

	body := []byte(`{"type":"session.status_run_started","data":{"session_id":"sess_abc","work_id":"work_xyz"}}`)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, signedRequest(secret, body))

	if rec.Code != http.StatusAccepted {
		t.Fatalf("expected 202, got %d (%s)", rec.Code, rec.Body)
	}
	if spawner.calls != 1 || spawner.session != "sess_abc" || spawner.work != "work_xyz" {
		t.Fatalf("spawner not called correctly: %+v", spawner)
	}
}

func TestHandler_RejectsBadSignature(t *testing.T) {
	spawner := &fakeSpawner{}
	h := newTestHandler(spawner, []byte("topsecret"))

	body := []byte(`{"type":"session.status_run_started","data":{"session_id":"x"}}`)
	req := signedRequest([]byte("WRONG-secret"), body)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", rec.Code)
	}
	if spawner.calls != 0 {
		t.Fatal("spawner should not be called on bad signature")
	}
}

func TestHandler_IgnoresOtherEvents(t *testing.T) {
	secret := []byte("topsecret")
	spawner := &fakeSpawner{}
	h := newTestHandler(spawner, secret)

	body := []byte(`{"type":"session.status_completed","data":{"session_id":"x"}}`)
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, signedRequest(secret, body))

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rec.Code)
	}
	if spawner.calls != 0 {
		t.Fatal("spawner should not be called for unrelated event")
	}
}

func TestHandler_Healthz(t *testing.T) {
	h := newTestHandler(&fakeSpawner{}, []byte("s"))
	rec := httptest.NewRecorder()
	h.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 from healthz, got %d", rec.Code)
	}
}

func TestParseEvent(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		wantType    string
		wantSession string
		wantWork    string
	}{
		{"flat", `{"type":"e","session_id":"s1","work_id":"w1"}`, "e", "s1", "w1"},
		{"nested data", `{"type":"e","data":{"session_id":"s2","work_id":"w2"}}`, "e", "s2", "w2"},
		{"nested session object", `{"type":"e","data":{"session":{"id":"s3"}}}`, "e", "s3", ""},
		{"missing ids", `{"type":"e"}`, "e", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			evt, err := parseEvent([]byte(tc.body))
			if err != nil {
				t.Fatalf("parse error: %v", err)
			}
			if evt.Type != tc.wantType || evt.SessionID != tc.wantSession || evt.WorkID != tc.wantWork {
				t.Fatalf("got %+v", evt)
			}
		})
	}
}
