package telltaledns

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
)

// fakeServer implements the subset of /api/v1/records the provider uses.
type fakeServer struct {
	mu    sync.Mutex
	names map[string][]record
	calls []string
}

func newFakeServer(t *testing.T, names map[string][]record) (*fakeServer, *Provider) {
	t.Helper()

	fake := &fakeServer{names: names}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()

		if got := r.Header.Get("Authorization"); got != "Bearer tt_id_secret" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		fake.calls = append(fake.calls, r.Method+" "+r.URL.Path)

		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/records":
			items := make([]localName, 0, len(fake.names))
			for name, records := range fake.names {
				items = append(items, localName{Name: name, Records: records})
			}
			_ = json.NewEncoder(w).Encode(listResponse{Items: items})
		case r.Method == http.MethodPut && strings.HasPrefix(r.URL.Path, "/api/v1/records/"):
			var body putRequest
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode put body: %v", err)
			}
			fake.names[strings.TrimPrefix(r.URL.Path, "/api/v1/records/")] = body.Records
			_, _ = w.Write([]byte(`{"applied":true}`))
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/v1/records/"):
			delete(fake.names, strings.TrimPrefix(r.URL.Path, "/api/v1/records/"))
			_, _ = w.Write([]byte(`{"applied":true}`))
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)

	return fake, New(config.OutputConfig{Type: "telltaledns", Name: "primary", URL: server.URL, APIKey: "tt_id_secret"})
}

func TestListVisibleReturnsOnlyManagedRecordTypes(t *testing.T) {
	t.Parallel()

	_, provider := newFakeServer(t, map[string][]record{
		"app.home.arpa":   {{Type: "A", Value: "10.0.0.10"}, {Type: "TXT", Value: "note"}},
		"alias.home.arpa": {{Type: "CNAME", Value: "app.home.arpa"}},
	})

	visible, err := provider.ListVisible(context.Background())
	if err != nil {
		t.Fatalf("ListVisible returned error: %v", err)
	}

	got := map[string]string{}
	for _, record := range visible {
		got[record.Hostname] = record.Answer
		if record.Output != (contracts.ProviderRef{Type: "telltaledns", Name: "primary"}) || record.Provenance != nil {
			t.Fatalf("unexpected record metadata: %+v", record)
		}
	}
	if len(visible) != 2 || got["app.home.arpa"] != "10.0.0.10" || got["alias.home.arpa"] != "app.home.arpa" {
		t.Fatalf("unexpected visible records: %+v", visible)
	}
}

func TestCreatePreservesUnmanagedRecordsOnTheName(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t, map[string][]record{
		"app.home.arpa": {{Type: "TXT", Value: "note"}},
	})

	if _, err := provider.Create(context.Background(), contracts.DesiredRecord{Hostname: "app.home.arpa", Answer: "10.0.0.10"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := provider.Create(context.Background(), contracts.DesiredRecord{Hostname: "v6.home.arpa", Answer: "fd00::1"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err := provider.Create(context.Background(), contracts.DesiredRecord{Hostname: "alias.home.arpa", Answer: "app.home.arpa"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	want := map[string][]record{
		"app.home.arpa":   {{Type: "TXT", Value: "note"}, {Type: "A", Value: "10.0.0.10"}},
		"v6.home.arpa":    {{Type: "AAAA", Value: "fd00::1"}},
		"alias.home.arpa": {{Type: "CNAME", Value: "app.home.arpa"}},
	}
	assertNames(t, fake, want)
}

func TestCreateIsIdempotent(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t, map[string][]record{
		"app.home.arpa": {{Type: "A", Value: "10.0.0.10"}},
	})

	if _, err := provider.Create(context.Background(), contracts.DesiredRecord{Hostname: "app.home.arpa", Answer: "10.0.0.10"}); err != nil {
		t.Fatalf("Create returned error: %v", err)
	}

	assertNames(t, fake, map[string][]record{"app.home.arpa": {{Type: "A", Value: "10.0.0.10"}}})
}

func TestUpdateReplacesAnswerOnSameName(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t, map[string][]record{
		"app.home.arpa": {{Type: "A", Value: "10.0.0.10"}, {Type: "TXT", Value: "note"}},
	})

	_, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		contracts.DesiredRecord{Hostname: "app.home.arpa", Answer: "10.0.0.11"})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	assertNames(t, fake, map[string][]record{"app.home.arpa": {{Type: "TXT", Value: "note"}, {Type: "A", Value: "10.0.0.11"}}})
}

func TestUpdateMovesRecordToNewName(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t, map[string][]record{
		"old.home.arpa": {{Type: "A", Value: "10.0.0.10"}},
	})

	_, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "old.home.arpa", Answer: "10.0.0.10"},
		contracts.DesiredRecord{Hostname: "new.home.arpa", Answer: "10.0.0.10"})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	assertNames(t, fake, map[string][]record{"new.home.arpa": {{Type: "A", Value: "10.0.0.10"}}})
}

func TestDeleteRemovesNameWhenNoRecordsRemain(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t, map[string][]record{
		"gone.home.arpa": {{Type: "A", Value: "10.0.0.10"}},
		"kept.home.arpa": {{Type: "A", Value: "10.0.0.11"}, {Type: "TXT", Value: "note"}},
	})

	for _, visible := range []contracts.VisibleRecord{
		{Hostname: "gone.home.arpa", Answer: "10.0.0.10"},
		{Hostname: "kept.home.arpa", Answer: "10.0.0.11"},
	} {
		if err := provider.Delete(context.Background(), visible); err != nil {
			t.Fatalf("Delete returned error: %v", err)
		}
	}

	assertNames(t, fake, map[string][]record{"kept.home.arpa": {{Type: "TXT", Value: "note"}}})
}

func TestRequestFailuresClassifyTemporaryStatuses(t *testing.T) {
	t.Parallel()

	for status, wantTemporary := range map[int]bool{
		http.StatusConflict:           false,
		http.StatusUnauthorized:       false,
		http.StatusTooManyRequests:    true,
		http.StatusServiceUnavailable: true,
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"detail":"nope"}`))
		}))

		provider := New(config.OutputConfig{Type: "telltaledns", Name: "primary", URL: server.URL, APIKey: "tt_id_secret"})
		_, err := provider.ListVisible(context.Background())
		server.Close()

		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("status %d: expected error carrying response detail, got %v", status, err)
		}
		var temporary interface{ Temporary() bool }
		if got := errors.As(err, &temporary) && temporary.Temporary(); got != wantTemporary {
			t.Fatalf("status %d: temporary = %t, want %t", status, got, wantTemporary)
		}
	}
}

func assertNames(t *testing.T, fake *fakeServer, want map[string][]record) {
	t.Helper()

	fake.mu.Lock()
	defer fake.mu.Unlock()

	got, _ := json.Marshal(fake.names)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("unexpected names\n got: %s\nwant: %s\ncalls: %v", got, expected, fake.calls)
	}
}
