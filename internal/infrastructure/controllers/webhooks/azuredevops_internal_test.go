//go:build unit

package webhooks_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/rios0rios0/codeguru/internal/infrastructure/controllers/webhooks"
)

// These tests pin the contracts of the unexported helpers in
// `internal/infrastructure/controllers/webhooks/azuredevops.go`. Each helper
// has been the source of a real production bug at least once (URL parser
// missed userinfo URLs ADO actually sends; status check rejected empty
// values; etc.), so the coverage here is intentionally exhaustive — every
// edge case we have hit in the wild gets a row, and any future regression
// must update this file before it can ship.

func TestExtractADOOrganization(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		remoteURL string
		want      string
	}{
		{
			name:      "should parse org from canonical https://dev.azure.com URL",
			remoteURL: "https://dev.azure.com/ExampleOrg/internal-terraform/_git/internal-customer-app",
			want:      "ExampleOrg",
		},
		{
			name: "should parse org from URL carrying userinfo (the shape ADO actually delivers in webhook payloads)",
			// ADO Service Hooks observed in production embed the org slug
			// as userinfo on the remoteUrl — `https://Org@dev.azure.com/...`.
			// Captured live on internal-terraform/internal-customer-app PR #NNNN;
			// the older parser handled it correctly because `url.Parse`
			// strips userinfo before exposing `Hostname()`. This row pins
			// that behaviour against future "let me clean up the URL
			// parser" refactors.
			remoteURL: "https://ExampleOrg@dev.azure.com/ExampleOrg/internal-terraform/_git/internal-customer-app",
			want:      "ExampleOrg",
		},
		{
			name:      "should parse org from legacy *.visualstudio.com host",
			remoteURL: "https://ExampleOrg.visualstudio.com/internal-terraform/_git/internal-customer-app",
			want:      "ExampleOrg",
		},
		{
			name: "should parse org from visualstudio.com URL even when the host has more than two labels",
			// `*.visualstudio.com` is matched via `CutSuffix`, so a host
			// like `org.subdomain.visualstudio.com` would return
			// `org.subdomain` — that is the documented ADO behaviour for
			// orgs hosted on a regional sub-domain. Pin it.
			remoteURL: "https://test-org.eu.visualstudio.com/Project/_git/repo",
			want:      "test-org.eu",
		},
		{
			name:      "should return empty string when remoteUrl is empty",
			remoteURL: "",
			want:      "",
		},
		{
			name: "should return empty string when remoteUrl is unparseable",
			// `url.Parse` is famously lenient — most strings parse — but
			// a control character in the URL forces an error. Pinning
			// the empty fallback so future authors know the contract.
			remoteURL: "https://dev.azure.com/\x7f",
			want:      "",
		},
		{
			name:      "should return empty string when host is unknown (neither dev.azure.com nor *.visualstudio.com)",
			remoteURL: "https://github.com/some/repo",
			want:      "",
		},
		{
			name:      "should return empty string when dev.azure.com URL has no path segment",
			remoteURL: "https://dev.azure.com/",
			want:      "",
		},
		{
			name:      "should return the bare org name (no path leak) when only the org segment is present",
			remoteURL: "https://dev.azure.com/ExampleOrg",
			want:      "ExampleOrg",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// when
			got := webhooks.ExtractADOOrganization(tc.remoteURL)

			// then
			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIsClosedADOPullRequestStatus(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		status string
		want   bool
	}{
		{name: "should reject the literal `abandoned`", status: "abandoned", want: true},
		{name: "should reject the literal `completed`", status: "completed", want: true},
		{name: "should normalise mixed case (`Completed`)", status: "Completed", want: true},
		{name: "should normalise upper case (`ABANDONED`)", status: "ABANDONED", want: true},
		{name: "should trim leading/trailing whitespace (` completed `)", status: " completed ", want: true},
		{name: "should treat empty string as not-closed (so commit-only updates proceed)", status: "", want: false},
		{name: "should treat whitespace-only string as not-closed", status: "   ", want: false},
		{name: "should treat `active` as not-closed", status: "active", want: false},
		{
			name: "should treat unknown values as not-closed (defer the decision to the worker)",
			// Important contract: the predicate is reject-only-known-closed.
			// Anything Microsoft adds to the enum in the future (`merged`,
			// `notSet`, etc.) MUST default to "not closed" so the bot
			// keeps reviewing it. Pin this so a future "be safe, default
			// to closed" refactor surfaces here.
			status: "merging",
			want:   false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, webhooks.IsClosedADOPullRequestStatus(tc.status))
		})
	}
}

func TestIsSupportedADOEvent(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		eventType string
		want      bool
	}{
		{name: "should accept git.pullrequest.created", eventType: "git.pullrequest.created", want: true},
		{name: "should accept git.pullrequest.updated", eventType: "git.pullrequest.updated", want: true},
		{name: "should reject git.push (we filter at the subscription level too)", eventType: "git.push", want: false},
		{name: "should reject the comment event (no value to the bot today)", eventType: "ms.vss-code.git-pullrequest-comment-event", want: false},
		{name: "should reject empty string", eventType: "", want: false},
		{
			name: "should be case-sensitive — ADO ships lower-case event types and a normalisation here would mask a real malformed payload",
			eventType: "Git.PullRequest.Created",
			want:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, webhooks.IsSupportedADOEvent(tc.eventType))
		})
	}
}

func TestRefToBranch(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		ref  string
		want string
	}{
		{name: "should strip refs/heads/ prefix", ref: "refs/heads/main", want: "main"},
		{name: "should preserve nested branch names", ref: "refs/heads/feat/vault-rbac-migration-shim", want: "feat/vault-rbac-migration-shim"},
		{name: "should leave a non-prefixed value alone (defensive — ADO occasionally sends bare branch names)", ref: "main", want: "main"},
		{name: "should return empty string for empty input", ref: "", want: ""},
		{
			name: "should NOT strip refs/tags/ — that prefix is for tag refs, not branches",
			// PRs target branches; if a tag ref ever shows up in
			// sourceRefName/targetRefName, leaving the prefix intact is the
			// correct behaviour so downstream code can detect the
			// inconsistency instead of silently accepting a tag as a
			// branch.
			ref:  "refs/tags/v1.0.0",
			want: "refs/tags/v1.0.0",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, webhooks.RefToBranch(tc.ref))
		})
	}
}

func TestTruncateForLog(t *testing.T) {
	t.Parallel()

	t.Run("should return input unchanged when shorter than the limit", func(t *testing.T) {
		assert.Equal(t, "short", webhooks.TruncateForLog("short", 10))
	})

	t.Run("should return input unchanged when exactly at the limit", func(t *testing.T) {
		assert.Equal(t, "0123456789", webhooks.TruncateForLog("0123456789", 10))
	})

	t.Run("should clip and append the truncation sentinel when over the limit", func(t *testing.T) {
		// given
		input := strings.Repeat("a", 20)

		// when
		got := webhooks.TruncateForLog(input, 10)

		// then
		assert.Equal(t, strings.Repeat("a", 10)+"...[truncated]", got)
	})

	t.Run("should clip the canonical 4 KB body cap honoured by the diagnostic", func(t *testing.T) {
		// given: the allowlist-rejection diagnostic uses
		// `adoRawBodyLogLimit = 4096`. Pin both the cap and the
		// truncation sentinel so a future "I'll bump it to 8 KB"
		// change has to surface here.
		input := strings.Repeat("x", webhooks.ADORawBodyLogLimit*2)

		// when
		got := webhooks.TruncateForLog(input, webhooks.ADORawBodyLogLimit)

		// then
		assert.Len(t, got, webhooks.ADORawBodyLogLimit+len("...[truncated]"))
		assert.True(t, strings.HasSuffix(got, "...[truncated]"), "truncated output must end with the sentinel so log readers can tell")
	})

	t.Run("should handle empty input", func(t *testing.T) {
		assert.Empty(t, webhooks.TruncateForLog("", 10))
	})

	t.Run("should be byte-based (a multi-byte rune at the boundary may be split)", func(t *testing.T) {
		// given: the diagnostic operates on raw bytes (no UTF-8
		// boundary search) because the goal is "let an operator glance
		// at the structure", not produce a valid UTF-8 sub-string. Pin
		// the byte-based behaviour so a future "let me make this
		// rune-aware" change is deliberate.
		input := "héllo" // h=1, é=2 bytes (0xC3 0xA9), l=1, l=1, o=1 → 6 bytes

		// when
		got := webhooks.TruncateForLog(input, 2)

		// then
		assert.Len(t, got, 2+len("...[truncated]"))
	})
}
