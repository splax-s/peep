package deploy

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/splax/localvercel/pkg/config"
)

func TestSuppressionExpires(t *testing.T) {
	svc := Service{
		cfg:           config.BuilderConfig{CallbackSuppressionTTL: 20 * time.Millisecond},
		logSuppressed: &sync.Map{},
	}

	svc.suppress("project-1")

	if !svc.shouldSuppress("project-1") {
		t.Fatalf("expected suppression to be active immediately")
	}

	time.Sleep(50 * time.Millisecond)

	if svc.shouldSuppress("project-1") {
		t.Fatalf("expected suppression to expire after TTL")
	}
}

func TestSuppressionCleared(t *testing.T) {
	svc := Service{
		cfg:           config.BuilderConfig{},
		logSuppressed: &sync.Map{},
	}

	svc.suppress("project-2")

	if !svc.shouldSuppress("project-2") {
		t.Fatalf("expected suppression to be active before clear")
	}

	svc.clearSuppression("project-2")

	if svc.shouldSuppress("project-2") {
		t.Fatalf("expected suppression to be cleared")
	}
}

func TestNotifyStatusSuppressesAfterClientError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
	}))
	defer server.Close()

	logger := slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{Level: slog.LevelDebug}))
	svc := Service{
		logger:        logger,
		cfg:           config.BuilderConfig{DeployCallbackURL: server.URL, CallbackSuppressionTTL: 200 * time.Millisecond},
		statusClient:  server.Client(),
		logSuppressed: &sync.Map{},
	}

	req := Request{DeploymentID: "dep-1", ProjectID: "project-1"}
	if svc.shouldSuppress(req.ProjectID) {
		t.Fatalf("suppression should be empty before request")
	}

	if ok := svc.notifyStatus(req, "running", "stage", "msg", "image", "", nil, nil); ok {
		t.Fatalf("notifyStatus should return false when callback fails with 4xx")
	}

	if !svc.shouldSuppress(req.ProjectID) {
		t.Fatalf("expected project to be suppressed after 4xx response")
	}
}
