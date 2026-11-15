package telltaledns

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
	"strings"

	"github.com/auron-labs/dnsync/internal/config"
	"github.com/auron-labs/dnsync/internal/contracts"
)

// Provider manages TelltaleDNS local names through /api/v1/records. The API
// replaces every API-defined record of a name on PUT and exposes no per-record
// identity, so each mutation reads the name's current records and writes back
// the full set, leaving record types dnsync does not manage untouched.
type Provider struct {
	ref     contracts.ProviderRef
	baseURL string
	apiKey  string
	client  *http.Client
}

func New(cfg config.OutputConfig) *Provider {
	return &Provider{
		ref:     contracts.ProviderRef{Type: cfg.Type, Name: cfg.Name},
		baseURL: cfg.URL,
		apiKey:  cfg.APIKey,
		client:  http.DefaultClient,
	}
}

func (p *Provider) Provider() contracts.ProviderRef {
	return p.ref
}

func (p *Provider) ListVisible(ctx context.Context) ([]contracts.VisibleRecord, error) {
	names, err := p.listNames(ctx)
	if err != nil {
		return nil, err
	}

	var visible []contracts.VisibleRecord
	for _, name := range names {
		for _, record := range name.Records {
			if !isManagedType(record.Type) {
				continue
			}
			visible = append(visible, contracts.VisibleRecord{
				Output:   p.ref,
				Hostname: strings.TrimSpace(name.Name),
				Answer:   strings.TrimSpace(record.Value),
			})
		}
	}

	return visible, nil
}

func (p *Provider) Create(ctx context.Context, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	return nil, p.editName(ctx, desired.Hostname, func(records []record) []record {
		return addRecord(records, newRecord(desired.Answer))
	})
}

func (p *Provider) Update(ctx context.Context, visible contracts.VisibleRecord, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	if sameDNSName(visible.Hostname, desired.Hostname) {
		return nil, p.editName(ctx, desired.Hostname, func(records []record) []record {
			return addRecord(removeRecord(records, visible.Answer), newRecord(desired.Answer))
		})
	}

	// Add before removing so a failure leaves a duplicate rather than a gap.
	if err := p.editName(ctx, desired.Hostname, func(records []record) []record {
		return addRecord(records, newRecord(desired.Answer))
	}); err != nil {
		return nil, err
	}

	return nil, p.Delete(ctx, visible)
}

func (p *Provider) Delete(ctx context.Context, visible contracts.VisibleRecord) error {
	return p.editName(ctx, visible.Hostname, func(records []record) []record {
		return removeRecord(records, visible.Answer)
	})
}

type record struct {
	Type  string `json:"type"`
	Value string `json:"value"`
	TTL   *int32 `json:"ttl,omitempty"`
}

type localName struct {
	Name    string   `json:"name"`
	Records []record `json:"records"`
}

type listResponse struct {
	Items []localName `json:"items"`
}

type putRequest struct {
	Records []record `json:"records"`
}

func isManagedType(recordType string) bool {
	switch strings.ToUpper(recordType) {
	case "A", "AAAA", "CNAME":
		return true
	}
	return false
}

func newRecord(answer string) record {
	answer = strings.TrimSpace(answer)
	addr, err := netip.ParseAddr(answer)
	switch {
	case err != nil:
		return record{Type: "CNAME", Value: answer}
	case addr.Is4():
		return record{Type: "A", Value: answer}
	default:
		return record{Type: "AAAA", Value: answer}
	}
}

// sameDNSName compares hostnames and CNAME targets, ignoring case and a trailing dot.
func sameDNSName(a, b string) bool {
	return strings.EqualFold(strings.TrimSuffix(strings.TrimSpace(a), "."), strings.TrimSuffix(strings.TrimSpace(b), "."))
}

func addRecord(records []record, add record) []record {
	for _, existing := range records {
		if strings.EqualFold(existing.Type, add.Type) && sameDNSName(existing.Value, add.Value) {
			return records
		}
	}
	return append(records, add)
}

func removeRecord(records []record, answer string) []record {
	kept := make([]record, 0, len(records))
	for _, existing := range records {
		if isManagedType(existing.Type) && sameDNSName(existing.Value, answer) {
			continue
		}
		kept = append(kept, existing)
	}
	return kept
}

func (p *Provider) listNames(ctx context.Context) ([]localName, error) {
	var resp listResponse
	if err := p.requestJSON(ctx, http.MethodGet, "/api/v1/records", nil, &resp); err != nil {
		return nil, err
	}
	return resp.Items, nil
}

func (p *Provider) editName(ctx context.Context, hostname string, mutate func([]record) []record) error {
	names, err := p.listNames(ctx)
	if err != nil {
		return err
	}

	var current []record
	exists := false
	for _, name := range names {
		if sameDNSName(name.Name, hostname) {
			current, exists = name.Records, true
			break
		}
	}

	next := mutate(append([]record(nil), current...))
	path := "/api/v1/records/" + url.PathEscape(strings.TrimSuffix(strings.TrimSpace(hostname), "."))
	if len(next) == 0 {
		if !exists {
			return nil
		}
		return p.requestJSON(ctx, http.MethodDelete, path, nil, nil)
	}

	return p.requestJSON(ctx, http.MethodPut, path, putRequest{Records: next}, nil)
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

func (p *Provider) requestJSON(ctx context.Context, method, path string, reqBody any, out any) error {
	if strings.TrimSpace(p.baseURL) == "" {
		return errors.New("telltaledns base URL is required")
	}

	var body io.Reader
	if reqBody != nil {
		payload, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("encode telltaledns request: %w", err)
		}
		body = bytes.NewReader(payload)
	}

	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(p.baseURL, "/")+path, body)
	if err != nil {
		return fmt.Errorf("build telltaledns request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.apiKey)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := p.client.Do(req)
	if err != nil {
		requestErr := fmt.Errorf("telltaledns request %s %s failed: %w", method, path, err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return requestErr
		}
		return temporaryError{err: requestErr}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		requestErr := fmt.Errorf("telltaledns request %s %s failed with status %d: %s", method, path, resp.StatusCode, strings.TrimSpace(string(payload)))
		if isTemporaryStatus(resp.StatusCode) {
			return temporaryError{err: requestErr}
		}
		return requestErr
	}

	if out != nil {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("decode telltaledns response: %w", err)
		}
	}

	return nil
}

func isTemporaryStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}
