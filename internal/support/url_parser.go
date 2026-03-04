package support

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// ParsedPRURL holds the components extracted from a pull request URL.
type ParsedPRURL struct {
	ProviderType string
	Organization string
	Project      string // Azure DevOps only
	RepoName     string
	PRID         int
}

// ParsePullRequestURL extracts provider, org, repo, and PR ID from a pull request URL.
// Supported formats:
//   - https://github.com/{org}/{repo}/pull/{id}
//   - https://dev.azure.com/{org}/{project}/_git/{repo}/pullrequest/{id}
func ParsePullRequestURL(rawURL string) (*ParsedPRURL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("invalid URL: %w", err)
	}

	host := strings.ToLower(parsed.Host)
	segments := strings.Split(strings.Trim(parsed.Path, "/"), "/")

	switch {
	case strings.Contains(host, "github.com"):
		return parseGitHubURL(segments)
	case strings.Contains(host, "dev.azure.com"):
		return parseAzureDevOpsURL(segments)
	default:
		return nil, fmt.Errorf("unsupported provider host: %q", host)
	}
}

func parseGitHubURL(segments []string) (*ParsedPRURL, error) {
	// Expected: {org}/{repo}/pull/{id}
	if len(segments) < 4 || segments[2] != "pull" {
		return nil, errors.New("invalid GitHub PR URL format, expected: /{org}/{repo}/pull/{id}")
	}

	prID, err := strconv.Atoi(segments[3])
	if err != nil {
		return nil, fmt.Errorf("invalid PR ID %q: %w", segments[3], err)
	}

	return &ParsedPRURL{
		ProviderType: "github",
		Organization: segments[0],
		RepoName:     segments[1],
		PRID:         prID,
	}, nil
}

func parseAzureDevOpsURL(segments []string) (*ParsedPRURL, error) {
	// Expected: {org}/{project}/_git/{repo}/pullrequest/{id}
	if len(segments) < 6 || segments[2] != "_git" || segments[4] != "pullrequest" {
		return nil, errors.New(
			"invalid Azure DevOps PR URL format, expected: /{org}/{project}/_git/{repo}/pullrequest/{id}",
		)
	}

	prID, err := strconv.Atoi(segments[5])
	if err != nil {
		return nil, fmt.Errorf("invalid PR ID %q: %w", segments[5], err)
	}

	return &ParsedPRURL{
		ProviderType: "azuredevops",
		Organization: segments[0],
		Project:      segments[1],
		RepoName:     segments[3],
		PRID:         prID,
	}, nil
}
