package runtime

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseLogLevelSupportsTrace(t *testing.T) {
	t.Parallel()

	level, err := parseLogLevel("trace")
	if err != nil {
		t.Fatalf("parseLogLevel returned error: %v", err)
	}
	if level != LevelTrace {
		t.Fatalf("expected trace level %v, got %v", LevelTrace, level)
	}
}

func TestNewRuntimeDepsInfoDisablesTrace(t *testing.T) {
	t.Parallel()

	deps, err := newRuntimeDeps(testRuntimeConfig(t.TempDir()), emptyRuntimeEnv, io.Discard)
	if err != nil {
		t.Fatalf("NewRuntimeDeps returned error: %v", err)
	}
	if deps.Logger.Enabled(context.Background(), LevelTrace) {
		t.Fatal("expected info logger to disable trace level")
	}
}

func TestNewRuntimeDepsDefaultsOperationTimeout(t *testing.T) {
	t.Parallel()

	deps, err := newRuntimeDeps(testRuntimeConfig(t.TempDir()), emptyRuntimeEnv, io.Discard)
	if err != nil {
		t.Fatalf("NewRuntimeDeps returned error: %v", err)
	}

	if deps.OperationTimeout != defaultOperationTimeout {
		t.Fatalf("expected default operation timeout %v, got %v", defaultOperationTimeout, deps.OperationTimeout)
	}
}

func TestNewRuntimeDepsUsesConfiguredOperationTimeout(t *testing.T) {
	t.Parallel()

	cfg := testRuntimeConfig(t.TempDir())
	cfg.Runtime.OperationTimeout = "25s"

	deps, err := newRuntimeDeps(cfg, emptyRuntimeEnv, io.Discard)
	if err != nil {
		t.Fatalf("NewRuntimeDeps returned error: %v", err)
	}

	if deps.OperationTimeout != 25*time.Second {
		t.Fatalf("expected configured operation timeout %v, got %v", 25*time.Second, deps.OperationTimeout)
	}
}

func TestNewRuntimeDepsLoggingEnvironmentAndRotationDefaults(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "logs", "daemon.log")
	deps, err := newRuntimeDeps(testRuntimeConfig(t.TempDir()), func(key string) string {
		switch key {
		case "LOG_LEVEL":
			return "debug"
		case "LOG_FILE":
			return path
		default:
			return ""
		}
	}, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer deps.LogCloser.Close()
	if deps.LogLevel != slog.LevelDebug {
		t.Fatalf("LOG_LEVEL did not override config: %v", deps.LogLevel)
	}
	writer, ok := deps.LogCloser.(*rotatingFile)
	if !ok {
		t.Fatalf("LOG_FILE did not install rotating writer: %T", deps.LogCloser)
	}
	if writer.maxBytes != defaultLogMaxBytes || writer.backups != defaultLogBackups {
		t.Fatalf("unexpected rotation defaults: max=%d backups=%d", writer.maxBytes, writer.backups)
	}
}

func TestNewRuntimeDepsRejectsInvalidEnvironmentLevelAndLogFile(t *testing.T) {
	t.Parallel()
	if _, err := newRuntimeDeps(testRuntimeConfig(t.TempDir()), func(key string) string {
		if key == "LOG_LEVEL" {
			return "verbose"
		}
		return ""
	}, io.Discard); err == nil || !strings.Contains(err.Error(), "logging.level") {
		t.Fatalf("expected invalid LOG_LEVEL error, got %v", err)
	}

	parentFile := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(parentFile, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := newRuntimeDeps(testRuntimeConfig(t.TempDir()), func(key string) string {
		if key == "LOG_FILE" {
			return filepath.Join(parentFile, "daemon.log")
		}
		return ""
	}, io.Discard); err == nil || !strings.Contains(err.Error(), "open LOG_FILE") {
		t.Fatalf("expected LOG_FILE open error, got %v", err)
	}
}

func TestNewRuntimeDepsRemovalGraceDefaultsAndAllowsZero(t *testing.T) {
	t.Parallel()
	deps, err := newRuntimeDeps(testRuntimeConfig(t.TempDir()), emptyRuntimeEnv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if deps.RemovalGrace != defaultRemovalGrace {
		t.Fatalf("default removal grace = %v, want %v", deps.RemovalGrace, defaultRemovalGrace)
	}
	cfg := testRuntimeConfig(t.TempDir())
	cfg.Runtime.RemovalGracePeriod = "0s"
	deps, err = newRuntimeDeps(cfg, emptyRuntimeEnv, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if deps.RemovalGrace != 0 {
		t.Fatalf("zero removal grace = %v", deps.RemovalGrace)
	}
}

func emptyRuntimeEnv(string) string { return "" }
