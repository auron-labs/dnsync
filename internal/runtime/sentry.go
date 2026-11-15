package runtime

import (
	"context"
	"log/slog"
	"strings"

	"github.com/getsentry/sentry-go"
)

// sentryHandler reports error-level records to Sentry as events. It sits behind
// redactingHandler so only redacted messages and attributes are sent.
type sentryHandler struct {
	hub    *sentry.Hub
	attrs  []slog.Attr
	groups []string
}

func sentryEnabled(hub *sentry.Hub) bool {
	client := hub.Client()
	return client != nil && client.Options().Dsn != ""
}

func (h sentryHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= slog.LevelError
}

func (h sentryHandler) Handle(_ context.Context, record slog.Record) error {
	extra := sentry.Context{}
	for _, attr := range h.attrs {
		addSentryAttr(extra, "", attr)
	}
	prefix := strings.Join(h.groups, ".")
	if prefix != "" {
		prefix += "."
	}
	record.Attrs(func(attr slog.Attr) bool {
		addSentryAttr(extra, prefix, attr)
		return true
	})
	h.hub.CaptureEvent(&sentry.Event{
		Level:    sentry.LevelError,
		Message:  record.Message,
		Logger:   "dnsync",
		Contexts: map[string]sentry.Context{"log": extra},
	})
	return nil
}

func (h sentryHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	prefix := strings.Join(h.groups, ".")
	if prefix != "" {
		prefix += "."
	}
	next := h
	next.attrs = append([]slog.Attr(nil), h.attrs...)
	for _, attr := range attrs {
		attr.Key = prefix + attr.Key
		next.attrs = append(next.attrs, attr)
	}
	return next
}

func (h sentryHandler) WithGroup(name string) slog.Handler {
	next := h
	next.groups = append(append([]string(nil), h.groups...), name)
	return next
}

func addSentryAttr(extra sentry.Context, prefix string, attr slog.Attr) {
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		for _, member := range value.Group() {
			addSentryAttr(extra, prefix+attr.Key+".", member)
		}
		return
	}
	extra[prefix+attr.Key] = value.Any()
}
