# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Project Does

Code Guru is a Go CLI tool that uses AI (Anthropic Messages API, Claude Code CLI, or OpenAI Chat Completions API) to automatically review pull requests across GitHub and Azure DevOps. It loads review rules from Markdown files, loads the reviewed repository's own `CLAUDE.md` as project-specific context, sends PR diffs to an AI backend, and posts review comments back on the PR. It also supports trivial PR detection (dependency updates, version bumps, docs-only changes) to auto-approve or reject without calling the LLM, and a long-running webhook server mode for automatic reviews.

**Module path is `github.com/rios0rios0/codeguru` (no hyphen)** — the repo directory, binary, and config name are `code-guru` (hyphenated). Import paths always use `codeguru`.

## Build, Test, and Lint

```bash
make lint          # Run golangci-lint (via pipelines repo config — always use this, never the binary directly)
make test          # Run all tests (via pipelines repo)
make sast          # Run full SAST security suite (CodeQL, Semgrep, Trivy, Hadolint, Gitleaks)

make build                                      # Build with version ldflags into bin/code-guru
go build -o bin/code-guru ./cmd/code-guru/      # Direct build
go test -tags unit ./...                        # Run unit tests directly
go test -tags unit -run TestFunctionName ./internal/support/  # Run a single test
```

The Makefile imports targets from `~/Development/github.com/rios0rios0/pipelines` (`SCRIPTS_DIR`). There is no local `.golangci.yaml` — lint config comes from the pipelines repo (79 linters, including `funlen` at 100 lines, `golines` at 120 chars). CI runs `.github/workflows/default.yaml` via the reusable `rios0rios0/pipelines` Go workflow (lint, CodeQL, SonarCloud, Docker delivery to `ghcr.io/rios0rios0/code-guru`). `claude-code-review.yaml` auto-reviews PRs; `claude.yaml` answers `@claude` mentions.

## Architecture

Clean Architecture with domain/infrastructure separation, using Uber DIG for dependency injection and Cobra for the CLI.

### Flows

**Review flow (single PR):** CLI input → `ReviewController` (parses PR URL, loads settings, builds provider from gitforge registry) → `ReviewCommand.Execute` → skip gates (draft unless `ai.review_drafts`; review-once unless `@code-guru` mentioned) → `GetPullRequestFiles` → trivial detection → post "reviewing" marker → build diffs (per-file patches, fallback to full unified diff split for ADO) → classify languages (langforge) → load rules → load project guidelines (reviewed repo's `CLAUDE.md`) → AI backend `ReviewDiff` → closed-PR re-check → post inline/PR-wide comments (with stale/duplicate/resolved-anchor filters) → completion annotation → native review submission.

**Trivial PR flow:** After file listing, `TrivialDetectorRegistry.Detect` runs each enabled adapter; on a match the LLM is skipped entirely and the verdict (approve/reject) posts immediately, optionally auto-merging (see gates below).

**Re-review (mention) flow:** `@code-guru` mention webhook → Worker → `ReviewCommand` with `UserMentioned=true` → fetch prior bot inline threads via `ListPullRequestComments` → assemble `ReviewThread[]` (carrying gitforge `ThreadID`) → AI prompt requires per-thread `thread_resolutions` decisions (`resolved`/`outstanding`/`outdated`) BEFORE any new `comments` → `applyThreadResolutions` posts one reply **nested inside each prior thread** via `postResolutionReply` (gitforge `ReplyToThread` when a `ThreadID` exists, fresh inline comment otherwise) + `UpdatePullRequestThreadStatus("fixed")` on resolved / `"closed"` on outdated → `dropResolvedAnchorComments` strips new comments whose anchor was already addressed, so the same finding never lands twice. First-pass reviews leave `ThreadResolutions` empty so the prompt and post-pipeline behave exactly as before.

**Project guidelines flow:** `ReviewCommand.loadProjectGuidelines` (`internal/domain/commands/project_guidelines.go`) fetches the reviewed repository's root `CLAUDE.md` via gitforge's `FileAccessProvider` (works on GitHub and Azure DevOps) and sets `ReviewRequest.ProjectGuidelines`; `support.BuildUserPromptFor` renders it as a fenced, escape-proofed documentation block between the PR header and the conversation/diff. Skipped when the PR itself modifies `CLAUDE.md` (the diff already shows it), when the operator set `ai.project_guidelines: false`, or when the provider lacks file access. Best-effort: a missing file or fetch error logs at debug and the review proceeds; content is trimmed and bounded to 32 KiB with a truncation sentinel; the fetch has a 10s timeout.

**Webhook (serve) flow:** `POST /webhooks/github` (HMAC-SHA256) or `/webhooks/azuredevops` (HTTP Basic, username hardcoded `code-guru`) → source-IP CIDR allowlist → payload parsing + org/project allowlists → dedup (per-pod TTL cache, or cross-pod K8s Lease when in-cluster) → bounded worker pool → `Dispatcher.HandlePR` (fresh `ReviewCommand` per job). ADO org-wide subscriptions send "skinny" payloads (`{url, pullRequestId}` only) that are REST-hydrated with an SSRF guard (https + `dev.azure.com`/`*.visualstudio.com` only). GitHub App auth exchanges an RS256 JWT for cached installation tokens.

### Entry Points & DI

- `cmd/code-guru/main.go` — the only real entry point (`cmd/azfunc` and `cmd/lambda` are `panic()` stubs). Builds the root Cobra command; `PersistentPreRun` checks for updates.
- `cmd/code-guru/dig.go` — builds **three separate `dig.New()` containers** (`injectAppContext`, `injectReviewController`, `injectSelfUpdater`); there is no shared graph.
- Each layer has a `container.go` with `RegisterProviders(*dig.Container) error`. Registration order: repositories → entities → commands → controllers → app (`internal/container.go`).
- `entities.provideSettings` auto-discovers the config file, falls back to env-only settings, and degrades to an empty `&Settings{}` with a warning — only `serve` hard-validates settings.

### Domain Layer (`internal/domain/`)

- `entities/` — Framework-agnostic domain models:
  - `settings.go` — `Settings` with `AIConfig` (tri-state `SubmitNativeReview`/`ProjectGuidelines` pointers resolved via `NativeReviewSubmissionEnabled()`/`ProjectGuidelinesEnabled()`, both default ON; `ReviewAttempts()` defaults to 3), `RulesConfig`, `TrivialConfig`, `ServerConfig`, `GitHubAppConfig`, `BotIdentities`. `NewSettings` (YAML + env overrides) and `NewSettingsFromEnv` (env-only).
  - `review.go` — `ReviewRequest` (with `Conversation`, `Attempt`, `ProjectGuidelines`), `ReviewResult` (with `ThreadResolutions`), `ReviewComment`, `ReviewThread` (with `ThreadID`/`RootCommentID`), `ThreadResolution`, `ReviewMessage`, `FileDiff`, `Rule`.
  - `controller.go` — `Controller`/`FlagBinder` interfaces; `auth.go` — `AuthToken`; `version.go` — `AppVersion`.
- `repositories/` — Interfaces only: `AIReviewerRepository` (`Name`, `ReviewDiff`), `RulesRepository` (`LoadAll`, `LoadForLanguages`), `TrivialDetector` (`Name`, `Detect(ctx, DetectionContext) DetectionResult`) + `TrivialDetectorRegistry` + `FileContentFetcher`, `TokenRepository`, `SelfUpdaterRepository`.
- `commands/` — Business logic returning values directly (no listener pattern): `ReviewCommand` (the 1,700-line heart: skip gates, trivial detection, marker/annotation posting, conversation walk, thread resolutions, comment filters, auto-merge), `project_guidelines.go` (CLAUDE.md loader), `ReviewAllCommand` (batch), `DiscoverCommand`, `AuthCommand` (login is a TODO stub, **not DI-registered**), `SelfUpdateCommand`, `VersionCommand`.

### Infrastructure Layer (`internal/infrastructure/`)

- `controllers/` — Cobra controllers implementing `entities.Controller`: review (root command with PR URL), review-all (**passes a nil detector registry — no trivial detection in batch mode**), discover, serve, health (Docker healthcheck probe), self-update, version. `auth_controller.go` exists but is **not registered** in any container.
- `controllers/webhooks/` — `auth.go` (HMAC-SHA256 + Basic helpers, constant-time), `github.go` / `azuredevops.go` (vendor handlers; both mention paths skip the bot's own comments to break infinite review loops), `dispatcher.go` (job wiring, dedup keys `gh:owner/repo:pr` / `ado:repoID:pr`), `dedup_cache.go` (30s TTL per-pod) + `dedup_lease.go` (K8s Lease cross-pod; needs RBAC on `coordination.k8s.io/leases`; renews every 30s against a 60s lease), `source_ip.go` (CIDR allowlist; header precedence CF-Connecting-IP → X-Real-IP → X-Forwarded-For → RemoteAddr), `azuredevops_hydrator.go` (skinny-payload REST hydration + SSRF host allowlist), `installation_token_exchange.go` (GitHub App JWT → installation token, `sync.Map` cache, 5m safety margin), `worker.go` (bounded pool, panic-recovering workers, graceful drain).
- `repositories/anthropic/` — direct `net/http` to `/v1/messages` (120s timeout, 10MB response cap, `WithEndpoint` test seam).
- `repositories/claude/` — shells out to `claude --print --output-format json --system-prompt <...>` with the user prompt on **stdin**; captures both stdout and stderr on failure.
- `repositories/openai/` — `sashabaranov/go-openai`, temperature 0.2, JSON response format.
- `repositories/rules/` — loads `*.md` from `rules.path`; frontmatter supports only `paths:` globs; filename (sans `.md`) is both rule name and category.
- `repositories/trivial/` — detectors: `update-go`, `update-node`, `update-python` (dependency updates), `bump-go`, `bump-node`, `bump-python` (version bumps validated against `.autobump.yaml` via `FileContentFetcher` — missing required files ⇒ **reject**), `docs-only`. A `CHANGELOG.md`-only change is classified as a version bump and claimed **exclusively** by the `bump-*` detectors (shared `isChangelogOnly` guard in `registry.go`); `docs-only`/`update-*` decline it, so disabling the `bump-*` adapters reliably keeps version bumps out of trivial auto-merge.
- `repositories/trivial/autobump/` — `.autobump.yaml` parser; `{project_name}` placeholder resolves to the repo name.
- `repositories/auth/` — filesystem token storage (`~/.config/code-guru/auth.json`, 0600) — implemented but **not DI-registered**.
- `repositories/selfupdate/` — cliforge-based binary self-updater.
- `repositories/container.go` — `AIReviewerFactory` (settings-driven backend selection; **unknown backend silently falls back to the Claude CLI**) wrapping every backend in the `RetryingAIReviewer` decorator (`WithRetry`): re-samples on non-JSON/transient errors up to `ai.max_attempts` times, setting `ReviewRequest.Attempt` so the prompt reinforces JSON-only output on retries; the raw model output never reaches the PR. Also `RulesRepositoryFactory`.

### Support Package (`internal/support/`)

Shared utilities: `diff_splitter.go` (parse unified diffs; `LookupChunkByPath` normalises the ADO leading `/`), `file_classifier.go` (langforge-based language → rule-category mapping), `url_parser.go` (PR URL parsing via gitforge), `prompt_builder.go` (system + user prompt assembly — see invariants below), `response_parser.go` (4-step JSON parse: strict → fenced block → string repair → `ErrUnparseableResponse`; verdict normalised to `approve`/`request_changes`/`comment`), `conversation.go` (prior bot thread walk; `IsBotAuthor` matches configured `bot_identities` + strict `code-guru` prefix shapes), `review_marker.go` (completion markers, `@code-guru` mention detection, `DetectBotAuthors` self-detection), `verdict_mapper.go` (AI verdict → native review submission), `truncate.go` (byte-bounded truncation; `TruncateForLog` is log-injection-safe).

## Configuration

YAML config discovered via gitforge `FindConfigFile("code-guru")` in `.`, `.config`, `configs`, `~`, `~/.config` (filenames `.code-guru.yaml`, `.code-guru.yml`, `code-guru.yaml`, `code-guru.yml`); override with `-c`. Token-like fields (`providers[].token`, `ai.openai.api_key`, `ai.anthropic.api_key`, `server.webhook_secret`, `github_app.private_key`) support `${ENV_VAR}` expansion → file-path contents → inline literal. Without a config file, `NewSettingsFromEnv` builds everything from `CODE_GURU_*` env vars.

| YAML key | Env var | Default / notes |
|---|---|---|
| `ai.backend` | `CODE_GURU_BACKEND` | required: `openai` / `claude` / `anthropic` (env default `openai`) |
| `ai.openai.api_key` / `.model` | `CODE_GURU_OPENAI_API_KEY` / `_MODEL` | model `gpt-4o`; key required for backend `openai` |
| `ai.anthropic.api_key` / `.model` | `CODE_GURU_ANTHROPIC_API_KEY` / `_MODEL` | model `claude-sonnet-4-20250514`; key required for backend `anthropic` |
| `ai.claude.binary_path` / `.model` / `.max_turns` | `CODE_GURU_CLAUDE_BINARY_PATH` / `_MODEL` / `_MAX_TURNS` | `claude` / `sonnet` / `1` |
| `ai.submit_native_review` | `CODE_GURU_AI_SUBMIT_NATIVE_REVIEW` | tri-state, default **true** (`NativeReviewSubmissionEnabled()`) |
| `ai.review_drafts` | `CODE_GURU_AI_REVIEW_DRAFTS` | `false` — drafts skipped by default |
| `ai.max_attempts` | `CODE_GURU_AI_MAX_ATTEMPTS` | `3` via `ReviewAttempts()`; `1` disables retries |
| `ai.project_guidelines` | `CODE_GURU_AI_PROJECT_GUIDELINES` | tri-state, default **true** (`ProjectGuidelinesEnabled()`) — loads the reviewed repo's `CLAUDE.md` |
| `rules.path` / `rules.categories` | `CODE_GURU_RULES_PATH` | universal categories always load: `architecture`, `ci-cd`, `code-style`, `design-patterns`, `documentation`, `git-flow`, `security`, `testing` |
| `trivial.enabled` / `.adapters` | `CODE_GURU_TRIVIAL_ADAPTERS` | env override also flips `enabled=true` |
| `trivial.auto_merge` / `.merge_strategy` / `.bypass_policy` / `.auto_merge_allowed_authors` | `CODE_GURU_TRIVIAL_AUTO_MERGE` / `_MERGE_STRATEGY` / `_BYPASS_POLICIES` / `_AUTO_MERGE_AUTHORS` | all off/empty by default — see auto-merge gates below |
| `server.port` / `.webhook_secret` / `.queue_size` / `.workers` / `.shutdown_timeout` | `CODE_GURU_PORT` / `_WEBHOOK_SECRET` / `_SERVER_QUEUE_SIZE` / `_SERVER_WORKERS` / `_SERVER_SHUTDOWN_TIMEOUT` | `8080` / required for `serve` / `100` / NumCPU / `30s` |
| `server.allowed_organizations` / `.allowed_projects` / `.allowed_source_cidrs` | `CODE_GURU_SERVER_ALLOWED_*` | empty = allow all |
| `github_app.app_id` / `.private_key` | `CODE_GURU_GITHUB_APP_ID` / `_GITHUB_PRIVATE_KEY` | PKCS#1 or PKCS#8 PEM |
| `bot_identities` | `CODE_GURU_BOT_IDENTITIES` | only needed for service accounts not matching the built-in `code-guru` shapes |
| `providers[]` | `CODE_GURU_PROVIDER_TOKEN` | env creates a single untyped catch-all entry |

In the YAML path (`NewSettings`), env vars override YAML only for: trivial settings, `bot_identities`, `ai.max_attempts`, and `ai.project_guidelines`.

## Testing

- Build tag `//go:build unit` required on every test file; run with `go test -tags unit ./...`. Entity builders use `//go:build integration || unit || test`.
- External test packages (e.g., `package support_test`, `package commands_test`).
- BDD structure: `// given`, `// when`, `// then` — all three markers even when a block is empty.
- `t.Parallel()` at the top of every test function and in `t.Run()` subtests; table-driven where it fits.
- `stretchr/testify` `assert`/`require` for assertions. **No mock libraries** — all doubles are hand-rolled.
- Test doubles: `test/domain/doubles/repositories/` for domain-contract stubs (`StubAIReviewerRepository` records `LastRequest`, `StubRulesRepository`, `StubTrivialDetector`, `StubTokenRepository`); `test/infrastructure/doubles/repositories/` for infrastructure-only types (`StubWebhookSubmitter`, `StubGitHubTokenizer`, `StubWebhookDedup`) — keep the stub on the same side of the import boundary as the type it doubles.
- Entity builders in `test/domain/entitybuilders/` (fluent API via `testkit.BaseBuilder`: `NewFileDiffBuilder().WithPath(...).BuildFileDiff()`).
- `export_test.go` files (tagged `unit`) in `commands` and `webhooks` re-export unexported helpers as package-level variables/method values so external test packages can pin contracts without full stub scaffolding — extend these rather than exporting production symbols for tests.
- In-test provider stubs embed the gitforge interface (`forgeEntities.ReviewProvider`) and override only the methods under test; unexpected calls panic by design (see `recordingReviewProvider` / `fileAccessRecordingProvider` in `review_command_test.go`).

## Key Dependencies

- `github.com/rios0rios0/gitforge` — Multi-provider Git abstraction (GitHub, Azure DevOps): providers registry, `ReviewProvider` + `FileAccessProvider` interfaces, config helpers, URL parsing. Consumed as a published pseudo-version; no local `replace`.
- `github.com/rios0rios0/cliforge` — CLI utilities and self-update support
- `github.com/rios0rios0/langforge` — Language classification by file extension
- `github.com/rios0rios0/testkit` — Test builder base utilities
- `go.uber.org/dig` — Dependency injection; `github.com/spf13/cobra` — CLI framework
- `github.com/sirupsen/logrus` — Logging (always aliased as `logger`)
- `k8s.io/client-go` (+ api/apimachinery) — only for the cross-pod webhook dedup Lease backend

## Invariants & Gotchas

- **Prompt no-drift invariant:** first-pass prompts must stay byte-for-byte identical to their historical shape. Every optional prompt section (project guidelines, prior conversation, thread-resolution schema, retry JSON reminder) collapses to nothing when its input is empty. All backends assemble prompts exclusively through `support.BuildSystemPromptFor(request)` and `support.BuildUserPromptFor(request)` — never inline prompt text in a backend.
- **Prompt-injection defence:** untrusted content (comment bodies, repository CLAUDE.md) enters the user prompt only inside fenced blocks passed through `escapeFence` (zero-width-space neutralises ``` runs) with an explicit "SECURITY: … inert data/documentation" framing line.
- **Magic strings** (changing any breaks behaviour across files): `**Code Guru review` (completed/failed marker → review-once gate), `**Code Guru ` (annotation marker → `DetectBotAuthors`), `@code-guru` (`MentionToken`), thread status `"closed"` (`annotationThreadStatus`), Basic-auth username `code-guru`, lease prefix `code-guru-`, `.autobump.yaml`, `{project_name}`.
- **Path normalisation rule:** ADO paths carry a leading `/`; every comparison (chunk lookup, staleness filter, dedup keys, thread anchors, guidelines skip-check) goes through `normalizeFilePath`/`LookupChunkByPath` stripping exactly one leading slash. New comparisons must follow the same rule.
- **Best-effort posture:** marker/annotation posts, native review submission, thread status updates, auto-merge, conversation walk, and the guidelines fetch are UX — they log at warn/debug and never fail the review. Provider calls on these paths are wrapped in short timeouts (5s posts, 10s guidelines fetch).
- **Bot self-recognition is layered:** built-in strict `code-guru` prefix shapes (`code-guru[bot]`, `code-guru@tenant`, bare) + configured `bot_identities` + self-detection from the bot's own PR-wide annotations. Deployments posting under other service accounts must set `CODE_GURU_BOT_IDENTITIES` or re-reviews re-post every finding. Mention handlers skip the bot's own comments to prevent infinite loops.
- **Auto-merge double gate:** triviality decides *eligibility*; `auto_merge_allowed_authors` decides *trust* (case-insensitive author match; empty = any author — dangerous with `bypass_policy`, which also requires the ADO "Bypass policies when completing pull requests" permission or merges 403). GitHub ignores the bypass flag.
- **Verdict vocabulary:** LLM emits `approve`/`request_changes`/`comment`; trivial detectors emit `approve`/`reject`. `support.MapVerdictToReview` maps both to native submissions (`comment` → WaitingForAuthor); `verdictApprove`/`verdictComment` constants are duplicated in `commands` (documented) — keep them in sync with `verdict_mapper.go`.
- **Raw model output never reaches the PR:** failure annotations are content-free classifications; raw output is logged only (`TruncateForLog`), and unparsed responses log a SHA-256 fingerprint at error level with the raw body at debug.
- **K8s cross-pod dedup requires RBAC** on `coordination.k8s.io/leases` (get/list/create/delete/update/patch); without it the bot silently degrades to per-pod dedup and duplicate reviews return with `replicas>1` (ADO fires `created`+`updated` for each PR).
- **Orphans and stubs:** the `auth` command tree and `FilesystemTokenRepository` are implemented but not wired into DI; `cmd/azfunc` and `cmd/lambda` are panic stubs; `review-all` runs without trivial detection (nil registry); `ciPassed` is hardcoded `false` everywhere pending a gitforge check-status API.

## Documentation & Change Control

Every change lands with a `CHANGELOG.md` entry under `[Unreleased]` (Keep a Changelog categories, entries in simple past tense starting lowercase). Update `README.md` when behaviour/configuration changes, and `.github/copilot-instructions.md` + this file when architecture, commands, or workflow change. Releases move `[Unreleased]` into a version heading on a `bump/x.x.x` branch.
