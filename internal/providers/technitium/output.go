package technitium

import (
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

// Persisted in record comments; changing it removes the ownership evidence from
// existing records.
const ownershipCommentPrefix = "dnsync:managed-by="

// Provider manages A, AAAA, and CNAME records in one Technitium DNS Server
// authoritative zone through /api/zones/records/*.
//
// Records have no stable ID, so provenance identifies a record by its type,
// name, and value, and takes the installation from the ownership comment
// stamped on created records. Updates and deletes also refuse records without
// this installation's comment, so a manual record that replaced a
// daemon-created one is left alone.
type Provider struct {
	ref            contracts.ProviderRef
	baseURL        string
	zone           string
	token          string
	installationID string
	client         *http.Client
}

func New(cfg config.OutputConfig, installationID string) (*Provider, error) {
	installationID = strings.TrimSpace(installationID)
	if installationID == "" {
		return nil, errors.New("technitium installation ID is required")
	}

	return &Provider{
		ref:            contracts.ProviderRef{Type: cfg.Type, Name: cfg.Name},
		baseURL:        cfg.URL,
		zone:           trimName(cfg.Zone),
		token:          cfg.APIToken,
		installationID: installationID,
		client:         http.DefaultClient,
	}, nil
}

func (p *Provider) Provider() contracts.ProviderRef {
	return p.ref
}

func (p *Provider) ListVisible(ctx context.Context) ([]contracts.VisibleRecord, error) {
	records, err := p.getRecords(ctx, p.zone, true)
	if err != nil {
		return nil, err
	}

	var visible []contracts.VisibleRecord
	for _, record := range records {
		answer, ok := record.answer()
		if !ok {
			continue
		}
		visible = append(visible, contracts.VisibleRecord{
			Output:   p.ref,
			Hostname: strings.TrimSpace(record.Name),
			Answer:   answer,
			Provenance: &contracts.RecordProvenance{
				RemoteID:       recordID(record.Name, answer),
				InstallationID: installationIDFromComment(record.Comments),
			},
		})
	}

	return visible, nil
}

func (p *Provider) Create(ctx context.Context, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	form := recordForm(desired.Hostname, desired.Answer)
	form.Set("zone", p.zone)
	form.Set("comments", ownershipCommentPrefix+p.installationID)
	if err := p.call(ctx, "/api/zones/records/add", form, nil); err != nil {
		return nil, err
	}
	return p.provenance(desired), nil
}

func (p *Provider) Update(ctx context.Context, visible contracts.VisibleRecord, desired contracts.DesiredRecord) (*contracts.RecordProvenance, error) {
	current, err := p.ownedRecord(ctx, visible)
	if err != nil {
		return nil, err
	}

	next := recordForm(desired.Hostname, desired.Answer)
	if !strings.EqualFold(current.Type, next.Get("type")) {
		// Technitium cannot change a record's type in place, and a CNAME cannot
		// share its name with the old record. A failed create leaves the name
		// empty, which the next reconcile recreates from daemon state.
		if err := p.deleteRecord(ctx, current); err != nil {
			return nil, err
		}
		return p.Create(ctx, desired)
	}

	form := current.identity(p.zone)
	form.Set("newDomain", next.Get("domain"))
	if current.isAddress() {
		form.Set("newIpAddress", next.Get("ipAddress"))
	} else {
		form.Set("cname", next.Get("cname"))
	}
	// Update resets every attribute it is not given, so carry the current ones over.
	form.Set("ttl", strconv.FormatUint(uint64(current.TTL), 10))
	form.Set("disable", strconv.FormatBool(current.Disabled))
	form.Set("comments", current.Comments)
	form.Set("expiryTtl", strconv.FormatUint(uint64(current.ExpiryTTL), 10))

	if err := p.call(ctx, "/api/zones/records/update", form, nil); err != nil {
		return nil, err
	}
	return p.provenance(desired), nil
}

func (p *Provider) Delete(ctx context.Context, visible contracts.VisibleRecord) error {
	current, err := p.ownedRecord(ctx, visible)
	if err != nil {
		return err
	}

	return p.deleteRecord(ctx, current)
}

type record struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	TTL       uint32 `json:"ttl"`
	Disabled  bool   `json:"disabled"`
	Comments  string `json:"comments"`
	ExpiryTTL uint32 `json:"expiryTtl"`
	RData     struct {
		IPAddress string `json:"ipAddress"`
		CNAME     string `json:"cname"`
	} `json:"rData"`
}

type recordsResponse struct {
	Zone struct {
		Name string `json:"name"`
	} `json:"zone"`
	Records []record `json:"records"`
}

func (r record) isAddress() bool {
	return strings.EqualFold(r.Type, "A") || strings.EqualFold(r.Type, "AAAA")
}

// answer returns the record's target for the record types dnsync manages.
func (r record) answer() (string, bool) {
	switch {
	case r.isAddress():
		return strings.TrimSpace(r.RData.IPAddress), true
	case strings.EqualFold(r.Type, "CNAME"):
		return strings.TrimSpace(r.RData.CNAME), true
	}
	return "", false
}

// identity returns the parameters that select this record in update and delete calls.
func (r record) identity(zone string) url.Values {
	form := url.Values{"zone": {zone}, "domain": {r.Name}, "type": {r.Type}}
	if r.isAddress() {
		form.Set("ipAddress", r.RData.IPAddress)
	}
	return form
}

func recordForm(hostname, answer string) url.Values {
	answer = strings.TrimSpace(answer)
	form := url.Values{"domain": {trimName(hostname)}}
	addr, err := netip.ParseAddr(answer)
	switch {
	case err != nil:
		form.Set("type", "CNAME")
		form.Set("cname", trimName(answer))
	case addr.Is4():
		form.Set("type", "A")
		form.Set("ipAddress", answer)
	default:
		form.Set("type", "AAAA")
		form.Set("ipAddress", answer)
	}
	return form
}

// recordID identifies a record by its type, name, and value, the same fields
// Technitium uses to select records. It changes whenever the record does.
// Persisted in daemon state; changing the format orphans existing ownership.
func recordID(hostname, answer string) string {
	form := recordForm(hostname, answer)
	value := form.Get("cname")
	if addr, err := netip.ParseAddr(form.Get("ipAddress")); err == nil {
		value = addr.String()
	}
	return strings.ToLower(form.Get("type") + " " + form.Get("domain") + " " + value)
}

func (p *Provider) provenance(desired contracts.DesiredRecord) *contracts.RecordProvenance {
	return &contracts.RecordProvenance{RemoteID: recordID(desired.Hostname, desired.Answer), InstallationID: p.installationID}
}

// installationIDFromComment finds the ownership marker anywhere in a record's
// comments, which operators may extend with their own notes.
func installationIDFromComment(comments string) string {
	for _, field := range strings.Fields(comments) {
		if id, ok := strings.CutPrefix(field, ownershipCommentPrefix); ok {
			return id
		}
	}
	return ""
}

func trimName(name string) string {
	return strings.TrimSuffix(strings.TrimSpace(name), ".")
}

func sameDNSName(a, b string) bool {
	return strings.EqualFold(trimName(a), trimName(b))
}

// getRecords lists records at domain, or the whole zone when listZone is set.
func (p *Provider) getRecords(ctx context.Context, domain string, listZone bool) ([]record, error) {
	form := url.Values{"zone": {p.zone}, "domain": {domain}}
	if listZone {
		form.Set("listZone", "true")
	}

	var resp recordsResponse
	if err := p.call(ctx, "/api/zones/records/get", form, &resp); err != nil {
		return nil, err
	}
	// Technitium falls back to the closest parent zone when the named zone does
	// not exist, so a mismatch means the configured zone is missing.
	if !sameDNSName(resp.Zone.Name, p.zone) {
		return nil, fmt.Errorf("technitium zone %q does not exist (closest authoritative zone is %q)", p.zone, resp.Zone.Name)
	}

	return resp.Records, nil
}

// ownedRecord returns the record behind visible after checking it carries this
// installation's ownership comment.
func (p *Provider) ownedRecord(ctx context.Context, visible contracts.VisibleRecord) (record, error) {
	records, err := p.getRecords(ctx, visible.Hostname, false)
	if err != nil {
		return record{}, err
	}

	for _, candidate := range records {
		answer, ok := candidate.answer()
		if !ok || !sameDNSName(candidate.Name, visible.Hostname) || !sameDNSName(answer, visible.Answer) {
			continue
		}
		if !strings.EqualFold(installationIDFromComment(candidate.Comments), p.installationID) {
			return record{}, fmt.Errorf("technitium %s record %s -> %s has no %q comment; leaving it unchanged", candidate.Type, candidate.Name, answer, ownershipCommentPrefix+p.installationID)
		}
		return candidate, nil
	}

	return record{}, fmt.Errorf("technitium record %s -> %s not found in zone %s", visible.Hostname, visible.Answer, p.zone)
}

func (p *Provider) deleteRecord(ctx context.Context, current record) error {
	return p.call(ctx, "/api/zones/records/delete", current.identity(p.zone), nil)
}

type apiResponse struct {
	Status       string          `json:"status"`
	ErrorMessage string          `json:"errorMessage"`
	Response     json.RawMessage `json:"response"`
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

// call posts form parameters so record data and the token stay out of request
// URLs, and decodes the "response" object of a successful call into out.
func (p *Provider) call(ctx context.Context, path string, form url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(p.baseURL, "/")+path, strings.NewReader(form.Encode()))
	if err != nil {
		return fmt.Errorf("build technitium request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+p.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		requestErr := fmt.Errorf("technitium request %s failed: %w", path, err)
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return requestErr
		}
		return temporaryError{err: requestErr}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		requestErr := fmt.Errorf("technitium request %s failed with status %d: %s", path, resp.StatusCode, strings.TrimSpace(string(payload)))
		if isTemporaryStatus(resp.StatusCode) {
			return temporaryError{err: requestErr}
		}
		return requestErr
	}

	// Technitium reports API failures, including invalid tokens, as HTTP 200
	// with a non-"ok" status.
	var body apiResponse
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return fmt.Errorf("decode technitium response: %w", err)
	}
	if body.Status != "ok" {
		return fmt.Errorf("technitium request %s failed with status %q: %s", path, body.Status, body.ErrorMessage)
	}

	if out != nil {
		if err := json.Unmarshal(body.Response, out); err != nil {
			return fmt.Errorf("decode technitium response: %w", err)
		}
	}

	return nil
}

func isTemporaryStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests || statusCode >= http.StatusInternalServerError
}
