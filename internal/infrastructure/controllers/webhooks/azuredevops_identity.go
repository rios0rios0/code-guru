package webhooks

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/rios0rios0/codeguru/internal/support"
)

// ADOIdentityResolver resolves the Azure DevOps identity id that the
// bot's own PAT authenticates as — the GUID Azure DevOps writes into a
// comment when a user picks the bot out of the comment box's
// @-autocomplete (`@<identity-guid>`).
//
// It exists so the mention gate works with NO configuration: without
// it, the only way an autocompleted mention could ever be recognised
// was for an operator to look their service account's GUID up by hand
// and paste it into `bot_identities` — and until they did, the most
// natural way to summon the bot on Azure DevOps failed silently.
//
// The interface is small on purpose (same rationale as
// ADOResourceHydrator): "given an org + token, who am I?" is all the
// handler needs, and everything else — HTTP client, caching — is an
// implementation detail tests never have to reproduce.
type ADOIdentityResolver interface {
	ResolveSelfID(ctx context.Context, organization, token string) (string, error)
}

// adoIdentityAPIBaseURL is the constant host every identity lookup is
// issued against. Pinning it here (rather than deriving it from the
// webhook payload's `remoteUrl`, the way hydration must) is the SSRF
// defence for this path: only the organisation slug comes from the
// delivery, and it lands in a single path segment that
// `url.PathEscape` cannot let escape the host. `dev.azure.com/{org}`
// is the canonical form for every organisation, including ones whose
// clone URLs still carry the legacy `{org}.visualstudio.com` host, so
// no second shape is needed — and if a lookup ever did fail here, the
// caller degrades to the configured mention forms rather than erroring.
const adoIdentityAPIBaseURL = "https://dev.azure.com"

// adoConnectionDataAPIVersion pins the api-version of the
// `_apis/connectionData` call. It is deliberately NOT the hydrator's
// `adoAPIVersion` constant: the two endpoints version independently,
// and `connectionData` answers `7.0` with an HTTP 400 (verified
// against a live organisation), so a well-meaning "let's use the
// stable version" edit would break the lookup on every deployment.
const adoConnectionDataAPIVersion = "7.1-preview.1"

// adoIdentityTimeout caps the per-request wall time of the identity
// lookup. It runs on the webhook handler path, before anything is
// enqueued, so it must fail fast rather than hold the delivery open:
// ADO retries the webhook, and a mention that resolves slowly is worse
// than one that resolves on the retry. Same budget as the hydrator's
// GET against the same service.
const adoIdentityTimeout = 10 * time.Second

// adoIdentityResponseLimit bounds how much of the connectionData
// response is read. The payload carries a `locationServiceData` block
// whose size is set by the service, not by us; 4 MiB is far above any
// observed response while still keeping a misbehaving endpoint from
// growing the handler's heap without limit.
const adoIdentityResponseLimit = 4 * 1024 * 1024

// adoConnectionData is the minimal subset of the `_apis/connectionData`
// response the resolver needs. Every other field (`authorizedUser`,
// `locationServiceData`, deployment ids) is ignored.
type adoConnectionData struct {
	AuthenticatedUser struct {
		ID string `json:"id"`
	} `json:"authenticatedUser"`
}

// httpADOIdentityResolver is the production resolver. Results are
// cached per organisation for the lifetime of the process: the identity
// behind a PAT does not change, and re-resolving would put an
// avoidable REST round-trip on a webhook path that is already doing a
// full review's worth of work. Only successes are cached — a transient
// failure must not pin the bot into "I don't know who I am" until the
// next rollout.
type httpADOIdentityResolver struct {
	client            *http.Client
	baseURL           string
	endpointValidator func(string) bool

	mu    sync.RWMutex
	cache map[string]string
}

// NewHTTPADOIdentityResolver returns a resolver that uses the supplied
// HTTP client. Pass nil to use a sensible default (a per-call timeout
// is still applied via [context.WithTimeout], so this client does not
// need its own).
func NewHTTPADOIdentityResolver(client *http.Client) ADOIdentityResolver {
	if client == nil {
		client = &http.Client{Timeout: adoIdentityTimeout}
	}
	return &httpADOIdentityResolver{
		client:            client,
		baseURL:           adoIdentityAPIBaseURL,
		endpointValidator: isADOAPIHost,
		cache:             map[string]string{},
	}
}

// adoOrganizationSlugMaxLength bounds the organisation slug accepted
// into the endpoint's one variable path segment. Azure DevOps caps
// organisation names at 64 characters.
const adoOrganizationSlugMaxLength = 64

// isADOOrganizationSlug reports whether organization is shaped like an
// Azure DevOps organisation name — alphanumerics, `-`, `_` and `.`,
// within the service's own length cap.
//
// The slug is the ONLY caller-supplied part of the identity endpoint
// and it arrives from a webhook payload, so it is validated before
// interpolation rather than trusted: `url.PathEscape` already stops it
// escaping its segment, and this narrows it further to values the
// service could actually have issued. A malformed slug is rejected
// outright instead of being escaped into a request that can only 404.
func isADOOrganizationSlug(organization string) bool {
	if organization == "" || len(organization) > adoOrganizationSlugMaxLength {
		return false
	}
	for i := range len(organization) {
		c := organization[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' || c == '_' || c == '.':
		default:
			return false
		}
	}
	return true
}

// ResolveSelfID returns the lower-cased identity id the token
// authenticates as in the given organisation.
func (r *httpADOIdentityResolver) ResolveSelfID(
	ctx context.Context,
	organization, token string,
) (string, error) {
	if !isADOOrganizationSlug(organization) {
		return "", fmt.Errorf("refusing to resolve identity for malformed organization %q", organization)
	}
	if token == "" {
		return "", errors.New("ADO PAT is empty")
	}
	if cached, ok := r.cached(organization); ok {
		return cached, nil
	}

	id, err := r.fetchSelfID(ctx, organization, token)
	if err != nil {
		return "", err
	}
	r.remember(organization, id)
	return id, nil
}

// fetchSelfID performs the uncached `_apis/connectionData` round-trip.
func (r *httpADOIdentityResolver) fetchSelfID(
	ctx context.Context,
	organization, token string,
) (string, error) {
	endpoint := fmt.Sprintf(
		"%s/%s/_apis/connectionData?api-version=%s",
		r.baseURL, url.PathEscape(organization), adoConnectionDataAPIVersion,
	)
	if !r.endpointValidator(endpoint) {
		return "", fmt.Errorf("refusing to resolve identity against non-ADO host in %q", endpoint)
	}

	ctx, cancel := context.WithTimeout(ctx, adoIdentityTimeout)
	defer cancel()

	// gosec's taint analysis follows the organisation slug from the
	// webhook body into this URL and reports G704. The request is not
	// attacker-directable: the scheme and host are the compile-time
	// constant `adoIdentityAPIBaseURL`, the slug is the only variable
	// part, it is rejected unless it matches `isADOOrganizationSlug`,
	// `url.PathEscape`d so it cannot break out of its path segment, and
	// the assembled endpoint is then re-validated by
	// `endpointValidator` (`isADOAPIHost` in production). The caller
	// additionally refuses to resolve for an off-allowlist organisation.
	//nolint:gosec // G704: host is constant; the one variable path segment is validated, escaped and re-checked.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return "", fmt.Errorf("build connectionData request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(":"+token)))

	//nolint:gosec // G704: same request as above — see the rationale on http.NewRequestWithContext.
	resp, err := r.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("connectionData GET: %w", err)
	}
	defer func() { _, _ = io.Copy(io.Discard, resp.Body); _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("connectionData GET returned %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, adoIdentityResponseLimit))
	if err != nil {
		return "", fmt.Errorf("read connectionData body: %w", err)
	}

	var data adoConnectionData
	if decodeErr := json.Unmarshal(body, &data); decodeErr != nil {
		return "", fmt.Errorf("decode connectionData body: %w", decodeErr)
	}
	id := strings.ToLower(strings.TrimSpace(data.AuthenticatedUser.ID))
	if id == "" {
		return "", errors.New("connectionData carried no authenticatedUser.id")
	}
	// The id is compared against comment content and written into
	// operator-facing logs, so the response is validated rather than
	// trusted: anything that is not a canonical GUID is a sign the
	// request did not reach the service we think it did.
	if !support.IsIdentityID(id) {
		return "", fmt.Errorf("connectionData returned a malformed authenticatedUser.id %q", id)
	}
	return id, nil
}

func (r *httpADOIdentityResolver) cached(organization string) (string, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	id, ok := r.cache[organization]
	return id, ok
}

func (r *httpADOIdentityResolver) remember(organization, id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cache == nil {
		r.cache = map[string]string{}
	}
	r.cache[organization] = id
}
