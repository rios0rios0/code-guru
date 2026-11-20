package support

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
)

// ErrNotAzureDevOpsOrganization reports an Azure DevOps organization, as
// written in an `organizations` entry or handed to the fallback token, that
// does not name an organization on dev.azure.com.
var ErrNotAzureDevOpsOrganization = errors.New("not an Azure DevOps organization")

// azureDevOpsOrganizationPattern matches an Azure DevOps organization name:
// letters, digits and hyphens, starting and ending with a letter or a digit.
var azureDevOpsOrganizationPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?$`)

// azureDevOpsHost and visualStudioHostSuffix are the only hosts an Azure
// DevOps organization is written with.
const (
	azureDevOpsHost        = "dev.azure.com"
	visualStudioHostSuffix = ".visualstudio.com"
)

// AzureDevOpsOrganizationName returns the name of the Azure DevOps
// organization an entry names: its bare name (`MyOrg`), its
// https://dev.azure.com/MyOrg URL, or its legacy https://myorg.visualstudio.com
// URL. Anything else — another host, another scheme, a port, credentials in
// the URL — is refused with ErrNotAzureDevOpsOrganization.
//
// The name is what callers hand gitforge, which then builds
// https://dev.azure.com/<name> itself. Its Azure DevOps provider keeps any
// other https:// entry as the base URL of its requests, so passing an entry
// on is what sent the token — configured or the runner's own — to whichever
// host a working-directory `.code-guru.yaml` named.
func AzureDevOpsOrganizationName(entry string) (string, error) {
	entry = strings.TrimSpace(entry)
	name := entry
	if strings.Contains(entry, "://") {
		var ok bool
		if name, ok = organizationFromURL(entry); !ok {
			return "", refuseOrganization(entry)
		}
	}
	name = strings.TrimSuffix(name, "/")
	if !azureDevOpsOrganizationPattern.MatchString(name) {
		return "", refuseOrganization(entry)
	}
	return name, nil
}

// organizationFromURL returns the organization an https URL on an Azure
// DevOps host names: the first path segment on dev.azure.com, the host's own
// label on visualstudio.com.
func organizationFromURL(entry string) (string, bool) {
	parsed, err := url.Parse(entry)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") || parsed.User != nil || parsed.Port() != "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	if host == azureDevOpsHost {
		name, _, _ := strings.Cut(strings.TrimPrefix(parsed.Path, "/"), "/")
		return name, true
	}
	if label, ok := strings.CutSuffix(host, visualStudioHostSuffix); ok {
		return label, true
	}
	return "", false
}

func refuseOrganization(entry string) error {
	return fmt.Errorf(
		"%w: %q (write the organization's name, or https://dev.azure.com/<name>)",
		ErrNotAzureDevOpsOrganization, entry,
	)
}
