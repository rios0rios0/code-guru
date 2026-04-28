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

### Added

- added HMAC-SHA256 verification for the GitHub `pull_request` webhook endpoint via the `X-Hub-Signature-256` header
- added Basic Auth verification for the Azure DevOps Service Hook endpoint (constant `code-guru` username, configurable secret password)
- added `git.pullrequest.created` and `git.pullrequest.updated` handler that builds an `azuredevops` `ReviewProvider` and enqueues active PRs for asynchronous review
- added GitHub `pull_request` (`opened`, `synchronize`, `reopened`) handler with GitHub App installation token exchange (RS256 JWT, `sync.Map`-backed cache with a 5-minute safety margin) and a configured PAT fallback
- added a bounded asynchronous worker `Pool` (configurable `Workers` and `QueueSize`) that drains review jobs and recovers from per-job panics so a single failure does not crash the worker
- added graceful shutdown to the `serve` controller, capturing `SIGINT`/`SIGTERM` and draining both the HTTP server and the worker pool within `Server.ShutdownTimeout`
- added `Server.AllowedOrganizations` and `Server.AllowedProjects` allowlists (defense-in-depth) consulted by both webhook handlers, returning `403 Forbidden` for off-list payloads
- added a `Dockerfile` (multi-stage `golang:1.26-alpine` builder, `gcr.io/distroless/static-debian12:nonroot` runtime, `EXPOSE 8080`) and a `.dockerignore`
- added a `health` subcommand (`code-guru health`) that probes a running `serve` listener and exits `0` on `200`, `1` otherwise; used by the `Dockerfile` `HEALTHCHECK` directive (the distroless base image has no shell, no `curl`, no `wget`, so the binary doubles as its own healthcheck client)
- added a `HEALTHCHECK` directive to the `Dockerfile` calling `code-guru health` with a 30s interval and a 10s start period, so `docker run` / `compose` deployments get a live readiness signal without requiring a Kubernetes probe

### Changed

- changed the `serve` controller to register the dispatcher and itself in the DIG container so all subcommands ship with one binary
- changed the `--port` flag on `serve` to be properly registered via `BindFlags` (it previously read but never declared the flag)
- changed `Pool.Submit` to hold the same mutex used by `Shutdown` while sending on the queue, eliminating the TOCTOU race that could panic with `send on closed channel` under concurrent traffic and graceful shutdown
- changed `Pool` workers to receive a cancellable base context that `Shutdown` cancels, so in-flight `JobHandler` invocations can observe shutdown timeouts via the `ctx` argument
- changed `NewSettings` to also resolve `${ENV_VAR}`/file-path references for `server.webhook_secret` and `github_app.private_key`, so YAML literals like `${CODE_GURU_WEBHOOK_SECRET}` are expanded before reaching the auth/JWT code paths
- changed `Dispatcher.findToken` to fall back to a single untyped provider entry, so the env-only configuration (`CODE_GURU_PROVIDER_TOKEN`) works for both GitHub and Azure DevOps webhook handlers
- changed `ServeController.Execute` to validate required settings (`ai.backend`, `server.webhook_secret`) up front and exit fatally instead of starting with the empty `Settings` fallback
- changed the HTTP server's `ReadHeaderTimeout` to a dedicated `defaultReadHeaderTimeout` (`10s`) instead of reusing `defaultShutdownTimeout`
- changed `VerifyBasicAuth` to accept the `Basic` scheme prefix case-insensitively per RFC 7617/7235
- refreshed `.github/copilot-instructions.md` to remove the `anthropic-sdk-go` dependency that was replaced with direct HTTP calls in 1.2.5
- refreshed `.github/copilot-instructions.md` to mark the webhook handlers as functional (no longer WIP)

### Fixed

- fixed `installation_token_exchange.go` to handle `io.ReadAll` errors and to send a `User-Agent` header on the GitHub installation token exchange request, preventing silent body truncation and 403 rejections from GitHub
- fixed `go.mod` to mark `github.com/golang-jwt/jwt/v5` as a direct dependency (it is imported directly by the installation token exchanger)

## [1.2.5] - 2026-04-24

### Changed

- changed the Anthropic backend to call the Messages API over HTTP directly instead of using `anthropic-sdk-go`
- changed the Go module dependencies to their latest versions

### Removed

- removed the `github.com/anthropics/anthropic-sdk-go` dependency

## [1.2.4] - 2026-04-19

### Changed

- changed the Go module dependencies to their latest versions

## [1.2.3] - 2026-04-17

### Changed

- changed the Go module dependencies to their latest versions

## [1.2.2] - 2026-04-16

### Changed

- changed the Go module dependencies to their latest versions

## [1.2.1] - 2026-04-15

### Changed

- changed the Go version to `1.26.2` and updated all module dependencies

### Fixed

- fixed `exhaustive` lint failure by adding `LanguageRuby` to the `languageToRuleCategory` map in `file_classifier.go` after the `langforge` upgrade introduced the new language constant

## [1.2.0] - 2026-04-14

### Added

- added automatic version check on CLI startup using `CheckForUpdates()`

### Changed

- changed the Go module dependencies to their latest versions

## [1.1.0] - 2026-04-03

### Added

- added `FlagBinder` optional interface for controllers to register command-specific flags
- added `self-update` subcommand to update the CLI binary from GitHub releases
- added `SelfUpdaterRepository` interface and `CliforgeSelfUpdaterRepository` implementation following Clean Architecture
- added `version` subcommand to display the current CLI version

### Changed

- changed cliforge import paths to reflect upstream `pkg/` restructuring
- changed the Go module dependencies to their latest versions

## [1.0.3] - 2026-03-31

### Changed

- changed the Go module dependencies to their latest versions

## [1.0.2] - 2026-03-30

### Changed

- changed the Go module dependencies to their latest versions

## [1.0.1] - 2026-03-24

### Changed

- changed the Go module dependencies to their latest versions

## [1.0.0] - 2026-03-23

### Added

- added `--version` flag to the CLI using Cobra's built-in version support
- added `.autobump.yaml` validation for bump-* trivial adapters to verify version files are present
- added `update-go`, `update-node`, `update-python` trivial adapters for dependency update PRs
- added version ldflags injection at build time via `make build` and `make install` targets

### Changed

- **BREAKING CHANGE:** changed `bump-go`, `bump-node`, `bump-python` trivial adapters to detect version bump (release ceremony) PRs instead of dependency updates; users who configured these for dependency updates must switch to `update-go`, `update-node`, `update-python`
- changed `TrivialDetector` interface to use `Detect(ctx, DetectionContext) DetectionResult` instead of `IsTrivial(files) bool` + `Summary(files) string`, enabling three-way verdicts (approve/reject/not-detected)
- changed `TrivialDetectorRegistry.Detect` to return a `DetectionResult` with verdict, enabling bump PR rejection when `.autobump.yaml` validation fails
- changed the Go module dependencies to their latest versions

## [0.2.1] - 2026-03-19

### Changed

- changed the Go module dependencies to their latest versions

## [0.2.0] - 2026-03-17

### Added

- added AI verdict system (`approve`, `request_changes`, `comment`) to review response for merge decisions
- added Anthropic API backend using the official Go SDK (`github.com/anthropics/anthropic-sdk-go`)
- added environment variable configuration fallback (`CODE_GURU_*`) for CI/CD environments
- added shared response parser (`support.ParseReviewResponse`) to eliminate duplicate parsing logic across backends
- added trivial PR detection that skips LLM when CI passes (CI status provided by webhook events; CLI auto-detection pending `gitforge` support)
- added trivial PR detection with built-in adapters (`bump-go`, `bump-node`, `bump-python`, `docs-only`) that skip the LLM

### Changed

- changed `ReviewCommand` to accept a `DetectorRegistry` for trivial PR detection
- changed `ReviewController` to fall back to environment variables when no config file is found
- changed `ReviewResult` entity to include a `Verdict` field for merge eligibility decisions
- changed AI system prompt to include verdict instructions and JSON schema

## [0.1.0] - 2026-03-12

### Added

- added `DiscoverCommand` in domain layer to separate business logic from controller
- added `end_line` and `suggestion` fields to `ReviewComment` for multi-line and code suggestion support
- added `SplitUnifiedDiff` utility for splitting multi-file diffs into per-file chunks
- added Claude Code CLI as an AI backend (alongside OpenAI) with configurable `max_turns`
- added diff fallback in review command for providers without per-file patches (e.g. Azure DevOps)
- added GitHub Actions workflow for CI/CD pipeline
- added glob-based rule matching for precise language/file filtering
- added unit tests for prompt builder, file classifier, URL parser, diff splitter, rules repository, and response parsing
- added YAML `frontmatter` stripping from rule files to extract `paths` globs

### Changed

- changed `DiscoverController` to delegate to `DiscoverCommand` following Clean Architecture
- changed Claude CLI backend to pass user prompt via stdin instead of CLI argument to avoid OS argument length limits
- changed Claude CLI response parsing to handle JSON wrapped in Markdown code fences
- changed OpenAI backend to enforce JSON response format via `ResponseFormat` parameter
- changed system prompt to include strict JSON-only instructions, line number rules, and severity definitions
- changed the Go version to `1.26.1` and updated all module dependencies
- replaced inline `parseGitHubURL` and `parseAzureDevOpsURL` PR URL parsing with `gitforge`'s `ParsePullRequestURL` to consolidate duplicated code
- replaced local `ProviderConfig` struct, `resolveToken()`, and `FindConfigFile()` with `gitforge`'s shared implementations
- replaced local file extension classifier with `langforge`'s `ClassifyFileByExtension` and `ClassifyFilesByExtension` to centralize language abstractions
- replaced raw struct literals in tests with `testkit` builders for consistent test data construction

### Fixed

- fixed `exhaustive` findings by adding missing `Language` and `ServiceType` keys to classifier and URL parser maps
