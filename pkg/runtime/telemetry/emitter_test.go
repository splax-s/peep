package telemetry

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestEmitSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/runtime/events" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		if token := r.Header.Get("X-Builder-Token"); token != "secret" {
			t.Fatalf("unexpected token header %s", token)
		}
		var payload map[string]any
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode payload: %v", err)
		}
		if payload["project_id"] != "proj-123" {
			t.Fatalf("unexpected project_id %v", payload["project_id"])
		}
		if payload["level"] != "info" {
			t.Fatalf("expected default level info, got %v", payload["level"])
		}
		if payload["occurred_at"] == "" {
			t.Fatalf("expected occurred_at to be populated")
		}
		w.WriteHeader(http.StatusAccepted)
	}))
	defer srv.Close()

	emitter, err := NewEmitter(srv.URL+"/", " secret ", nil)
	if err != nil {
		t.Fatalf("new emitter: %v", err)
	}
	event := Event{ProjectID: "proj-123", Message: "hello"}
	if err := emitter.Emit(context.Background(), event); err != nil {
		t.Fatalf("emit: %v", err)
	}
}

func TestEmitUnauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "invalid token", http.StatusUnauthorized)
	}))
	defer srv.Close()

	emitter, err := NewEmitter(srv.URL, "", &http.Client{Timeout: time.Second})
	if err != nil {
		t.Fatalf("new emitter: %v", err)
	}
	err = emitter.Emit(context.Background(), Event{ProjectID: "proj"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}

func TestEmitRequiresProjectID(t *testing.T) {
	emitter, err := NewEmitter("https://api.example.com", "", nil)
	if err != nil {
		t.Fatalf("new emitter: %v", err)
	}
	if err := emitter.Emit(context.Background(), Event{}); err == nil {
		t.Fatal("expected validation error")
	}
}
