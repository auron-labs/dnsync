package runtime

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/cenkalti/backoff/v5"

	"github.com/auron-labs/dnsync/internal/config"
)

const (
	LevelTrace               slog.Level = slog.LevelDebug - 4
	defaultWatchHintDebounce            = 500 * time.Millisecond
	defaultOperationTimeout             = 10 * time.Second
	defaultRemovalGrace                 = 10 * time.Minute
)

type RetryPolicy struct {
	InitialInterval time.Duration
	MaxInterval     time.Duration
	MaxElapsedTime  time.Duration
	// RandomizationFactor is the jitter applied to each delay; zero disables jitter.
	RandomizationFactor float64
}

func (p RetryPolicy) newBackOff() *backoff.ExponentialBackOff {
	return &backoff.ExponentialBackOff{
		InitialInterval:     p.InitialInterval,
		MaxInterval:         p.MaxInterval,
		Multiplier:          2,
		RandomizationFactor: p.RandomizationFactor,
	}
}

type RuntimeDeps struct {
	Logger            *slog.Logger
	LogLevel          slog.Level
	InstallationID    string
	OperationTimeout  time.Duration
	RemovalGrace      time.Duration
	Retry             RetryPolicy
	WatchHintDebounce time.Duration
	LogCloser         io.Closer
}

func NewRuntimeDeps(cfg config.Config) (RuntimeDeps, error) {
	return newRuntimeDeps(cfg, os.Getenv, os.Stdout)
}

func newRuntimeDeps(cfg config.Config, getenv func(string) string, stdout io.Writer) (RuntimeDeps, error) {
	levelName := cfg.Logging.Level
	if envLevel := strings.TrimSpace(getenv("LOG_LEVEL")); envLevel != "" {
		levelName = envLevel
	}
	level, err := parseLogLevel(levelName)
	if err != nil {
		return RuntimeDeps{}, err
	}

	retry, err := parseRetryPolicy(cfg.Retry)
	if err != nil {
		return RuntimeDeps{}, err
	}

	operationTimeout, err := parseOperationTimeout(cfg.Runtime)
	if err != nil {
		return RuntimeDeps{}, err
	}
	removalGrace, err := parseRemovalGrace(cfg.Runtime)
	if err != nil {
		return RuntimeDeps{}, err
	}

	handlerOptions := &slog.HandlerOptions{Level: level}
	writers := []io.Writer{stdout}
	var logCloser io.Closer
	if path := strings.TrimSpace(getenv("LOG_FILE")); path != "" {
		maxBytes := defaultLogMaxBytes
		if cfg.Logging.MaxSizeMB > 0 {
			maxBytes = int64(cfg.Logging.MaxSizeMB) << 20
		}
		backups := defaultLogBackups
		if cfg.Logging.MaxBackups > 0 {
			backups = cfg.Logging.MaxBackups
		}
		rotating, err := newRotatingFile(path, maxBytes, backups)
		if err != nil {
			return RuntimeDeps{}, fmt.Errorf("open LOG_FILE: %w", err)
		}
		writers = append(writers, rotating)
		logCloser = rotating
	}
	writer := io.MultiWriter(writers...)
	var handler slog.Handler
	switch strings.ToLower(strings.TrimSpace(cfg.Logging.Format)) {
	case "json":
		handler = slog.NewJSONHandler(writer, handlerOptions)
	case "text":
		handler = slog.NewTextHandler(writer, handlerOptions)
	default:
		if logCloser != nil {
			_ = logCloser.Close()
		}
		return RuntimeDeps{}, errors.New("logging.format must be one of json or text")
	}
	handler = newRedactingHandler(handler, configSecrets(cfg))

	return RuntimeDeps{
		Logger:            slog.New(handler),
		LogLevel:          level,
		OperationTimeout:  operationTimeout,
		RemovalGrace:      removalGrace,
		Retry:             retry,
		WatchHintDebounce: defaultWatchHintDebounce,
		LogCloser:         logCloser,
	}, nil
}

func parseLogLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "trace":
		return LevelTrace, nil
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, errors.New("logging.level must be one of trace, debug, info, warn, error")
	}
}

func parseRetryPolicy(cfg config.RetryConfig) (RetryPolicy, error) {
	initial, err := time.ParseDuration(cfg.InitialInterval)
	if err != nil {
		return RetryPolicy{}, fmt.Errorf("parse retry.initial_interval: %w", err)
	}

	maxInterval, err := time.ParseDuration(cfg.MaxInterval)
	if err != nil {
		return RetryPolicy{}, fmt.Errorf("parse retry.max_interval: %w", err)
	}

	maxElapsed, err := time.ParseDuration(cfg.MaxElapsedTime)
	if err != nil {
		return RetryPolicy{}, fmt.Errorf("parse retry.max_elapsed_time: %w", err)
	}

	return RetryPolicy{
		InitialInterval: initial,
		MaxInterval:     maxInterval,
		MaxElapsedTime:  maxElapsed,

		RandomizationFactor: backoff.DefaultRandomizationFactor,
	}, nil
}

func parseOperationTimeout(cfg config.RuntimeConfig) (time.Duration, error) {
	value := strings.TrimSpace(cfg.OperationTimeout)
	if value == "" {
		return defaultOperationTimeout, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse runtime.operation_timeout: %w", err)
	}

	if duration <= 0 {
		return 0, errors.New("runtime.operation_timeout must be greater than zero")
	}

	return duration, nil
}

func parseRemovalGrace(cfg config.RuntimeConfig) (time.Duration, error) {
	value := strings.TrimSpace(cfg.RemovalGracePeriod)
	if value == "" {
		return defaultRemovalGrace, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("parse runtime.removal_grace_period: %w", err)
	}
	if duration < 0 {
		return 0, errors.New("runtime.removal_grace_period must be zero or greater")
	}
	return duration, nil
}

func withOperationTimeout(parent context.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, timeout)
}

func isContextCancellationError(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)
}

func isTemporaryError(err error) bool {
	if isContextCancellationError(err) {
		return false
	}

	var temporaryErr temporaryError
	return errors.As(err, &temporaryErr) && temporaryErr.Temporary()
}
