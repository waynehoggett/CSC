package webhook

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
)

// EventSessionRunStarted is the webhook event that triggers a session Job.
const EventSessionRunStarted = "session.status_run_started"

// maxBodyBytes caps the inbound request body.
const maxBodyBytes = 1 << 20 // 1 MiB

// Spawner creates a session Job for a claimed work item. JobSpawner implements it.
type Spawner interface {
	Spawn(r *http.Request, sessionID, workID string) (string, error)
}

// Handler verifies inbound webhooks and dispatches session Jobs.
type Handler struct {
	Verifier *Verifier
	Spawner  Spawner
	Log      *slog.Logger
}

// Routes returns the configured http.Handler.
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	mux.HandleFunc("POST /", h.handleWebhook)
	return mux
}

func (h *Handler) handleWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes))
	if err != nil {
		http.Error(w, "read error", http.StatusBadRequest)
		return
	}

	if err := h.Verifier.Verify(
		r.Header.Get(HeaderWebhookID),
		r.Header.Get(HeaderWebhookTimestamp),
		body,
		r.Header.Get(HeaderWebhookSignature),
	); err != nil {
		h.Log.Warn("signature verification failed", "err", err)
		http.Error(w, "invalid signature", http.StatusUnauthorized)
		return
	}

	evt, err := parseEvent(body)
	if err != nil {
		http.Error(w, "invalid payload", http.StatusBadRequest)
		return
	}

	if evt.Type != EventSessionRunStarted {
		// Acknowledge unrelated events so Anthropic does not retry them.
		h.Log.Info("ignoring event", "type", evt.Type)
		w.WriteHeader(http.StatusOK)
		return
	}

	if evt.SessionID == "" {
		http.Error(w, "event missing session id", http.StatusBadRequest)
		return
	}

	jobName, err := h.Spawner.Spawn(r, evt.SessionID, evt.WorkID)
	if err != nil {
		h.Log.Error("failed to spawn session job", "session", evt.SessionID, "err", err)
		http.Error(w, "failed to create job", http.StatusInternalServerError)
		return
	}

	h.Log.Info("spawned session job", "session", evt.SessionID, "job", jobName)
	w.WriteHeader(http.StatusAccepted)
	_, _ = fmt.Fprintf(w, "created job %s", jobName)
}

// event is the minimal subset of a webhook payload CSC needs.
type event struct {
	Type      string
	SessionID string
	WorkID    string
}

// parseEvent tolerantly extracts the event type and session/work ids from the
// webhook body, accepting either flat fields or a nested "data" object.
func parseEvent(body []byte) (event, error) {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(body, &raw); err != nil {
		return event{}, err
	}

	var evt event
	evt.Type = jsonString(raw, "type")
	evt.SessionID = firstNonEmpty(
		jsonString(raw, "session_id"),
		nestedString(raw, "data", "session_id"),
		nestedString(raw, "data", "session", "id"),
	)
	evt.WorkID = firstNonEmpty(
		jsonString(raw, "work_id"),
		nestedString(raw, "data", "work_id"),
	)
	return evt, nil
}

func jsonString(m map[string]json.RawMessage, key string) string {
	v, ok := m[key]
	if !ok {
		return ""
	}
	var s string
	if err := json.Unmarshal(v, &s); err != nil {
		return ""
	}
	return s
}

func nestedString(m map[string]json.RawMessage, keys ...string) string {
	cur := m
	for i, k := range keys {
		v, ok := cur[k]
		if !ok {
			return ""
		}
		if i == len(keys)-1 {
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return ""
			}
			return s
		}
		var next map[string]json.RawMessage
		if err := json.Unmarshal(v, &next); err != nil {
			return ""
		}
		cur = next
	}
	return ""
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
