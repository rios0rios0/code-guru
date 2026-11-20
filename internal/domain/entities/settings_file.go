package entities

import (
	"os"
	"path/filepath"
	"slices"

	configHelpers "github.com/rios0rios0/gitforge/v4/pkg/config/domain/helpers"
)

// settingsFileName is the stem gitforge builds code-guru's configuration file
// names from: `.code-guru.yaml`, `.code-guru.yml`, `code-guru.yaml` and
// `code-guru.yml`.
const settingsFileName = "code-guru"

// settingsFileNames are the names a configuration file may have, in the order
// gitforge's finders try them in each directory.
//
//nolint:gochecknoglobals // read-only list used as a constant
var settingsFileNames = []string{
	"." + settingsFileName + ".yaml",
	"." + settingsFileName + ".yml",
	settingsFileName + ".yaml",
	settingsFileName + ".yml",
}

// workingDirectorySettingsLocations are the directories, relative to the
// working directory, where earlier releases looked for a configuration file
// before the home directory (gitforge's FindConfigFile), in its order.
//
//nolint:gochecknoglobals // read-only list used as a constant
var workingDirectorySettingsLocations = []string{".", ".config", "configs"}

// FindSettingsFile returns the configuration file code-guru reads when
// `--config` names none: the operator's own, in the home directory (`~`, then
// `~/.config`), or "" when there is none, and the settings then come from the
// CODE_GURU_* variables. It also returns every configuration file in the
// working directory (`.`, `.config` and `configs`) that it leaves unread, in
// the order earlier releases searched them, so the caller can say so: earlier
// releases read the first of them.
//
// The working directory is never read on its own because code-guru reviews
// other people's code and often runs inside a checkout of it, such as a pull
// request's branch or a CI job's workspace, where a `.code-guru.yaml` is the
// pull request author's to write. Read as configuration, it would choose the
// program code-guru starts (`ai.claude.binary_path`), the token it acts with
// (`providers[].token` expands any variable and reads any file), and whether it
// approves and merges (`ai.submit_native_review`, `trivial`). A file there is
// read only when `--config` names it, which is the operator's own choice.
//
// A home directory that is not an absolute path (`HOME=.`) is not searched: it
// would be the working directory again.
func FindSettingsFile() (string, []string) {
	home, err := os.UserHomeDir()
	if err != nil || !filepath.IsAbs(home) {
		return "", unreadWorkingDirectoryFiles(nil)
	}
	path, err := configHelpers.FindGlobalConfigFile(settingsFileName)
	if err != nil {
		path = ""
	}
	return path, unreadWorkingDirectoryFiles(existingSettingsFiles([]string{home, filepath.Join(home, ".config")}))
}

// settingsFile is a configuration file that exists.
type settingsFile struct {
	path string
	info os.FileInfo
}

// sameFile reports whether both name one file.
func (f settingsFile) sameFile(other settingsFile) bool {
	return os.SameFile(f.info, other.info)
}

// existingSettingsFiles returns every configuration file in locations, in the
// order gitforge's finders try them: each name in a directory before the next
// directory.
func existingSettingsFiles(locations []string) []settingsFile {
	var found []settingsFile
	for _, location := range locations {
		for _, name := range settingsFileNames {
			path := filepath.Join(location, name)
			if info, err := os.Stat(path); err == nil {
				found = append(found, settingsFile{path: path, info: info})
			}
		}
	}
	return found
}

// unreadWorkingDirectoryFiles returns every configuration file in the working
// directory except one of homeFiles, the files the home directory's search
// looks at: when the working directory is the home directory (or `~/.config`),
// a file there is the operator's own, read or outranked by the one the home
// search picks, not skipped for being in the working directory.
func unreadWorkingDirectoryFiles(homeFiles []settingsFile) []string {
	var unread []string
	for _, candidate := range existingSettingsFiles(workingDirectorySettingsLocations) {
		if !slices.ContainsFunc(homeFiles, candidate.sameFile) {
			unread = append(unread, candidate.path)
		}
	}
	return unread
}
