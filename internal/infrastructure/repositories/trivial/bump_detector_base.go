package trivial

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial/autobump"
)

const autobumpConfigPath = ".autobump.yaml"

// detectBump implements the shared bump detection logic used by all bump-* detectors.
//
// A release pull request may change three kinds of file: the changelog, the
// chlog fragments the release compiled into it (deleted, under
// `.changes/unreleased/`), and the language's version files. A version file
// is one of the detector's defaults, one AutoBump's built-in defaults declare
// for the language, or one the repository's `.autobump.yaml` declares.
// Anything else, including any other deletion, means the pull request is not
// a release.
//
// When .autobump.yaml declares version files, all of them must be present or
// the PR is rejected. Every version file must then change nothing but its
// version strings, each to the version the changelog gains a heading for:
// a version file can be code, so its name alone never approves it.
func detectBump(
	ctx context.Context,
	dctx repositories.DetectionContext,
	detectorName string,
	autobumpLangKey string,
	defaultAllowed map[string]bool,
	defaultAllowedFn func(string) bool,
) repositories.DetectionResult {
	if len(dctx.Files) == 0 {
		return repositories.DetectionResult{}
	}

	// try to load autobump config first to expand the allowed set
	autobumpRequired, autobumpActive := loadAutobumpRequired(ctx, dctx, autobumpLangKey)
	allowed := versionFileMatcher{
		defaults:   defaultAllowed,
		defaultsFn: defaultAllowedFn,
		autobump:   autobumpRequired,
		builtin:    autobump.BuiltinVersionFilePatterns(autobumpLangKey, dctx.RepoName),
	}

	versionFiles, isRelease := releaseVersionFiles(dctx, allowed)
	if !isRelease {
		return repositories.DetectionResult{}
	}

	// if autobump is active, check all required files are present
	if autobumpActive {
		if missing := missingAutobumpFiles(dctx, autobumpRequired); len(missing) > 0 {
			return repositories.DetectionResult{
				Detected: true,
				Verdict:  verdictReject,
				Summary: fmt.Sprintf(
					"%s version bump is incomplete per .autobump.yaml: %s",
					detectorName, strings.Join(missing, ", "),
				),
			}
		}
	}

	if !versionFilesChangeOnlyTheVersion(ctx, dctx, versionFiles) {
		return repositories.DetectionResult{}
	}

	summary := fmt.Sprintf(
		"%s version bump detected (%d files). Auto-approved by trivial PR policy.",
		detectorName, len(dctx.Files),
	)
	if autobumpActive {
		summary = fmt.Sprintf(
			"%s version bump detected (%d files, validated against .autobump.yaml). Auto-approved by trivial PR policy.",
			detectorName,
			len(dctx.Files),
		)
	}
	return repositories.DetectionResult{Detected: true, Verdict: verdictApprove, Summary: summary}
}

// versionFileMatcher decides which changed files a bump detector accepts as
// version files.
type versionFileMatcher struct {
	// defaults are the detector's own version file names, matched on the
	// base name; defaultsFn widens them (e.g. any `*/__init__.py`).
	defaults   map[string]bool
	defaultsFn func(string) bool
	// autobump holds the paths the repository's .autobump.yaml declares.
	autobump map[string]bool
	// builtin holds the path patterns of AutoBump's built-in defaults.
	builtin []string
}

func (m versionFileMatcher) matches(filePath string) bool {
	return m.defaults[filepath.Base(filePath)] ||
		(m.defaultsFn != nil && m.defaultsFn(filePath)) ||
		m.autobump[filePath] ||
		autobump.MatchesAny(m.builtin, filePath)
}

// releaseVersionFiles sorts a pull request's files for a bump detector. It
// returns the version files, i.e. every changed file that is neither a
// changelog nor a chlog fragment the release compiled, and false when a file
// has no place in a release: a deletion of anything but a pending fragment,
// or a file that is not a version file of the language.
func releaseVersionFiles(dctx repositories.DetectionContext, allowed versionFileMatcher) ([]string, bool) {
	var versionFiles []string
	for _, f := range dctx.Files {
		if dctx.DeletedFiles[f] {
			if isChlogFragment(f) {
				continue
			}
			return nil, false
		}
		if isChangelog(f) {
			continue
		}
		if !allowed.matches(f) {
			return nil, false
		}
		versionFiles = append(versionFiles, f)
	}
	return versionFiles, true
}

// versionFilesChangeOnlyTheVersion reports whether every version file's diff
// replaces version strings with the release version and does nothing else.
// The release version is the one the changelog gains a heading for. A
// release that touches no version file (only the changelog, perhaps with the
// chlog fragments it compiled) has nothing to read and passes.
//
// Without a readable diff the answer is no: a version file can be code
// (`main.go`, `__init__.py`), and a detector never approves code it could
// not read. The pull request then gets the LLM review instead.
func versionFilesChangeOnlyTheVersion(
	ctx context.Context,
	dctx repositories.DetectionContext,
	versionFiles []string,
) bool {
	if len(versionFiles) == 0 {
		return true
	}
	if dctx.FileDiffFetcher == nil {
		return false
	}
	release, found := releaseVersion(ctx, dctx)
	if !found {
		return false
	}
	for _, f := range versionFiles {
		diff, err := dctx.FileDiffFetcher.GetFileDiff(ctx, f)
		if err != nil {
			logger.Debugf("trivial: cannot read the diff of %q: %v", f, err)
			return false
		}
		if !replacesOnlyVersion(diff, release) {
			return false
		}
	}
	return true
}

// loadAutobumpRequired attempts to load .autobump.yaml and returns the set of
// required files (CHANGELOG.md + version_files) and whether autobump is active.
func loadAutobumpRequired(
	ctx context.Context,
	dctx repositories.DetectionContext,
	autobumpLangKey string,
) (map[string]bool, bool) {
	if dctx.FileContentFetcher == nil || !dctx.FileContentFetcher.HasFile(ctx, autobumpConfigPath) {
		return nil, false
	}

	content, err := dctx.FileContentFetcher.GetFileContent(ctx, autobumpConfigPath)
	if err != nil {
		logger.Warnf("failed to read %s: %v, falling back to default patterns", autobumpConfigPath, err)
		return nil, false
	}

	cfg, err := autobump.ParseConfig(content)
	if err != nil {
		logger.Warnf("failed to parse %s: %v, falling back to default patterns", autobumpConfigPath, err)
		return nil, false
	}

	versionPaths := autobump.ResolveVersionFilePaths(cfg, autobumpLangKey, dctx.RepoName)

	required := map[string]bool{changelogFile: true}
	for _, p := range versionPaths {
		required[p] = true
	}

	return required, true
}

// missingAutobumpFiles returns the files .autobump.yaml requires that the
// pull request does not change.
func missingAutobumpFiles(dctx repositories.DetectionContext, required map[string]bool) []string {
	prFiles := make(map[string]bool, len(dctx.Files))
	for _, f := range dctx.Files {
		prFiles[f] = true
	}

	var missing []string
	for exp := range required {
		if !prFiles[exp] {
			missing = append(missing, exp)
		}
	}
	slices.Sort(missing)
	return missing
}
