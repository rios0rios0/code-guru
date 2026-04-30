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

## [1.4.1] - 2026-04-30

### Changed

- changed `support.ParseReviewResponse` so it no longer falls back to `&ReviewResult{Summary: content}` on a parse failure — that fallback is what allowed the malformed-JSON dump described above to reach the PR. Callers that depended on the previous "always returns a result" contract (the three AI reviewer repositories — `claude`, `openai`, `anthropic`) propagate the new error up through `ReviewDiff`; the worker layer already logs and swallows reviewer errors, so a parse failure now manifests as a log line and an absent comment rather than a noisy thread
- changed the Go module dependencies to their latest versions

### Fixed

- fixed `code-guru` posting the raw model output as a single PR-wide thread when the AI returned malformed JSON; observed on `backend/authenticator#12027` thread `71418` where the model emitted `"body":"... Rule: Go Logging — "Always use \`WithFields\` ..."."` with unescaped `"` characters inside the string value. `json.Unmarshal` rejected it, the markdown-fence regex missed (the model honoured the "no fences" instruction), and `ParseReviewResponse` defaulted to `Verdict="comment"` plus `Summary=raw response` — which `postComments` then dumped onto the PR as a 3.5 KB JSON blob. Added a `repairJSONStrings` state-machine pass that escapes any `"` whose lookahead is not a JSON structural token (`,`, `:`, `}`, `]`, or end of input); valid JSON round-trips unchanged. On total parse failure the parser now logs the raw content (truncated to `4096` bytes) at `ERROR` and returns `support.ErrUnparseableResponse` so the worker logs the failure and posts nothing — instead of fabricating a comment from the broken response

## [1.4.0] - 2026-04-29

### Added

- added `clientIP(*http.Request)` and `sourceIPAllowed(ip, prefixes)` helpers in `internal/infrastructure/controllers/webhooks/source_ip.go`, plus a `Dispatcher.enforceSourceIPAllowlist` middleware-style helper that both webhook handlers call as their first guard
- added `deliver_docker: true` to `.github/workflows/default.yaml` so future tag pushes automatically build and publish the Docker image to `ghcr.io/rios0rios0/code-guru` alongside the binary release; previously every image bump required a manual `docker build && docker push` (see the `0.2.0` rollout for the toolbox stack)
- added `packages: 'write'` to the workflow `permissions:` block so the `delivery-docker` job can authenticate to GHCR; reusable workflows cannot escalate beyond the caller's grants, so the permission has to be declared at the caller level
- added `Server.AllowedSourceCIDRs` (env: `CODE_GURU_SERVER_ALLOWED_SOURCE_CIDRS`) — a comma-separated CIDR allowlist enforced on `/webhooks/azuredevops` and `/webhooks/github` before any auth check; the source IP is read from `CF-Connecting-IP`, then `X-Real-IP`, then the leftmost `X-Forwarded-For` entry, then `RemoteAddr` (in that order); empty list means "no allowlist", preserving existing behaviour. CIDRs are parsed once at dispatcher construction so the per-request hot path has no parsing cost; invalid entries are logged and skipped

### Changed

- changed `BuildSystemPrompt` to fall back to a general best-practices system prompt when no rules are loaded; the previous template embedded an empty `Rules to enforce` block plus the instruction `Do NOT comment on style preferences not covered by the rules`, which made the LLM correctly produce zero comments on every PR when `CODE_GURU_RULES_PATH` was unset or no rules matched the file languages — the no-rules path now asks the model to review for bugs, security issues, performance problems, and clear correctness violations without referencing a non-existent rule set
- changed both system prompt templates to include `"verdict": "approve"` in the no-issues example so the LLM does not omit the field on clean reviews; without this, `ParseReviewResponse` defaulted to `comment` and downstream automation could never reach a clean `approve` verdict
- changed the `Dockerfile` `SHELL` directive from `["/bin/bash", "-c"]` to `["/bin/bash", "-eo", "pipefail", "-c"]` so `pipefail` is enforced at the shell level for every `RUN` (the inline `set -euxo pipefail` becomes redundant defense in depth) — fixes hadolint `DL4006` triggered by the `claude --version | tee /etc/claude-version` pipe added in `1.4.0`
- changed the Go module dependencies to their latest versions

### Fixed

- fixed `default / delivery > docker` job failing on `main` with `ERROR: failed to build: resolve : lstat .ci: no such file or directory` after `rios0rios0/pipelines` commit `c9553e2` (`hotfix(moved): moved Dockerfile of original position`) renamed the convention to `.ci/stages/40-delivery/app.Dockerfile`; relocated the existing `Dockerfile` to that path. Build context stays at the repo root so all `COPY` directives resolve unchanged
- fixed `Repository ID is empty, falling back to repository name for API calls` warning emitted by the gitforge Azure DevOps provider on every webhook delivery; the ADO `git.pullrequest.created` / `updated` payload includes the repository UUID at `resource.repository.id` but the handler was not extracting it. Added `ID` to the `adoRepository` struct and now passes `forgeEntities.Repository{ID: ...}`. The fallback-to-name path still works when the handler receives a payload with a missing or empty `resource.repository.id`
- fixed Azure DevOps PR reviews ending with a bot comment that says "no diff" even when the PR has real changes; `ReviewCommand.buildDiffs` was looking up unified-diff chunks by `diffs[i].Path` (e.g. `/README.md`, the leading slash that `gitforge`'s ADO `GetPullRequestFiles` returns) while `support.SplitUnifiedDiff` keys chunks by the bare new-side path (`README.md`, parsed from `diff --git a/X b/X`). The lookup missed for every ADO file, leaving the diff body empty under each `### File:` header and tricking the LLM into reporting "no diff to review". Centralised the normalisation in a new `support.LookupChunkByPath(chunks, path)` helper so the leading slash is stripped at exactly one site, with a regression test exercising both the bare and the leading-slash shapes

## [1.3.0] - 2026-04-28

### Added

- added `git.pullrequest.created` and `git.pullrequest.updated` handler that builds an `azuredevops` `ReviewProvider` and enqueues active PRs for asynchronous review
- added `Server.AllowedOrganizations` and `Server.AllowedProjects` allowlists (defense-in-depth) consulted by both webhook handlers, returning `403 Forbidden` for off-list payloads
- added a `Dockerfile` (multi-stage `golang:1.26-alpine` builder, `gcr.io/distroless/static-debian12:nonroot` runtime, `EXPOSE 8080`) and a `.dockerignore`
- added a `health` subcommand (`code-guru health`) that probes a running `serve` listener and exits `0` on `200`, `1` otherwise; used by the `Dockerfile` `HEALTHCHECK` directive (the distroless base image has no shell, no `curl`, no `wget`, so the binary doubles as its own healthcheck client)
- added a `HEALTHCHECK` directive to the `Dockerfile` calling `code-guru health` with a 30s interval and a 10s start period, so `docker run` / `compose` deployments get a live readiness signal without requiring a Kubernetes probe
- added a bounded asynchronous worker `Pool` (configurable `Workers` and `QueueSize`) that drains review jobs and recovers from per-job panics so a single failure does not crash the worker
- added Basic Auth verification for the Azure DevOps Service Hook endpoint (constant `code-guru` username, configurable secret password)
- added GitHub `pull_request` (`opened`, `synchronize`, `reopened`) handler with GitHub App installation token exchange (RS256 JWT, `sync.Map`-backed cache with a 5-minute safety margin) and a configured PAT fallback
- added graceful shutdown to the `serve` controller, capturing `SIGINT`/`SIGTERM` and draining both the HTTP server and the worker pool within `Server.ShutdownTimeout`
- added HMAC-SHA256 verification for the GitHub `pull_request` webhook endpoint via the `X-Hub-Signature-256` header

### Changed

- changed `Dispatcher.findToken` to fall back to a single untyped provider entry, so the env-only configuration (`CODE_GURU_PROVIDER_TOKEN`) works for both GitHub and Azure DevOps webhook handlers
- changed `NewSettings` to also resolve `${ENV_VAR}`/file-path references for `server.webhook_secret` and `github_app.private_key`, so YAML literals like `${CODE_GURU_WEBHOOK_SECRET}` are expanded before reaching the auth/JWT code paths
- changed `Pool.Submit` to hold the same mutex used by `Shutdown` while sending on the queue, eliminating the TOCTOU race that could panic with `send on closed channel` under concurrent traffic and graceful shutdown
- changed `Pool` workers to receive a cancellable base context that `Shutdown` cancels, so in-flight `JobHandler` invocations can observe shutdown timeouts via the `ctx` argument
- changed `ServeController.Execute` to validate required settings (`ai.backend`, `server.webhook_secret`) up front and exit fatally instead of starting with the empty `Settings` fallback
- changed `VerifyBasicAuth` to accept the `Basic` scheme prefix case-insensitively per RFC 7617/7235
- changed the `--port` flag on `serve` to be properly registered via `BindFlags` (it previously read but never declared the flag)
- changed the `Dockerfile` `RUN` shell to `/bin/bash` and added `set -euxo pipefail` so download-pipeline failures cannot be masked by `sh`/`dash` semantics
- changed the `Dockerfile` runtime stage from `gcr.io/distroless/static-debian12:nonroot` to `debian:12-slim@sha256:f9c6a2fd2ddbc23e336b6257a5245e31f996953ef06cd13a59fa0a1df2d5c252` so the `claude` AI backend can exec the Claude Code CLI inside the container; the native binary is installed via `claude.ai/install.sh stable` on every image rebuild (intentionally not version-pinned so security fixes ship without a manual bump; the `stable` channel is passed explicitly so the image is insulated from a future change to the installer default; downloaded to a file, then executed — no `curl | bash` pipe; the resolved version is written to `/etc/claude-version` for runtime traceability)
- changed the `serve` controller to register the dispatcher and itself in the DIG container so all subcommands ship with one binary
- changed the HTTP server's `ReadHeaderTimeout` to a dedicated `defaultReadHeaderTimeout` (`10s`) instead of reusing `defaultShutdownTimeout`
- refreshed `.github/copilot-instructions.md` to mark the webhook handlers as functional (no longer WIP)
- refreshed `.github/copilot-instructions.md` to remove the `anthropic-sdk-go` dependency that was replaced with direct HTTP calls in 1.2.5

### Fixed

- fixed `go.mod` to mark `github.com/golang-jwt/jwt/v5` as a direct dependency (it is imported directly by the installation token exchanger)
- fixed `installation_token_exchange.go` to handle `io.ReadAll` errors and to send a `User-Agent` header on the GitHub installation token exchange request, preventing silent body truncation and 403 rejections from GitHub

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
