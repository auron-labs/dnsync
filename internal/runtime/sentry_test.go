package runtime

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
)

type captureTransport struct{ events []*sentry.Event }

func (*captureTransport) Flush(time.Duration) bool              { return true }
func (*captureTransport) FlushWithContext(context.Context) bool { return true }
func (*captureTransport) Configure(sentry.ClientOptions)        {}
func (c *captureTransport) SendEvent(event *sentry.Event)       { c.events = append(c.events, event) }
func (*captureTransport) Close()                                {}

func newTestHub(t *testing.T, dsn string) (*sentry.Hub, *captureTransport) {
	t.Helper()
	transport := &captureTransport{}
	client, err := sentry.NewClient(sentry.ClientOptions{Dsn: dsn, Transport: transport})
	if err != nil {
		t.Fatal(err)
	}
	return sentry.NewHub(client, sentry.NewScope()), transport
}

func TestSentryHandlerReportsRedactedErrorsOnly(t *testing.T) {
	t.Parallel()
	hub, transport := newTestHub(t, "https://key@example.invalid/1")
	if !sentryEnabled(hub) {
		t.Fatal("sentry should be enabled with a DSN")
	}
	base := slog.DiscardHandler
	logger := slog.New(newRedactingHandler(slog.NewMultiHandler(base, sentryHandler{hub: hub}), []string{"hunter2"})).
		With("output", "adguard")

	logger.Info("started", "password", "hunter2")
	logger.Error("write failed: hunter2", "error", "token=abc hunter2", "password", "hunter2")

	if len(transport.events) != 1 {
		t.Fatalf("events = %d, want 1", len(transport.events))
	}
	event := transport.events[0]
	if event.Level != sentry.LevelError || event.Contexts["log"]["output"] != "adguard" {
		t.Fatalf("unexpected event: %+v", event)
	}
	if strings.Contains(event.Message, "hunter2") {
		t.Fatalf("message not redacted: %q", event.Message)
	}
	for key, value := range event.Contexts["log"] {
		if s, ok := value.(string); ok && (strings.Contains(s, "hunter2") || strings.Contains(s, "abc")) {
			t.Fatalf("extra %q not redacted: %q", key, s)
		}
	}
}

func TestSentryDisabledWithoutDSN(t *testing.T) {
	t.Parallel()
	hub, _ := newTestHub(t, "")
	if sentryEnabled(hub) {
		t.Fatal("sentry should be disabled without a DSN")
	}
}
