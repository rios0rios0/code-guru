package support_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rios0rios0/codeguru/internal/support"
)

func TestAzureDevOpsOrganizationName(t *testing.T) {
	t.Parallel()

	t.Run("should return the organization's name when the entry names one on Azure DevOps", func(t *testing.T) {
		t.Parallel()

		// given
		cases := map[string]string{
			"ExampleOrg":                                         "ExampleOrg",
			"ExampleOrg/":                                        "ExampleOrg",
			"example-org-2":                                      "example-org-2",
			"https://dev.azure.com/Example":                      "Example",
			"https://dev.azure.com/Example/":                     "Example",
			"https://DEV.AZURE.COM/Example":                      "Example",
			"https://dev.azure.com/Example/Platform/_git/demo":   "Example",
			"https://example.visualstudio.com":                   "example",
			"https://example.visualstudio.com/DefaultCollection": "example",
		}

		for entry, want := range cases {
			// when
			name, err := support.AzureDevOpsOrganizationName(entry)

			// then
			require.NoError(t, err, entry)
			assert.Equal(t, want, name, entry)
		}
	})

	t.Run("should refuse an entry that does not name an organization on Azure DevOps", func(t *testing.T) {
		t.Parallel()

		// given
		entries := []string{
			"",
			"https://attacker.example/ExampleOrg",
			"https://127.0.0.1:8443/attacker-org",
			"https://dev.azure.com.attacker.example/ExampleOrg",
			"https://attacker.example/dev.azure.com/ExampleOrg",
			"http://dev.azure.com/ExampleOrg",
			"https://dev.azure.com:443/ExampleOrg",
			"https://user@dev.azure.com/ExampleOrg",
			"https://dev.azure.com/",
			"https://a.b.visualstudio.com",
			"https://dev.azure.com/%zz",
			"ExampleOrg/Platform",
			"-ExampleOrg",
			"Example Org",
			"ftp://dev.azure.com/ExampleOrg",
		}

		for _, entry := range entries {
			// when
			name, err := support.AzureDevOpsOrganizationName(entry)

			// then
			require.ErrorIs(t, err, support.ErrNotAzureDevOpsOrganization, entry)
			assert.Empty(t, name, entry)
		}
	})

	t.Run("should name the entry and the accepted forms when it refuses one", func(t *testing.T) {
		t.Parallel()

		// given
		entry := "https://attacker.example/x"

		// when
		_, err := support.AzureDevOpsOrganizationName(entry)

		// then
		require.EqualError(t, err, `not an Azure DevOps organization: "https://attacker.example/x" `+
			`(write the organization's name, or https://dev.azure.com/<name>)`)
	})
}
