package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/auron-labs/dnsync/internal/config"
)

const (
	defaultLogMaxBytes int64 = 10 << 20
	defaultLogBackups        = 5
)

type rotatingFile struct {
	mu       sync.Mutex
	path     string
	maxBytes int64
	backups  int
	file     *os.File
	size     int64
}

func newRotatingFile(path string, maxBytes int64, backups int) (*rotatingFile, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("path is required")
	}
	if maxBytes <= 0 {
		return nil, errors.New("max bytes must be greater than zero")
	}
	if backups < 1 {
		return nil, errors.New("backup count must be at least one")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	r := &rotatingFile{path: path, maxBytes: maxBytes, backups: backups}
	if err := r.open(); err != nil {
		return nil, err
	}
	return r, nil
}

func (r *rotatingFile) Write(payload []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return 0, os.ErrClosed
	}
	if r.size > 0 && r.size+int64(len(payload)) > r.maxBytes {
		if err := r.rotate(); err != nil {
			return 0, fmt.Errorf("rotate log file: %w", err)
		}
	}
	n, err := r.file.Write(payload)
	r.size += int64(n)
	if err != nil {
		return n, fmt.Errorf("write log file: %w", err)
	}
	return n, nil
}

func (r *rotatingFile) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.file == nil {
		return nil
	}
	err := r.file.Close()
	r.file = nil
	if err != nil {
		return fmt.Errorf("close log file: %w", err)
	}
	return nil
}

func (r *rotatingFile) open() error {
	file, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("open rotating log: %w", err)
	}
	if err := file.Chmod(0o600); err != nil {
		_ = file.Close()
		return fmt.Errorf("set rotating log permissions: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("stat rotating log: %w", err)
	}
	r.file = file
	r.size = info.Size()
	return nil
}

func (r *rotatingFile) rotate() error {
	if err := r.file.Close(); err != nil {
		return fmt.Errorf("close rotating log: %w", err)
	}
	r.file = nil
	for index := r.backups - 1; index >= 1; index-- {
		oldPath := fmt.Sprintf("%s.%d", r.path, index)
		newPath := fmt.Sprintf("%s.%d", r.path, index+1)
		if err := os.Rename(oldPath, newPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("rotate log backup: %w", err)
		}
	}
	if err := os.Rename(r.path, r.path+".1"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("rotate active log: %w", err)
	}
	return r.open()
}

var structuredSecretPattern = regexp.MustCompile(`(?i)(authorization|bearer|token|password|api[_-]?key)([=: ]+)([^\s,;]+)`)

type redactingHandler struct {
	next    slog.Handler
	secrets []string
}

func newRedactingHandler(next slog.Handler, secrets []string) slog.Handler {
	filtered := make([]string, 0, len(secrets))
	for _, secret := range secrets {
		if secret = strings.TrimSpace(secret); secret != "" {
			filtered = append(filtered, secret)
		}
	}
	return redactingHandler{next: next, secrets: filtered}
}

func (h redactingHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h redactingHandler) Handle(ctx context.Context, record slog.Record) error {
	next := slog.NewRecord(record.Time, record.Level, h.redact(record.Message), record.PC)
	record.Attrs(func(attr slog.Attr) bool {
		next.AddAttrs(h.redactAttr(attr))
		return true
	})
	if err := h.next.Handle(ctx, next); err != nil {
		return fmt.Errorf("handle log record: %w", err)
	}
	return nil
}

func (h redactingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	redacted := make([]slog.Attr, len(attrs))
	for i, attr := range attrs {
		redacted[i] = h.redactAttr(attr)
	}
	return redactingHandler{next: h.next.WithAttrs(redacted), secrets: h.secrets}
}

func (h redactingHandler) WithGroup(name string) slog.Handler {
	return redactingHandler{next: h.next.WithGroup(name), secrets: h.secrets}
}

func (h redactingHandler) redactAttr(attr slog.Attr) slog.Attr {
	if secretAttributeKey(attr.Key) {
		return slog.String(attr.Key, "[redacted]")
	}
	value := attr.Value.Resolve()
	if value.Kind() == slog.KindGroup {
		attrs := value.Group()
		for i := range attrs {
			attrs[i] = h.redactAttr(attrs[i])
		}
		return slog.Attr{Key: attr.Key, Value: slog.GroupValue(attrs...)}
	}
	if value.Kind() == slog.KindString {
		return slog.String(attr.Key, h.redact(value.String()))
	}
	if value.Kind() == slog.KindAny {
		return slog.String(attr.Key, h.redact(fmt.Sprint(value.Any())))
	}
	return attr
}

func secretAttributeKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return strings.Contains(key, "token") || strings.Contains(key, "password") || strings.Contains(key, "authorization") || strings.Contains(key, "api_key") || strings.Contains(key, "api-key")
}

func (h redactingHandler) redact(value string) string {
	redacted := structuredSecretPattern.ReplaceAllString(value, "$1$2[redacted]")
	for _, secret := range h.secrets {
		redacted = strings.ReplaceAll(redacted, secret, "[redacted]")
	}
	return redacted
}

func configSecrets(cfg config.Config) []string {
	secrets := make([]string, 0, len(cfg.Outputs)*2)
	for _, output := range cfg.Outputs {
		secrets = append(secrets, output.Password, output.APIKey)
	}
	return secrets
}
