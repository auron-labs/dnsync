package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/getsentry/sentry-go"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/runtime"
)

type appRunner interface {
	Run(context.Context) error
}

var newApp = func(cfg config.Config) appRunner {
	return runtime.New(cfg)
}

// version is set by GoReleaser's default ldflags.
var version string

const sentryFlushTimeout = 2 * time.Second

func main() {
	os.Exit(runMain())
}

func runMain() int {
	// With SENTRY_DSN unset the SDK is a no-op. DSN and environment come from
	// SENTRY_DSN and SENTRY_ENVIRONMENT; PII collection is off by default.
	if err := sentry.Init(sentry.ClientOptions{Release: version}); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "warning: sentry disabled: %v\n", err)
	}
	defer sentry.Flush(sentryFlushTimeout)
	defer func() {
		if r := recover(); r != nil {
			sentry.CurrentHub().Recover(r)
			sentry.Flush(sentryFlushTimeout)
			panic(r)
		}
	}()

	if err := run(os.Args[1:]); err != nil {
		sentry.CaptureException(err)
		_, _ = fmt.Fprintf(os.Stderr, "error: %v\n", err)
		return 1
	}
	return 0
}

func run(args []string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return runWithContext(ctx, args)
}

func runWithContext(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("dnsync", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	configPath := fs.String("config", "", "Path to the TOML config file")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	if *configPath == "" {
		return errors.New("-config is required")
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	app := newApp(cfg)
	if err := app.Run(ctx); err != nil {
		return fmt.Errorf("run daemon: %w", err)
	}
	return nil
}
