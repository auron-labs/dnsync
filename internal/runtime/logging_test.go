package runtime

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestRotatingFileRotatesAppendsAndUsesPrivatePermissions(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "logs", "daemon.log")
	writer, err := newRotatingFile(path, 8, 2)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"1234\n", "5678\n", "abcd\n", "efgh\n", "ijkl\n"} {
		if _, err := writer.Write([]byte(line)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("log permissions: info=%v err=%v", info, err)
	}
	if _, err := os.Stat(path + ".1"); err != nil {
		t.Fatalf("missing first backup: %v", err)
	}
	if _, err := os.Stat(path + ".2"); err != nil {
		t.Fatalf("missing second backup: %v", err)
	}
	if _, err := os.Stat(path + ".3"); !os.IsNotExist(err) {
		t.Fatalf("backup limit not enforced: %v", err)
	}
	writer, err = newRotatingFile(path, 1024, 2)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("restart\n")); err != nil {
		t.Fatal(err)
	}
	_ = writer.Close()
	contents, err := os.ReadFile(path)
	if err != nil || !strings.Contains(string(contents), "restart") {
		t.Fatalf("restart did not append: %q err=%v", contents, err)
	}
}

func TestRotatingFileConcurrentWrites(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "daemon.log")
	writer, err := newRotatingFile(path, 1<<20, 2)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 32 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				if _, err := writer.Write([]byte("record\n")); err != nil {
					t.Errorf("write: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(path)
	if err != nil || strings.Count(string(contents), "record\n") != 640 {
		t.Fatalf("lost concurrent records: count=%d err=%v", strings.Count(string(contents), "record\n"), err)
	}
}

func TestRuntimeLoggingWritesEquivalentRecordsToStdoutAndConfiguredFile(t *testing.T) {
	// This deliberately avoids process-global os.Stdout and environment state.
	path := filepath.Join(t.TempDir(), "daemon.log")
	cfg := testRuntimeConfig(t.TempDir())
	cfg.Logging.Format = "json"
	var stdout bytes.Buffer
	deps, err := newRuntimeDeps(cfg, func(key string) string {
		if key == "LOG_FILE" {
			return path
		}
		return ""
	}, &stdout)
	if err != nil {
		t.Fatal(err)
	}
	deps.Logger.Info("dual sink", "request_token", "do-not-log")
	if err := deps.LogCloser.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if stdout.String() != string(file) {
		t.Fatalf("stdout and file records differ:\nstdout=%q\nfile=%q", stdout.String(), file)
	}
	if strings.Contains(stdout.String(), "do-not-log") {
		t.Fatalf("secret was not redacted: %s", stdout.String())
	}
}
