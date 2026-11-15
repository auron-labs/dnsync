package technitium

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
)

const (
	testInstallationID = "123e4567-e89b-42d3-a456-426614174000"
	testMarker         = ownershipCommentPrefix + testInstallationID
)

// fakeServer implements the subset of /api/zones/records/* the provider uses
// for one zone. Requests naming another zone resolve to parentZone, mirroring
// Technitium's closest-zone fallback.
type fakeServer struct {
	mu         sync.Mutex
	zone       string
	parentZone string
	records    []record
}

func newFakeServer(t *testing.T, records ...record) (*fakeServer, *Provider) {
	t.Helper()

	fake := &fakeServer{zone: "home.arpa", parentZone: "arpa", records: records}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fake.mu.Lock()
		defer fake.mu.Unlock()

		if got := r.Header.Get("Authorization"); got != "Bearer technitium-token" {
			t.Errorf("unexpected Authorization header %q", got)
		}
		if r.Method != http.MethodPost || r.URL.RawQuery != "" {
			t.Errorf("expected form POST without query, got %s %s", r.Method, r.URL)
		}
		if err := r.ParseForm(); err != nil {
			t.Errorf("parse form: %v", err)
		}

		response, err := fake.handle(r.URL.Path, r.PostForm)
		w.Header().Set("Content-Type", "application/json")
		if err != nil {
			_ = json.NewEncoder(w).Encode(map[string]string{"status": "error", "errorMessage": err.Error()})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok", "response": response})
	}))
	t.Cleanup(server.Close)

	provider, err := New(config.OutputConfig{Type: "technitium", Name: "primary", URL: server.URL, Zone: "home.arpa.", APIToken: "technitium-token"}, testInstallationID)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return fake, provider
}

func (f *fakeServer) handle(path string, form map[string][]string) (any, error) {
	get := func(key string) string {
		if values := form[key]; len(values) > 0 {
			return values[0]
		}
		return ""
	}
	zone := f.zone
	if get("zone") != f.zone {
		zone = f.parentZone
	}

	switch path {
	case "/api/zones/records/get":
		matched := []record{}
		for _, existing := range f.records {
			if get("listZone") == "true" || existing.Name == get("domain") {
				matched = append(matched, existing)
			}
		}
		return map[string]any{"zone": map[string]string{"name": zone}, "records": matched}, nil
	case "/api/zones/records/add":
		added := record{Name: get("domain"), Type: get("type"), TTL: 3600, Comments: get("comments")}
		added.RData.IPAddress, added.RData.CNAME = get("ipAddress"), get("cname")
		if _, ok := f.find(added.Name, added.Type, added.RData.IPAddress); ok {
			return nil, errors.New("record already exists")
		}
		f.records = append(f.records, added)
		return map[string]any{}, nil
	case "/api/zones/records/update":
		i, ok := f.find(get("domain"), get("type"), get("ipAddress"))
		if !ok {
			return nil, errors.New("old record does not exist")
		}
		updated := &f.records[i]
		ttl, _ := strconv.ParseUint(get("ttl"), 10, 32)
		expiry, _ := strconv.ParseUint(get("expiryTtl"), 10, 32)
		updated.Name, updated.TTL, updated.ExpiryTTL = get("newDomain"), uint32(ttl), uint32(expiry)
		updated.Disabled, updated.Comments = get("disable") == "true", get("comments")
		if updated.isAddress() {
			updated.RData.IPAddress = get("newIpAddress")
		} else {
			updated.RData.CNAME = get("cname")
		}
		return map[string]any{}, nil
	case "/api/zones/records/delete":
		i, ok := f.find(get("domain"), get("type"), get("ipAddress"))
		if !ok {
			return nil, errors.New("no such record exists")
		}
		f.records = append(f.records[:i], f.records[i+1:]...)
		return map[string]any{}, nil
	}
	return nil, errors.New("unexpected path " + path)
}

func (f *fakeServer) find(name, recordType, ipAddress string) (int, bool) {
	for i, existing := range f.records {
		if existing.Name == name && existing.Type == recordType && existing.RData.IPAddress == ipAddress {
			return i, true
		}
	}
	return 0, false
}

func newRecord(name, recordType, value, comments string) record {
	r := record{Name: name, Type: recordType, TTL: 3600, Comments: comments}
	if recordType == "CNAME" {
		r.RData.CNAME = value
	} else {
		r.RData.IPAddress = value
	}
	return r
}

func TestListVisibleReturnsManagedRecordTypesInZone(t *testing.T) {
	t.Parallel()

	_, provider := newFakeServer(t,
		record{Name: "home.arpa", Type: "SOA"},
		newRecord("app.home.arpa", "A", "10.0.0.10", ""),
		newRecord("app.home.arpa", "TXT", "", ""),
		newRecord("v6.home.arpa", "AAAA", "fd00::1", testMarker),
		newRecord("alias.home.arpa", "CNAME", "app.home.arpa", testMarker),
	)

	visible, err := provider.ListVisible(context.Background())
	if err != nil {
		t.Fatalf("ListVisible returned error: %v", err)
	}

	want := []contracts.VisibleRecord{
		{Output: provider.Provider(), Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		{Output: provider.Provider(), Hostname: "v6.home.arpa", Answer: "fd00::1"},
		{Output: provider.Provider(), Hostname: "alias.home.arpa", Answer: "app.home.arpa"},
	}
	if len(visible) != len(want) {
		t.Fatalf("unexpected visible records: %+v", visible)
	}
	for i := range want {
		if visible[i] != want[i] {
			t.Fatalf("visible[%d] = %+v, want %+v", i, visible[i], want[i])
		}
	}
}

func TestListVisibleRejectsMissingZone(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t)
	fake.zone = "other.arpa"

	_, err := provider.ListVisible(context.Background())
	if err == nil || !strings.Contains(err.Error(), `technitium zone "home.arpa" does not exist`) {
		t.Fatalf("expected missing zone error, got %v", err)
	}
}

func TestCreateStampsOwnershipComment(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t)

	for _, desired := range []contracts.DesiredRecord{
		{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		{Hostname: "v6.home.arpa", Answer: "fd00::1"},
		{Hostname: "alias.home.arpa.", Answer: "app.home.arpa."},
	} {
		provenance, err := provider.Create(context.Background(), desired)
		if err != nil {
			t.Fatalf("Create(%+v) returned error: %v", desired, err)
		}
		if provenance != nil {
			t.Fatalf("expected no provenance, got %+v", provenance)
		}
	}

	assertRecords(t, fake,
		newRecord("app.home.arpa", "A", "10.0.0.10", testMarker),
		newRecord("v6.home.arpa", "AAAA", "fd00::1", testMarker),
		newRecord("alias.home.arpa", "CNAME", "app.home.arpa", testMarker),
	)
}

func TestUpdateKeepsRecordAttributes(t *testing.T) {
	t.Parallel()

	tuned := newRecord("app.home.arpa", "A", "10.0.0.10", testMarker+" media server")
	tuned.TTL, tuned.Disabled, tuned.ExpiryTTL = 60, true, 86400
	fake, provider := newFakeServer(t, tuned, newRecord("alias.home.arpa", "CNAME", "app.home.arpa", testMarker))

	if _, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		contracts.DesiredRecord{Hostname: "renamed.home.arpa", Answer: "10.0.0.11"}); err != nil {
		t.Fatalf("Update A returned error: %v", err)
	}
	if _, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "alias.home.arpa", Answer: "app.home.arpa"},
		contracts.DesiredRecord{Hostname: "alias.home.arpa", Answer: "renamed.home.arpa"}); err != nil {
		t.Fatalf("Update CNAME returned error: %v", err)
	}

	want := newRecord("renamed.home.arpa", "A", "10.0.0.11", testMarker+" media server")
	want.TTL, want.Disabled, want.ExpiryTTL = 60, true, 86400
	assertRecords(t, fake, want, newRecord("alias.home.arpa", "CNAME", "renamed.home.arpa", testMarker))
}

func TestUpdateReplacesRecordWhenTypeChanges(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t,
		newRecord("app.home.arpa", "A", "10.0.0.10", testMarker),
		newRecord("app.home.arpa", "TXT", "", ""),
	)

	if _, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		contracts.DesiredRecord{Hostname: "app.home.arpa", Answer: "fd00::10"}); err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	assertRecords(t, fake,
		newRecord("app.home.arpa", "TXT", "", ""),
		newRecord("app.home.arpa", "AAAA", "fd00::10", testMarker),
	)
}

func TestUpdateAndDeletePreserveRecordsWithoutOwnershipComment(t *testing.T) {
	t.Parallel()

	manual := []record{
		newRecord("manual.home.arpa", "A", "10.0.0.50", ""),
		newRecord("other.home.arpa", "A", "10.0.0.51", ownershipCommentPrefix+"another-installation"),
	}
	fake, provider := newFakeServer(t, manual...)

	for _, existing := range manual {
		visible := contracts.VisibleRecord{Hostname: existing.Name, Answer: existing.RData.IPAddress}
		if _, err := provider.Update(context.Background(), visible, contracts.DesiredRecord{Hostname: existing.Name, Answer: "10.0.0.99"}); err == nil || !strings.Contains(err.Error(), "leaving it unchanged") {
			t.Fatalf("expected Update of %s to be refused, got %v", existing.Name, err)
		}
		if err := provider.Delete(context.Background(), visible); err == nil || !strings.Contains(err.Error(), "leaving it unchanged") {
			t.Fatalf("expected Delete of %s to be refused, got %v", existing.Name, err)
		}
	}

	assertRecords(t, fake, manual...)
}

func TestDeleteRemovesOnlyTheOwnedRecord(t *testing.T) {
	t.Parallel()

	fake, provider := newFakeServer(t,
		newRecord("app.home.arpa", "A", "10.0.0.10", testMarker),
		newRecord("app.home.arpa", "A", "10.0.0.20", ""),
		newRecord("alias.home.arpa", "CNAME", "app.home.arpa", testMarker),
	)

	for _, visible := range []contracts.VisibleRecord{
		{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		{Hostname: "alias.home.arpa", Answer: "app.home.arpa"},
	} {
		if err := provider.Delete(context.Background(), visible); err != nil {
			t.Fatalf("Delete(%+v) returned error: %v", visible, err)
		}
	}

	assertRecords(t, fake, newRecord("app.home.arpa", "A", "10.0.0.20", ""))
}

func TestRequestFailuresClassifyTemporaryStatuses(t *testing.T) {
	t.Parallel()

	for _, tt := range []struct {
		name          string
		status        int
		body          string
		wantMessage   string
		wantTemporary bool
	}{
		{name: "api error", status: http.StatusOK, body: `{"status":"error","errorMessage":"Access was denied."}`, wantMessage: "Access was denied.", wantTemporary: false},
		{name: "invalid token", status: http.StatusOK, body: `{"status":"invalid-token","errorMessage":"Invalid token or session expired."}`, wantMessage: "invalid-token", wantTemporary: false},
		{name: "not found", status: http.StatusNotFound, body: "missing", wantMessage: "status 404", wantTemporary: false},
		{name: "rate limited", status: http.StatusTooManyRequests, body: "slow down", wantMessage: "slow down", wantTemporary: true},
		{name: "unavailable", status: http.StatusServiceUnavailable, body: "restarting", wantMessage: "restarting", wantTemporary: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			t.Cleanup(server.Close)

			provider, err := New(config.OutputConfig{Type: "technitium", Name: "primary", URL: server.URL, Zone: "home.arpa", APIToken: "technitium-token"}, testInstallationID)
			if err != nil {
				t.Fatalf("New returned error: %v", err)
			}

			_, err = provider.ListVisible(context.Background())
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("expected error containing %q, got %v", tt.wantMessage, err)
			}
			if strings.Contains(err.Error(), "technitium-token") {
				t.Fatalf("token leaked into error: %v", err)
			}
			var temporary interface{ Temporary() bool }
			if got := errors.As(err, &temporary) && temporary.Temporary(); got != tt.wantTemporary {
				t.Fatalf("temporary = %t, want %t", got, tt.wantTemporary)
			}
		})
	}
}

func assertRecords(t *testing.T, fake *fakeServer, want ...record) {
	t.Helper()

	fake.mu.Lock()
	defer fake.mu.Unlock()

	got, _ := json.Marshal(fake.records)
	expected, _ := json.Marshal(want)
	if string(got) != string(expected) {
		t.Fatalf("unexpected records\n got: %s\nwant: %s", got, expected)
	}
}
