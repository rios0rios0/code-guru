package entities

import (
	"os"

	configHelpers "github.com/rios0rios0/gitforge/v4/pkg/config/domain/helpers"
)

// settingsFileName is the stem gitforge builds code-guru's configuration file
// names from: `.code-guru.yaml`, `.code-guru.yml`, `code-guru.yaml` and
// `code-guru.yml`.
const settingsFileName = "code-guru"

// FindSettingsFile returns the configuration file code-guru reads when
// `--config` names none: the operator's own, in the home directory (`~`, then
// `~/.config`), or "" when there is none, and the settings then come from the
// CODE_GURU_* variables. It also returns the configuration file in the working
// directory (`.`, `.config` or `configs`) that it leaves unread, or "", so the
// caller can say so: earlier releases read that file first.
//
// The working directory is never read on its own because code-guru reviews
// other people's code and often runs inside a checkout of it, such as a pull
// request's branch or a CI job's workspace, where a `.code-guru.yaml` is the
// pull request author's to write. Read as configuration, it would choose the
// program code-guru starts (`ai.claude.binary_path`), the token it acts with
// (`providers[].token` expands any variable and reads any file), and whether it
// approves and merges (`ai.submit_native_review`, `trivial`). A file there is
// read only when `--config` names it, which is the operator's own choice.
func FindSettingsFile() (string, string) {
	path, err := configHelpers.FindGlobalConfigFile(settingsFileName)
	if err != nil {
		path = ""
	}
	return path, unreadWorkingDirectoryFile(path)
}

// unreadWorkingDirectoryFile returns the configuration file in the working
// directory, unless it is `used` itself, as when the working directory is the
// home directory. gitforge's FindConfigFile searches the working directory
// first and then the home directory in the order FindGlobalConfigFile does, so
// any result of it other than `used` lies in the working directory.
func unreadWorkingDirectoryFile(used string) string {
	candidate, err := configHelpers.FindConfigFile(settingsFileName)
	if err != nil || candidate == "" {
		return ""
	}
	if used != "" && sameFile(candidate, used) {
		return ""
	}
	return candidate
}

// sameFile reports whether both paths name one existing file.
func sameFile(first, second string) bool {
	firstInfo, firstErr := os.Stat(first)
	secondInfo, secondErr := os.Stat(second)
	return firstErr == nil && secondErr == nil && os.SameFile(firstInfo, secondInfo)
}
