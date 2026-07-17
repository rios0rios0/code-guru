# Copilot Instructions

## Project Overview

Code Guru is an AI-powered CLI tool written in Go that automatically reviews pull requests.
It supports GitHub and Azure DevOps as Git hosting providers, and Anthropic Messages API, Claude Code CLI, or OpenAI Chat Completions API as AI backends.
Review rules are loaded from configurable Markdown files with optional YAML frontmatter for file-glob filtering.

## Architecture

The project follows **Clean Architecture** with strict layer separation:

- **`cmd/code-guru/`** — Application entry point. Builds the DI container, wires Cobra commands, and starts the CLI.
- **`internal/`** — Internal application wiring (`app.go`, `container.go`) that aggregates controllers.
- **`internal/domain/`** — Core business logic. Contains entity definitions, repository interfaces, and command implementations. This layer has no infrastructure dependencies.
  - `entities/` — Domain models (`FileDiff`, `ReviewComment`, `ReviewResult` (with `ThreadResolutions`), `ReviewRequest`, `ReviewThread` (with gitforge `ThreadID`/`RootCommentID`), `ThreadResolution`, `Rule`, `Settings`, `AuthToken`, `AppVersion`, `Controller`/`FlagBinder` interfaces).
  - `repositories/` — Abstract interfaces (`AIReviewerRepository`, `RulesRepository`, `TrivialDetector`/`TrivialDetectorRegistry`, `PullRequestMetadataRepository`, `TokenRepository`, `SelfUpdaterRepository`).
  - `commands/` — Use-case implementations (`ReviewCommand`, `ReviewAllCommand`, `DiscoverCommand`, `AuthCommand`, `SelfUpdateCommand`, `VersionCommand`).
- **`internal/infrastructure/`** — Concrete implementations of domain interfaces.
  - `repositories/` — AI backend implementations (`anthropic/`, `claude/`, `openai/`), rule loading (`rules/`), trivial PR detectors (`trivial/`), PR metadata fetchers (`prmetadata/`: description + commit count via the GitHub / Azure DevOps REST APIs, dispatched by provider name), OAuth token storage (`auth/`), self-updater (`selfupdate/`). A `container.go` at this level provides `AIReviewerFactory` and `RulesRepositoryFactory` for settings-driven backend selection. The factory wraps the chosen backend in a `RetryingAIReviewer` decorator (`retrying_ai_reviewer.go`) that re-samples on a non-JSON / unparseable or transient-error response up to `ai.max_attempts` times (reinforcing the JSON-only instruction via `ReviewRequest.Attempt`), so a recoverable blip does not fail the review or surface the raw model output on the PR. A context-window overflow (`support.ErrContextWindowExceeded`, raised by every backend when the diff is too large for the model) and a content-safety refusal (`support.ErrContentSafetyRefusal`, raised when the model's safety classifiers decline the content — Anthropic `stop_reason: "refusal"`, OpenAI `content_filter`) are both deterministic and returned immediately without retry, and the command layer posts a dedicated annotation for each (respectively "PR too large" with the change's scale + split-the-PR guidance, and "content-safety declined" with the policy category + request-a-human/switch-model guidance) instead of the generic failure notice. The Anthropic backend can re-issue the review once against `ai.anthropic.refusal_fallback_model` when the primary model refuses.
  - `controllers/` — Cobra CLI controllers (review, review-all, discover, auth, serve, health, self-update, version) that bridge CLI input to domain commands.
  - `controllers/webhooks/` — HTTP webhook dispatcher (`dispatcher.go`) with auth (`auth.go`: HMAC-SHA256 + Basic Auth), per-vendor handlers (`github.go`, `azuredevops.go`), webhook dedup (`dedup_cache.go`: per-pod TTL cache; `dedup_lease.go`: K8s Lease cross-pod dedup), CIDR allowlist (`source_ip.go`), ADO skinny-payload hydration (`azuredevops_hydrator.go`), GitHub App installation token exchange (`installation_token_exchange.go`: RS256 JWT, `sync.Map` cache), and a bounded async worker pool (`worker.go`).
- **`internal/support/`** — Shared utility functions: URL parsing, diff splitting, file classification, prompt building, response parsing, conversation assembly (prior bot review threads for re-review context), bot-identity recognition for the re-review walk (`IsBotAuthor` honours configured `bot_identities` plus `DetectBotAuthors` self-detection from the bot's own PR-wide annotations, so it works when the deployment posts under a service account), review markers and mention detection, verdict-to-native-review mapping, byte-bounded text truncation.
- **`test/domain/doubles/`** — Test doubles (stubs) for domain repository interfaces.
- **`test/infrastructure/doubles/`** — Test doubles for infrastructure-only types (e.g., webhook `Submitter`, `GitHubTokenizer`, `WebhookDedup`).
- **`configs/`** — Example YAML configuration files.

## Dependency Injection

The project uses **Uber DIG** (`go.uber.org/dig`) for dependency injection. Each package exposes a `RegisterProviders(container *dig.Container) error` function in a `container.go` file. Providers are registered bottom-up: repositories → entities → commands → controllers → app.

## CLI Framework

The CLI is built with **Cobra** (`github.com/spf13/cobra`). Controllers implement the `entities.Controller` interface:

```go
type Controller interface {
    GetBind() ControllerBind
    Execute(command *cobra.Command, arguments []string)
}
```

Controllers are automatically registered as subcommands via the DI container.

## Go Conventions

- **Go version**: 1.26+ (see `go.mod`).
- **Formatting**: Use `gofmt`; tabs for indentation (see `.editorconfig`).
- **Imports**: Group into standard library, external dependencies, and internal packages (separated by blank lines). Alias the `logrus` logger as `logger` and `gitforge` packages with `forge` prefixes.
- **Error handling**: Always return errors explicitly. Wrap errors with `fmt.Errorf("context: %w", err)` for context propagation.
- **Naming**: PascalCase for exported identifiers. Use descriptive suffixes: `Repository`, `Command`, `Controller`, `Config`, `Factory`. Private helpers use camelCase.
- **Comments**: Every exported type and function must have a GoDoc comment starting with the identifier name. Keep comments concise and descriptive of intent.
- **Nolint directives**: Use `//nolint:exhaustruct` when intentionally initializing structs with only required fields. Always include a justification comment.

## Testing

- **Build tags**: All unit tests must include `//go:build unit` as the first line. Run tests with `go test -tags unit ./...`.
- **Package naming**: Test files use the `_test` suffix on the package name (e.g., `package support_test`).
- **Framework**: Use `github.com/stretchr/testify/assert` and `github.com/stretchr/testify/require` for assertions.
- **Structure**: Follow Given-When-Then with `// given`, `// when`, `// then` comments. Use table-driven tests with `t.Run()` subtests. Call `t.Parallel()` at the top of test functions.
- **Test doubles**: Place stubs in `test/domain/doubles/repositories/` for domain-contract stubs and `test/infrastructure/doubles/repositories/` for stubs that double infrastructure-only types. Name them with the `Stub` prefix (e.g., `StubAIReviewerRepository`). Stubs store the last request and return canned responses. Keep the layer the stub references on the same side of the import boundary.

## Logging

Use `github.com/sirupsen/logrus` aliased as `logger`. Use structured log levels: `logger.Infof`, `logger.Errorf`, `logger.Debugf`, `logger.Warnf`.

## Configuration

Settings are loaded from YAML files discovered automatically by `FindConfigFile`. Searched locations (in order): `.`, `.config`, `configs`, `~`, `~/.config`. Accepted filenames: `.code-guru.yaml`, `.code-guru.yml`, `code-guru.yaml`, `code-guru.yml`. Pass an explicit path with `-c/--config` to override discovery.

Token fields support three resolution strategies in order: **environment variable** (`${VAR_NAME}`), **file path** (contents read if resolved string is a valid file), and **inline** (literal string).

Key config sections:

- `providers[]` — list of Git hosting providers (`type: github|azuredevops`, `token`, `organizations[]`).
- `ai.backend` — required; `openai`, `claude`, or `anthropic`.
- `ai.openai` — `api_key`, `model` (e.g. `gpt-4o`). `api_key` is required when backend is `openai`.
- `ai.anthropic` — `api_key`, `model` (default `claude-sonnet-4-20250514`), `context_1m` (tri-state, default on: sends the `context-1m-2025-08-07` beta for the 1M-token window; env `CODE_GURU_ANTHROPIC_CONTEXT_1M`, resolve via `AnthropicConfig.Context1MEnabled()`), `refusal_fallback_model` (env `CODE_GURU_ANTHROPIC_REFUSAL_FALLBACK_MODEL`, default empty/off: the model re-issued against on a content-safety `stop_reason: "refusal"`). `api_key` is required when backend is `anthropic`.
- `ai.claude` — `binary_path` (default `claude`), `model` (default `sonnet`), `max_turns` (default `1`).
- `ai.max_attempts` — AI retry budget per review (env `CODE_GURU_AI_MAX_ATTEMPTS`, default `3`; `1` disables retries). Resolve via `AIConfig.ReviewAttempts()`.
- `ai.project_guidelines` — tri-state; when enabled (default), the reviewed repository's own root `CLAUDE.md` is fetched via the provider's file-access API and forwarded to the LLM as project-specific review context (env `CODE_GURU_AI_PROJECT_GUIDELINES`). Resolve via `AIConfig.ProjectGuidelinesEnabled()`; never dereference the pointer directly.
- `ai.pr_metadata` — tri-state; when enabled (default), the PR's description and commit count are fetched from the provider's REST API and forwarded to the LLM as intent context (env `CODE_GURU_AI_PR_METADATA`). Resolve via `AIConfig.PullRequestMetadataEnabled()`; never dereference the pointer directly.
- `rules.path` — directory containing Markdown rule files (supports `${VAR}` expansion).
- `rules.categories` — optional allow-list of rule categories to load; empty means load all.
- `server.port` — webhook server port (default `8080`).
- `server.webhook_secret` — HMAC secret for verifying webhook payloads.
- `github_app.app_id` — GitHub App ID for webhook authentication.
- `github_app.private_key` — GitHub App private key.
- `bot_identities` — comma-separated account identities code-guru posts under (env `CODE_GURU_BOT_IDENTITIES`), so re-reviews recognise prior bot threads under a service account. The built-in `code-guru` name shapes — `code-guru[bot]` (GitHub App), `code-guru@<tenant>` (Azure DevOps), and bare `code-guru` — plus self-detected identities are always recognised, so this is only needed for service accounts that don't follow those shapes.

Validate required fields in `validateSettings`.

## Rules

Rules are Markdown files stored in the directory specified by `rules.path`. Each file becomes one rule, using its filename (without `.md`) as both its name and category.

**Frontmatter**: A rule file may start with a YAML frontmatter block delimited by `---`. The only supported frontmatter key is `paths`, a list of glob patterns restricting the rule to specific changed files:

```markdown
---
paths:
  - "**/*.go"
---
# Go Conventions
...
```

**Category filtering**: `FilesystemRulesRepository.LoadForLanguages` always includes rules in the following *universal* categories regardless of detected languages: `architecture`, `ci-cd`, `code-style`, `design-patterns`, `documentation`, `git-flow`, `security`, `testing`. Language-specific rules are included when their category matches a detected language, or when their `paths` globs match the changed files.

**Project guidelines**: On top of the configured rules, the review command loads the reviewed repository's own root `CLAUDE.md` (`internal/domain/commands/project_guidelines.go`) and forwards it on `ReviewRequest.ProjectGuidelines`; `support.BuildUserPromptFor` renders it into the user prompt as a fenced, escape-proofed documentation block. The fetch is skipped when the PR itself modifies `CLAUDE.md`, is best-effort (missing file or provider error never fails the review), and bounds content to 32 KiB.

**Pull request metadata**: The review command also loads the PR's description and commit count (`internal/domain/commands/pull_request_metadata.go`, via the `prmetadata` fetcher registry) and forwards them on `ReviewRequest.Metadata`; the prompt builder renders them — together with the title and branch names already in the PR header — as an intent-context section instructing the model to verify the diff against the stated intent and flag scope creep. Best-effort (an unsupported provider or API error never fails the review); the description is bounded to 16 KiB and escape-proofed like every other untrusted block.

## CLI Usage

Available subcommands:

| Command        | Description                                              |
|----------------|----------------------------------------------------------|
| `review <url>` | Review a single PR by URL (GitHub or Azure DevOps)       |
| `review-all`   | Batch-review all open PRs across configured providers    |
| `discover`     | Discover repos and list open PRs without posting reviews |
| `auth`         | OAuth login/logout/status (login flow WIP)               |
| `serve`        | Start webhook server for automatic PR review             |
| `self-update`  | Update the CLI binary to the latest version              |
| `version`      | Print the current CLI version                            |

Common flags (all commands):

| Flag              | Description                                     |
|-------------------|-------------------------------------------------|
| `-c, --config`    | Path to config file (default: auto-discover)    |
| `-v, --verbose`   | Enable debug logging                            |
| `--dry-run`       | Perform review without posting comments (`review-all` only) |

## Build and CI

- **Build**: `go build -o bin/code-guru ./cmd/code-guru/`
- **Test**: `go test -tags unit ./...`
- **CI**: GitHub Actions workflow in `.github/workflows/default.yaml` using reusable pipelines from `rios0rios0/pipelines`. Includes golangci-lint, CodeQL SAST, and SonarCloud quality gates.
- **Makefile**: Includes external makefiles from `rios0rios0/pipelines` for common and Go-specific targets.

## Dependencies

Only add new dependencies when strictly necessary. Prefer the standard library. Current key dependencies:

- `github.com/rios0rios0/cliforge` — CLI utilities and self-update support
- `github.com/rios0rios0/gitforge` — Multi-provider Git abstraction (consumed as a published pseudo-version; no local `replace` directive)
- `github.com/rios0rios0/langforge` — Language classification by file extension
- `github.com/rios0rios0/testkit` — Test builder base utilities
- `github.com/sashabaranov/go-openai` — OpenAI API client
- `github.com/sirupsen/logrus` — Structured logging
- `github.com/spf13/cobra` — CLI framework
- `github.com/stretchr/testify` — Testing assertions
- `go.uber.org/dig` — Dependency injection
- `gopkg.in/yaml.v3` — YAML parsing

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the development workflow and the [Development Guide](https://github.com/rios0rios0/guide/wiki) for coding standards.
