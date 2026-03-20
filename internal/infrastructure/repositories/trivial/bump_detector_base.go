package trivial

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	logger "github.com/sirupsen/logrus"

	"github.com/rios0rios0/codeguru/internal/domain/repositories"
	"github.com/rios0rios0/codeguru/internal/infrastructure/repositories/trivial/autobump"
)

const autobumpConfigPath = ".autobump.yaml"

// detectBump implements the shared bump detection logic used by all bump-* detectors.
//
// It first checks whether all PR files fall within the default expected set.
// If .autobump.yaml exists in the repo, it expands the expected set with
// version_files and validates that all required files are present.
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

	// first pass: check all PR files fall within the default allowed set
	for _, f := range dctx.Files {
		base := filepath.Base(f)
		if !defaultAllowed[base] && (defaultAllowedFn == nil || !defaultAllowedFn(f)) {
			return repositories.DetectionResult{}
		}
	}

	// try autobump validation if file content fetcher is available
	if dctx.FileContentFetcher != nil {
		result, handled := tryAutobumpValidation(
			ctx, dctx, detectorName, autobumpLangKey,
		)
		if handled {
			return result
		}
	}

	// fallback: all files matched the default set
	return repositories.DetectionResult{
		Detected: true,
		Verdict:  "approve",
		Summary: fmt.Sprintf(
			"%s version bump detected (%d files). Auto-approved by trivial PR policy.",
			detectorName, len(dctx.Files),
		),
	}
}

func tryAutobumpValidation(
	ctx context.Context,
	dctx repositories.DetectionContext,
	detectorName string,
	autobumpLangKey string,
) (repositories.DetectionResult, bool) {
	if !dctx.FileContentFetcher.HasFile(ctx, autobumpConfigPath) {
		return repositories.DetectionResult{}, false
	}

	content, err := dctx.FileContentFetcher.GetFileContent(ctx, autobumpConfigPath)
	if err != nil {
		logger.Warnf("failed to read %s: %v, falling back to default patterns", autobumpConfigPath, err)
		return repositories.DetectionResult{}, false
	}

	cfg, err := autobump.ParseConfig(content)
	if err != nil {
		logger.Warnf("failed to parse %s: %v, falling back to default patterns", autobumpConfigPath, err)
		return repositories.DetectionResult{}, false
	}

	versionPaths := autobump.ResolveVersionFilePaths(cfg, autobumpLangKey, dctx.RepoName)

	// build the full expected set: CHANGELOG.md + all version file paths
	expected := map[string]bool{"CHANGELOG.md": true}
	for _, p := range versionPaths {
		expected[p] = true
	}

	// check all expected files are present in the PR
	prFiles := make(map[string]bool, len(dctx.Files))
	for _, f := range dctx.Files {
		prFiles[f] = true
	}

	var missing []string
	for exp := range expected {
		if !prFiles[exp] {
			missing = append(missing, exp)
		}
	}

	// also check PR doesn't have extra files outside the expected set
	for _, f := range dctx.Files {
		if !expected[f] {
			missing = append(missing, fmt.Sprintf("unexpected file: %s", f))
		}
	}

	if len(missing) > 0 {
		return repositories.DetectionResult{
			Detected: true,
			Verdict:  "reject",
			Summary: fmt.Sprintf(
				"%s version bump is incomplete per .autobump.yaml: %s",
				detectorName, strings.Join(missing, ", "),
			),
		}, true
	}

	return repositories.DetectionResult{
		Detected: true,
		Verdict:  "approve",
		Summary: fmt.Sprintf(
			"%s version bump detected (%d files, validated against .autobump.yaml). Auto-approved by trivial PR policy.",
			detectorName,
			len(dctx.Files),
		),
	}, true
}
