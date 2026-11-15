package pihole

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"strings"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
)

const (
	authPath      = "/api/auth"
	dnsConfigPath = "/api/config/dns"
	hostsElement  = "hosts"
	cnameElement  = "cnameRecords"
)

// Provider manages Pi-hole v6 local DNS records through the config API. A and
// AAAA records are dns.hosts entries ("IP NAME [NAME ...]") and CNAMEs are
// dns.cnameRecords entries ("NAME[,NAME ...],TARGET[,TTL]"). Entries carry no
// identity and are added or removed by their exact text, so every mutation
// re-reads the current entries first. Entries listing several names are never
// written by dnsync and are left for the operator to edit.
type Provider struct {
	ref      contracts.ProviderRef
	baseURL  string
	password string
	client   *http.Client

	// Pi-hole extends a session on each authenticated request and answers an
	// expired or invalidated one with 401. sid stays empty when the Pi-hole has
	// no password and grants access without a session.
	loggedIn bool
	sid      string
}

func New(cfg config.OutputConfig) *Provider {
	return &Provider{
		ref:      contracts.ProviderRef{Type: cfg.Type, Name: cfg.Name},
		baseURL:  cfg.URL,
		password: cfg.Password,
		client:   http.DefaultClient,
	}
}

func (p *Provider) Provider() contracts.ProviderRef {
	return p.ref
}

func (p *Provider) ListVisible(ctx context.Context) ([]contracts.VisibleRecord, error) {
	entries, err := p.listEntries(ctx)
	if err != nil {
		return nil, err
	}

	var visible []contracts.VisibleRecord
	for _, e := range entries {
		for _, name := range e.names {
			visible = append(visible, contracts.VisibleRecord{Output: p.ref, Hostname: name, Answer: e.answer})
		}
	}

	return visible, nil
}

func (p *Provider) Create(ctx context.Context, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	return nil, p.add(ctx, desired.Hostname, desired.Answer)
}

func (p *Provider) Update(ctx context.Context, visible contracts.VisibleRecord, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	// dnsmasq rejects a second CNAME for a name, so a CNAME on an unchanged name
	// is removed before its replacement is added. Otherwise add first so a
	// failure leaves a duplicate rather than a gap.
	if sameDNSName(visible.Hostname, desired.Hostname) && (isCNAME(visible.Answer) || isCNAME(desired.Answer)) {
		if err := p.remove(ctx, visible.Hostname, visible.Answer); err != nil {
			return nil, err
		}
		return nil, p.add(ctx, desired.Hostname, desired.Answer)
	}

	if err := p.add(ctx, desired.Hostname, desired.Answer); err != nil {
		return nil, err
	}
	return nil, p.remove(ctx, visible.Hostname, visible.Answer)
}

func (p *Provider) Delete(ctx context.Context, visible contracts.VisibleRecord) error {
	return p.remove(ctx, visible.Hostname, visible.Answer)
}

// Close ends the API session so it does not hold one of Pi-hole's limited API
// seats (webserver.api.max_sessions) until it expires.
func (p *Provider) Close(ctx context.Context) error {
	if p.sid == "" {
		return nil
	}

	resp, err := p.send(ctx, http.MethodDelete, authPath, nil)
	p.loggedIn, p.sid = false, ""
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusNotFound {
		return nil
	}
	return checkStatus(http.MethodDelete, authPath, resp)
}

type entry struct {
	element string
	raw     string
	names   []string
	answer  string
}

func (e entry) serves(hostname, answer string) bool {
	if !strings.EqualFold(e.answer, strings.TrimSpace(answer)) {
		return false
	}
	for _, name := range e.names {
		if sameDNSName(name, hostname) {
			return true
		}
	}
	return false
}

func parseHostsEntry(raw string) (entry, bool) {
	fields := strings.Fields(stripComment(raw))
	if len(fields) < 2 {
		return entry{}, false
	}
	return entry{element: hostsElement, raw: raw, answer: fields[0], names: fields[1:]}, true
}

func parseCNAMEEntry(raw string) (entry, bool) {
	fields := strings.Split(stripComment(raw), ",")
	for i := range fields {
		fields[i] = strings.TrimSpace(fields[i])
	}
	if len(fields) > 2 {
		if _, err := strconv.ParseUint(fields[len(fields)-1], 10, 32); err == nil {
			fields = fields[:len(fields)-1]
		}
	}
	if len(fields) < 2 {
		return entry{}, false
	}
	return entry{element: cnameElement, raw: raw, names: fields[:len(fields)-1], answer: fields[len(fields)-1]}, true
}

func stripComment(raw string) string {
	value, _, _ := strings.Cut(raw, "#")
	return value
}

func isCNAME(answer string) bool {
	_, err := netip.ParseAddr(strings.TrimSpace(answer))
	return err != nil
}

// sameDNSName compares hostnames, ignoring case and a trailing dot.
func sameDNSName(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(a), "."), strings.TrimSuffix(strings.TrimSpace(b), "."))
}

func elementPath(element, value string) string {
	return dnsConfigPath + "/" + element + "/" + url.PathEscape(value)
}

type dnsConfigResponse struct {
	Config struct {
		DNS struct {
			Hosts        []string `json:"hosts"`
			CNAMERecords []string `json:"cnameRecords"`
		} `json:"dns"`
	} `json:"config"`
}

func (p *Provider) listEntries(ctx context.Context) ([]entry, error) {
	var resp dnsConfigResponse
	if err := p.requestJSON(ctx, http.MethodGet, dnsConfigPath, &resp); err != nil {
		return nil, err
	}

	entries := make([]entry, 0, len(resp.Config.DNS.Hosts)+len(resp.Config.DNS.CNAMERecords))
	for _, raw := range resp.Config.DNS.Hosts {
		if e, ok := parseHostsEntry(raw); ok {
			entries = append(entries, e)
		}
	}
	for _, raw := range resp.Config.DNS.CNAMERecords {
		if e, ok := parseCNAMEEntry(raw); ok {
			entries = append(entries, e)
		}
	}
	return entries, nil
}

func (p *Provider) add(ctx context.Context, hostname, answer string) error {
	entries, err := p.listEntries(ctx)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.serves(hostname, answer) {
			return nil
		}
	}

	hostname = strings.TrimSuffix(strings.TrimSpace(hostname), ".")
	answer = strings.TrimSpace(answer)
	if isCNAME(answer) {
		return p.requestJSON(ctx, http.MethodPut, elementPath(cnameElement, hostname+","+answer), nil)
	}
	return p.requestJSON(ctx, http.MethodPut, elementPath(hostsElement, answer+" "+hostname), nil)
}

func (p *Provider) remove(ctx context.Context, hostname, answer string) error {
	entries, err := p.listEntries(ctx)
	if err != nil {
		return err
	}

	shared := ""
	for _, e := range entries {
		if !e.serves(hostname, answer) {
			continue
		}
		if len(e.names) == 1 {
			return p.requestJSON(ctx, http.MethodDelete, elementPath(e.element, e.raw), nil)
		}
		shared = e.raw
	}
	if shared != "" {
		return fmt.Errorf("pihole entry %q also defines other names; edit it in Pi-hole to remove %s", shared, hostname)
	}
	return nil
}

type temporaryError struct {
	err error
}

func (e temporaryError) Error() string {
	return e.err.Error()
}

func (e temporaryError) Unwrap() error {
	return e.err
}

func (e temporaryError) Temporary() bool {
	return true
}

type loginRequest struct {
	Password string `json:"password"`
}

type authResponse struct {
	Session struct {
		Valid   bool    `json:"valid"`
		SID     *string `json:"sid"`
		Message string  `json:"message"`
	} `json:"session"`
}

func (p *Provider) login(ctx context.Context) error {
	resp, err := p.send(ctx, http.MethodPost, authPath, loginRequest{Password: p.password})
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode == http.StatusNotFound {
		return fmt.Errorf("pihole login POST %s failed with status 404; dnsync requires the Pi-hole v6 API", authPath)
	}
	if err := checkStatus(http.MethodPost, authPath, resp); err != nil {
		return err
	}

	var auth authResponse
	if err := json.NewDecoder(resp.Body).Decode(&auth); err != nil {
		return fmt.Errorf("decode pihole login response: %w", err)
	}
	if !auth.Session.Valid {
		return fmt.Errorf("pihole login rejected: %s", auth.Session.Message)
	}

	p.loggedIn = true
	if auth.Session.SID != nil {
		p.sid = *auth.Session.SID
	}
	return nil
}

func (p *Provider) requestJSON(ctx context.Context, method, path string, out any) error {
	if !p.loggedIn {
		if err := p.login(ctx); err != nil {
			return err
		}
	}

	resp, err := p.send(ctx, method, path, nil)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		_ = resp.Body.Close()
		p.loggedIn, p.sid = false, ""
		if err := p.login(ctx); err != nil {
			return err
		}
		if resp, err = p.send(ctx, method, path, nil); err != nil {
			return err
		}
	}
	defer func() { _ = resp.Body.Close() }()

	if err := checkStatus(method, path, resp); err != nil {
		return err
	}
	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode pihole response: %w", err)
		}
	}
	return nil
}

// send performs one request with the current session. Errors never include the
// request body or headers, which carry the password and session ID.
func (p *Provider) send(ctx context.Context, method, path string, reqBody any) (*http.Response, error) {
	if strings.TrimSpace(p.baseURL) == "" {
		return nil, errors.New("pihole base URL is required")
	}

	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return nil, fmt.Errorf("encode pihole request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.baseURL, "/")+path, body)
	if err != nil {
		return nil, fmt.Errorf("build pihole request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if p.sid != "" {
		req.Header.Set("X-Ftl-Sid", p.sid)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		requestErr := fmt.Errorf("pihole request %s %s failed: %w", method, path, err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, requestErr
		}
		return nil, temporaryError{err: requestErr}
	}
	return resp, nil
}

func checkStatus(method, path string, resp *http.Response) error {
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		return nil
	}

	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
	requestErr := fmt.Errorf("pihole request %s %s failed with status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(payload)))
	if isTemporaryStatus(resp.StatusCode) {
		return temporaryError{err: requestErr}
	}
	return requestErr
}

func isTemporaryStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}
