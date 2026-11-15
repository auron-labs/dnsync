package pihole

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
)

const testPassword = "correct horse battery staple"

// fakePihole implements the subset of the Pi-hole v6 API the provider uses,
// including session checks and dnsmasq's rejection of duplicate CNAMEs.
type fakePihole struct {
	t        *testing.T
	mu       sync.Mutex
	hosts    []string
	cnames   []string
	sessions map[string]bool
	logins   int
	logouts  int
}

func newFakePihole(t *testing.T, hosts, cnames []string) (*fakePihole, *Provider) {
	t.Helper()

	fake := &fakePihole{t: t, hosts: hosts, cnames: cnames, sessions: map[string]bool{}}
	server := httptest.NewServer(fake)
	t.Cleanup(server.Close)

	return fake, New(config.OutputConfig{Type: "pihole", Name: "primary", URL: server.URL, Password: testPassword})
}

func (f *fakePihole) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	if r.Method == http.MethodPost && r.URL.Path == "/api/auth" {
		var body loginRequest
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Password != testPassword {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"session":{"valid":false,"totp":false,"sid":null,"validity":-1,"message":"password incorrect"}}`))
			return
		}
		f.logins++
		sid := fmt.Sprintf("sid-%d", f.logins)
		f.sessions[sid] = true
		_, _ = fmt.Fprintf(w, `{"session":{"valid":true,"totp":false,"sid":%q,"validity":1800,"message":"password correct"}}`, sid)
		return
	}

	sid := r.Header.Get("X-Ftl-Sid")
	if !f.sessions[sid] {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"key":"unauthorized","message":"Unauthorized"}}`))
		return
	}

	switch {
	case r.Method == http.MethodDelete && r.URL.Path == "/api/auth":
		delete(f.sessions, sid)
		f.logouts++
		w.WriteHeader(http.StatusNoContent)
	case r.Method == http.MethodGet && r.URL.Path == "/api/config/dns":
		var resp dnsConfigResponse
		resp.Config.DNS.Hosts = f.hosts
		resp.Config.DNS.CNAMERecords = f.cnames
		_ = json.NewEncoder(w).Encode(resp)
	case strings.HasPrefix(r.URL.Path, "/api/config/dns/"):
		element, value, _ := strings.Cut(strings.TrimPrefix(r.URL.Path, "/api/config/dns/"), "/")
		items := map[string]*[]string{hostsElement: &f.hosts, cnameElement: &f.cnames}[element]
		if items == nil {
			f.t.Errorf("unexpected config element %q", element)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f.mutate(w, r.Method, element, items, value)
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakePihole) mutate(w http.ResponseWriter, method, element string, items *[]string, value string) {
	index := slices.Index(*items, value)
	switch method {
	case http.MethodPut:
		if index >= 0 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"key":"bad_request","message":"Item already present"}}`))
			return
		}
		if element == cnameElement {
			alias, _, _ := strings.Cut(value, ",")
			for _, existing := range *items {
				if existingAlias, _, _ := strings.Cut(existing, ","); strings.EqualFold(existingAlias, alias) {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"error":{"key":"bad_request","message":"Invalid configuration","hint":"duplicate CNAME"}}`))
					return
				}
			}
		}
		*items = append(*items, value)
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{}`))
	case http.MethodDelete:
		if index < 0 {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{}`))
			return
		}
		*items = slices.Delete(*items, index, index+1)
		w.WriteHeader(http.StatusNoContent)
	default:
		f.t.Errorf("unexpected method %s for %s", method, element)
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (f *fakePihole) expireSessions() {
	f.mu.Lock()
	defer f.mu.Unlock()
	clear(f.sessions)
}

func (f *fakePihole) sessionCounts() (logins, logouts, open int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins, f.logouts, len(f.sessions)
}

func (f *fakePihole) assertEntries(t *testing.T, wantHosts, wantCNAMEs []string) {
	t.Helper()

	f.mu.Lock()
	defer f.mu.Unlock()
	if !slices.Equal(f.hosts, wantHosts) || !slices.Equal(f.cnames, wantCNAMEs) {
		t.Fatalf("unexpected entries\n hosts: %q want %q\ncnames: %q want %q", f.hosts, wantHosts, f.cnames, wantCNAMEs)
	}
}

func TestListVisibleExpandsHostsAndCNAMEEntries(t *testing.T) {
	t.Parallel()

	_, provider := newFakePihole(t,
		[]string{"10.0.0.10 app.home.arpa", "10.0.0.11\tnas.home.arpa NAS # shared", "fd00::1 v6.home.arpa"},
		[]string{"alias.home.arpa,app.home.arpa", "a.home.arpa,b.home.arpa,nas.home.arpa,300"},
	)

	visible, err := provider.ListVisible(context.Background())
	if err != nil {
		t.Fatalf("ListVisible returned error: %v", err)
	}

	got := make([]string, 0, len(visible))
	for _, record := range visible {
		if record.Output != (contracts.ProviderRef{Type: "pihole", Name: "primary"}) || record.Provenance != nil {
			t.Fatalf("unexpected record metadata: %+v", record)
		}
		got = append(got, record.Hostname+"="+record.Answer)
	}
	want := []string{
		"app.home.arpa=10.0.0.10",
		"nas.home.arpa=10.0.0.11",
		"NAS=10.0.0.11",
		"v6.home.arpa=fd00::1",
		"alias.home.arpa=app.home.arpa",
		"a.home.arpa=nas.home.arpa",
		"b.home.arpa=nas.home.arpa",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected visible records\n got: %q\nwant: %q", got, want)
	}
}

func TestCreateWritesHostsAndCNAMEEntries(t *testing.T) {
	t.Parallel()

	fake, provider := newFakePihole(t, []string{"10.0.0.1 manual.home.arpa"}, nil)

	for _, desired := range []contracts.DesiredRecord{
		{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		{Hostname: "v6.home.arpa.", Answer: "fd00::1"},
		{Hostname: "alias.home.arpa", Answer: "app.home.arpa"},
		{Hostname: "MANUAL.home.arpa", Answer: "10.0.0.1"},
	} {
		if _, err := provider.Create(context.Background(), desired); err != nil {
			t.Fatalf("Create(%+v) returned error: %v", desired, err)
		}
	}

	fake.assertEntries(t,
		[]string{"10.0.0.1 manual.home.arpa", "10.0.0.10 app.home.arpa", "fd00::1 v6.home.arpa"},
		[]string{"alias.home.arpa,app.home.arpa"},
	)
}

func TestUpdateReplacesAddressOnSameName(t *testing.T) {
	t.Parallel()

	fake, provider := newFakePihole(t, []string{"10.0.0.10 app.home.arpa", "10.0.0.10 other.home.arpa"}, nil)

	_, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		contracts.DesiredRecord{Hostname: "app.home.arpa", Answer: "10.0.0.11"})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	fake.assertEntries(t, []string{"10.0.0.10 other.home.arpa", "10.0.0.11 app.home.arpa"}, nil)
}

func TestUpdateChangesCNAMETargetAndType(t *testing.T) {
	t.Parallel()

	fake, provider := newFakePihole(t, []string{"10.0.0.10 db.home.arpa"}, []string{"alias.home.arpa,old.home.arpa"})

	if _, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "alias.home.arpa", Answer: "old.home.arpa"},
		contracts.DesiredRecord{Hostname: "alias.home.arpa", Answer: "new.home.arpa"}); err != nil {
		t.Fatalf("Update CNAME target returned error: %v", err)
	}
	if _, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "db.home.arpa", Answer: "10.0.0.10"},
		contracts.DesiredRecord{Hostname: "db.home.arpa", Answer: "new.home.arpa"}); err != nil {
		t.Fatalf("Update A to CNAME returned error: %v", err)
	}

	fake.assertEntries(t, []string{}, []string{"alias.home.arpa,new.home.arpa", "db.home.arpa,new.home.arpa"})
}

func TestUpdateMovesRecordToNewName(t *testing.T) {
	t.Parallel()

	fake, provider := newFakePihole(t, []string{"10.0.0.10 old.home.arpa"}, nil)

	_, err := provider.Update(context.Background(),
		contracts.VisibleRecord{Hostname: "old.home.arpa", Answer: "10.0.0.10"},
		contracts.DesiredRecord{Hostname: "new.home.arpa", Answer: "10.0.0.10"})
	if err != nil {
		t.Fatalf("Update returned error: %v", err)
	}

	fake.assertEntries(t, []string{"10.0.0.10 new.home.arpa"}, nil)
}

func TestDeleteRemovesOnlySingleNameEntries(t *testing.T) {
	t.Parallel()

	fake, provider := newFakePihole(t,
		[]string{"10.0.0.10  APP.home.arpa # dnsync", "10.0.0.11 nas.home.arpa files.home.arpa"},
		[]string{"alias.home.arpa,app.home.arpa,60"},
	)

	for _, visible := range []contracts.VisibleRecord{
		{Hostname: "app.home.arpa", Answer: "10.0.0.10"},
		{Hostname: "alias.home.arpa", Answer: "app.home.arpa"},
		{Hostname: "gone.home.arpa", Answer: "10.0.0.12"},
	} {
		if err := provider.Delete(context.Background(), visible); err != nil {
			t.Fatalf("Delete(%+v) returned error: %v", visible, err)
		}
	}

	err := provider.Delete(context.Background(), contracts.VisibleRecord{Hostname: "nas.home.arpa", Answer: "10.0.0.11"})
	if err == nil || !strings.Contains(err.Error(), "also defines other names") {
		t.Fatalf("expected shared entry to be refused, got %v", err)
	}

	fake.assertEntries(t, []string{"10.0.0.11 nas.home.arpa files.home.arpa"}, []string{})
}

func TestSessionIsReusedRenewedAndClosed(t *testing.T) {
	t.Parallel()

	fake, provider := newFakePihole(t, []string{"10.0.0.10 app.home.arpa"}, nil)
	ctx := context.Background()

	for range 2 {
		if _, err := provider.ListVisible(ctx); err != nil {
			t.Fatalf("ListVisible returned error: %v", err)
		}
	}
	if logins, _, _ := fake.sessionCounts(); logins != 1 {
		t.Fatalf("expected one login for reused session, got %d", logins)
	}

	fake.expireSessions()
	if _, err := provider.Create(ctx, contracts.DesiredRecord{Hostname: "new.home.arpa", Answer: "10.0.0.11"}); err != nil {
		t.Fatalf("Create after session expiry returned error: %v", err)
	}
	if logins, _, _ := fake.sessionCounts(); logins != 2 {
		t.Fatalf("expected re-login after 401, got %d logins", logins)
	}
	fake.assertEntries(t, []string{"10.0.0.10 app.home.arpa", "10.0.0.11 new.home.arpa"}, nil)

	for range 2 {
		if err := provider.Close(ctx); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	}
	if _, logouts, open := fake.sessionCounts(); logouts != 1 || open != 0 {
		t.Fatalf("expected one logout and no open sessions, got %d logouts and %d sessions", logouts, open)
	}
}

func TestCloseIgnoresExpiredSession(t *testing.T) {
	t.Parallel()

	fake, provider := newFakePihole(t, nil, nil)
	if _, err := provider.ListVisible(context.Background()); err != nil {
		t.Fatalf("ListVisible returned error: %v", err)
	}

	fake.expireSessions()
	if err := provider.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error for expired session: %v", err)
	}
}

func TestLoginFailureIsPermanentAndOmitsPassword(t *testing.T) {
	t.Parallel()

	_, provider := newFakePihole(t, nil, nil)
	provider.password = "wrong " + testPassword

	_, err := provider.ListVisible(context.Background())
	if err == nil || !strings.Contains(err.Error(), "password incorrect") {
		t.Fatalf("expected rejected login error, got %v", err)
	}
	if strings.Contains(err.Error(), testPassword) {
		t.Fatalf("login error leaked the password: %v", err)
	}
	var temporary interface{ Temporary() bool }
	if errors.As(err, &temporary) && temporary.Temporary() {
		t.Fatalf("expected rejected login to be permanent, got %v", err)
	}
}

func TestLoginNotFoundReportsV6Requirement(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(server.Close)

	provider := New(config.OutputConfig{Type: "pihole", Name: "primary", URL: server.URL, Password: testPassword})
	if _, err := provider.ListVisible(context.Background()); err == nil || !strings.Contains(err.Error(), "Pi-hole v6 API") {
		t.Fatalf("expected v6 requirement error, got %v", err)
	}
}

func TestRequestFailuresClassifyTemporaryStatuses(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name          string
		authStatus    int
		configStatus  int
		wantTemporary bool
	}{
		{name: "config bad request", authStatus: http.StatusOK, configStatus: http.StatusBadRequest},
		{name: "config forbidden", authStatus: http.StatusOK, configStatus: http.StatusForbidden},
		{name: "config unavailable", authStatus: http.StatusOK, configStatus: http.StatusServiceUnavailable, wantTemporary: true},
		{name: "api seats exceeded", authStatus: http.StatusTooManyRequests, wantTemporary: true},
		{name: "login server error", authStatus: http.StatusInternalServerError, wantTemporary: true},
	} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/api/auth" {
				w.WriteHeader(tc.authStatus)
				if tc.authStatus == http.StatusOK {
					_, _ = w.Write([]byte(`{"session":{"valid":true,"sid":"sid-1","message":"password correct"}}`))
					return
				}
			} else {
				w.WriteHeader(tc.configStatus)
			}
			_, _ = w.Write([]byte(`{"error":{"key":"nope","message":"nope"}}`))
		}))

		provider := New(config.OutputConfig{Type: "pihole", Name: "primary", URL: server.URL, Password: testPassword})
		_, err := provider.ListVisible(context.Background())
		server.Close()

		if err == nil || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("%s: expected error carrying response detail, got %v", tc.name, err)
		}
		var temporary interface{ Temporary() bool }
		if got := errors.As(err, &temporary) && temporary.Temporary(); got != tc.wantTemporary {
			t.Fatalf("%s: temporary = %t, want %t", tc.name, got, tc.wantTemporary)
		}
	}
}
