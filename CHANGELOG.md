# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

When a new release is proposed:

1. Create a new branch `bump/x.x.x` (this isn't a long-lived branch!!!);
2. The Unreleased section on `CHANGELOG.md` gets a version number and date;
3. Open a Pull Request with the bump version changes targeting the `main` branch;
4. When the Pull Request is merged, a new Git tag must be created using [GitHub environment](https://github.com/rios0rios0/code-guru/tags).

Releases to productive environments should run from a tagged version.
Exceptions are acceptable depending on the circumstances (critical bug fixes that can be cherry-picked, etc.).

## [Unreleased]

## [0.1.0] - 2026-03-12

### Added

- added GitHub Actions workflow for CI/CD pipeline
- added Claude Code CLI as an AI backend (alongside OpenAI) with configurable `max_turns`
- added YAML frontmatter stripping from rule files to extract `paths` globs
- added glob-based rule matching for precise language/file filtering
- added diff fallback in review command for providers without per-file patches (e.g. Azure DevOps)
- added `SplitUnifiedDiff` utility for splitting multi-file diffs into per-file chunks
- added `DiscoverCommand` in domain layer to separate business logic from controller
- added `end_line` and `suggestion` fields to `ReviewComment` for multi-line and code suggestion support
- added unit tests for prompt builder, file classifier, URL parser, diff splitter, rules repository, and response parsing

### Changed

- replaced local file extension classifier with langforge's `ClassifyFileByExtension` and `ClassifyFilesByExtension` to centralize language abstractions
- replaced inline `parseGitHubURL` and `parseAzureDevOpsURL` PR URL parsing with gitforge's `ParsePullRequestURL` to consolidate duplicated code
- replaced raw struct literals in tests with testkit builders for consistent test data construction
- replaced local `ProviderConfig` struct, `resolveToken()`, and `FindConfigFile()` with gitforge's shared implementations
- changed Claude CLI backend to pass user prompt via stdin instead of CLI argument to avoid OS argument length limits
- changed Claude CLI response parsing to handle JSON wrapped in markdown code fences
- changed OpenAI backend to enforce JSON response format via `ResponseFormat` parameter
- changed system prompt to include strict JSON-only instructions, line number rules, and severity definitions
- changed `DiscoverController` to delegate to `DiscoverCommand` following Clean Architecture
- changed the Go module dependencies to their latest versions

### Fixed

- fixed `exhaustive` findings by adding missing `Language` and `ServiceType` keys to classifier and URL parser maps

