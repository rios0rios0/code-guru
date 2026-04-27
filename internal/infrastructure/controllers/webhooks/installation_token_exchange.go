package webhooks

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// githubAPIBase is the base URL for the GitHub REST API. Overridable in tests.
//
//nolint:gochecknoglobals // configurable for tests
var githubAPIBase = "https://api.github.com"

// tokenSafetyMargin is how long before expiry a cached installation token is
// considered stale and refreshed. Keeps a buffer for in-flight requests.
const tokenSafetyMargin = 5 * time.Minute

// installationTokenJWTLifetime is the lifetime of the App JWT used to exchange
// for an installation token. GitHub allows up to 10 minutes; we use 9 to leave
// headroom for clock skew.
const installationTokenJWTLifetime = 9 * time.Minute

// httpClientTimeout caps the round trip to GitHub when exchanging the App JWT
// for an installation access token.
const httpClientTimeout = 30 * time.Second

// cachedToken holds an installation token along with its expiry time.
type cachedToken struct {
	token     string
	expiresAt time.Time
}

// GitHubAppTokenizer exchanges a GitHub App JWT for an installation access
// token, caching results until shortly before expiry.
type GitHubAppTokenizer struct {
	appID      int64
	privateKey *rsa.PrivateKey
	httpClient *http.Client
	cache      sync.Map
	now        func() time.Time
}

// NewGitHubAppTokenizer parses the PEM-encoded private key and returns a tokenizer
// ready to mint installation tokens for any installation the App is installed on.
func NewGitHubAppTokenizer(appID int64, privateKeyPEM string) (*GitHubAppTokenizer, error) {
	if appID == 0 {
		return nil, errors.New("github app id is required")
	}
	if privateKeyPEM == "" {
		return nil, errors.New("github app private key is required")
	}

	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("failed to decode private key PEM")
	}

	key, err := parseRSAPrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	return &GitHubAppTokenizer{
		appID:      appID,
		privateKey: key,
		httpClient: &http.Client{Timeout: httpClientTimeout},
		now:        time.Now,
	}, nil
}

// InstallationToken returns a cached installation access token, refreshing it
// from GitHub when the cache is empty or the token is within the safety margin
// of its expiry.
func (g *GitHubAppTokenizer) InstallationToken(ctx context.Context, installationID int64) (string, error) {
	if installationID == 0 {
		return "", errors.New("installation id is required")
	}

	if cached, found := g.cache.Load(installationID); found {
		if entry, isToken := cached.(cachedToken); isToken && g.now().Before(entry.expiresAt.Add(-tokenSafetyMargin)) {
			return entry.token, nil
		}
	}

	jwtToken, err := g.signJWT()
	if err != nil {
		return "", fmt.Errorf("failed to sign App JWT: %w", err)
	}

	token, expiresAt, err := g.exchange(ctx, jwtToken, installationID)
	if err != nil {
		return "", err
	}

	g.cache.Store(installationID, cachedToken{token: token, expiresAt: expiresAt})
	return token, nil
}

func (g *GitHubAppTokenizer) signJWT() (string, error) {
	now := g.now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now.Add(-30 * time.Second)),
		ExpiresAt: jwt.NewNumericDate(now.Add(installationTokenJWTLifetime)),
		Issuer:    strconv.FormatInt(g.appID, 10),
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	return tok.SignedString(g.privateKey)
}

func (g *GitHubAppTokenizer) exchange(
	ctx context.Context, jwtToken string, installationID int64,
) (string, time.Time, error) {
	endpoint := fmt.Sprintf("%s/app/installations/%d/access_tokens", githubAPIBase, installationID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, http.NoBody)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("failed to build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+jwtToken)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-Github-Api-Version", "2022-11-28")

	resp, err := g.httpClient.Do(req)
	if err != nil {
		return "", time.Time{}, fmt.Errorf("token exchange request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", time.Time{}, fmt.Errorf("token exchange returned %d: %s", resp.StatusCode, string(body))
	}

	var payload struct {
		Token     string    `json:"token"`
		ExpiresAt time.Time `json:"expires_at"`
	}
	if jsonErr := json.Unmarshal(body, &payload); jsonErr != nil {
		return "", time.Time{}, fmt.Errorf("failed to parse token response: %w", jsonErr)
	}
	if payload.Token == "" {
		return "", time.Time{}, errors.New("token exchange returned empty token")
	}
	return payload.Token, payload.ExpiresAt, nil
}

// parseRSAPrivateKey accepts both PKCS#1 and PKCS#8 encoded RSA keys, since
// GitHub Apps issue PKCS#1 by default but operators commonly convert to PKCS#8.
func parseRSAPrivateKey(der []byte) (*rsa.PrivateKey, error) {
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		return nil, err
	}
	rsaKey, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("private key is not RSA")
	}
	return rsaKey, nil
}
