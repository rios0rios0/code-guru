# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Project Does

Code Guru is a Go CLI tool that uses AI (Anthropic API, Claude Code CLI, or OpenAI API) to automatically review pull requests across GitHub and Azure DevOps. It loads review rules from Markdown files, sends PR diffs to an AI backend, and posts review comments back on the PR. It also supports trivial PR detection (dependency updates, version bumps, docs-only changes) to auto-approve or reject without calling the LLM.

## Build, Test, and Lint

```bash
make lint          # Run golangci-lint (via pipelines repo)
make test          # Run all tests (via pipelines repo)
make sast          # Run full SAST security suite

go build -o bin/code-guru ./cmd/code-guru/     # Build binary
go test -tags unit ./...                        # Run unit tests directly
go test -tags unit -run TestFunctionName ./internal/support/  # Run a single test
```

The Makefile imports targets from `~/Development/github.com/rios0rios0/pipelines`. Always use `make lint`/`make test`/`make sast` rather than calling tool binaries directly.

## Architecture

Clean Architecture with domain/infrastructure separation, using Uber DIG for dependency injection and Cobra for the CLI.

**Request flow:** CLI input → Controller → Command → Repository (AI backend) → Post comments via gitforge

**Trivial PR flow:** CLI input → Controller → Command → DetectorRegistry (no AI) → Post approval/rejection comment via gitforge

### Domain Layer (`internal/domain/`)
- `entities/` — Framework-agnostic domain models: `Settings` (with `ServerConfig`, `GitHubAppConfig`), `ReviewRequest`, `ReviewResult`, `ReviewComment`, `FileDiff`, `Rule`, `AuthToken`, `AppVersion`, `Controller`/`FlagBinder` interfaces
- `repositories/` — Interfaces only: `AIReviewerRepository` (AI engine contract), `RulesRepository` (rule loading contract), `TrivialDetector` + `TrivialDetectorRegistry` (trivial PR detection with `DetectionContext`/`DetectionResult`/`FileContentFetcher`), `TokenRepository` (OAuth token storage), `SelfUpdaterRepository` (binary self-update)
- `commands/` — Business logic: `ReviewCommand` (single PR with trivial detection), `ReviewAllCommand` (batch), `DiscoverCommand` (list PRs), `AuthCommand` (OAuth login/logout/status), `SelfUpdateCommand`, `VersionCommand`

### Infrastructure Layer (`internal/infrastructure/`)
- `controllers/` — Cobra CLI controllers implementing `entities.Controller`: review, review-all, discover, auth, serve, self-update, version
- `controllers/webhooks/` — Functional HTTP webhook stack: `auth.go` (HMAC-SHA256 + HTTP Basic helpers), `github.go` and `azuredevops.go` (vendor handlers that parse payloads and enqueue jobs), `installation_token_exchange.go` (GitHub App JWT/installation-token exchanger with cache), `worker.go` (bounded async worker pool with graceful drain)
- `repositories/anthropic/` — Anthropic Messages API backend (direct `net/http` calls)
- `repositories/claude/` — Claude Code CLI backend (invokes `claude --print`)
- `repositories/openai/` — OpenAI Chat Completions API backend
- `repositories/rules/` — Loads Markdown rule files from filesystem with YAML frontmatter glob filtering
- `repositories/trivial/` — Built-in trivial PR detectors: `update-go`, `update-node`, `update-python` (dependency updates), `bump-go`, `bump-node`, `bump-python` (version bumps with `.autobump.yaml` validation), `docs-only`
- `repositories/trivial/autobump/` — Parser for `.autobump.yaml` config files used by bump detectors
- `repositories/auth/` — Filesystem-based OAuth token storage
- `repositories/selfupdate/` — CLI binary self-updater via cliforge
- `repositories/container.go` — `AIReviewerFactory` and `RulesRepositoryFactory` for settings-driven backend selection

### Support Package (`internal/support/`)
Shared utilities: `diff_splitter.go` (parse unified diffs), `file_classifier.go` (detect language via langforge), `url_parser.go` (parse PR URLs via gitforge), `prompt_builder.go` (build AI system/user prompts), `response_parser.go` (shared JSON response parsing for all AI backends)

### Dependency Injection
Each layer has a `container.go` with `RegisterProviders(*dig.Container) error`. Registration order: repositories → entities → commands → controllers → app. Entry point: `cmd/code-guru/dig.go`.

## Testing

- Build tag `//go:build unit` required on every test file
- External test packages (e.g., `package support_test`)
- BDD structure: `// given`, `// when`, `// then`
- `t.Parallel()` + `t.Run()` for unit tests
- Test doubles in `test/domain/doubles/repositories/` for domain-contract stubs and `test/infrastructure/doubles/repositories/` for stubs that double infrastructure-only types (e.g., webhook `Submitter`, `GitHubTokenizer`) — keep the layer the stub references on the same side of the import boundary
- Entity builders in `test/domain/entitybuilders/` (fluent API via testkit.BaseBuilder)

## Key Dependencies

- `github.com/rios0rios0/cliforge` — CLI utilities and self-update support
- `github.com/rios0rios0/gitforge` — Multi-provider Git abstraction (GitHub, Azure DevOps)
- `github.com/rios0rios0/langforge` — Language classification by file extension
- `github.com/rios0rios0/testkit` — Test builder base utilities
- `go.uber.org/dig` — Dependency injection
- `github.com/spf13/cobra` — CLI framework
- `github.com/sirupsen/logrus` — Logging (always aliased as `logger`)

## Configuration

YAML config file (`.code-guru.yaml`) searched in: `.`, `.config`, `configs`, `~`, `~/.config`. Override with `-c` flag. Token fields support `${ENV_VAR}` expansion, file path resolution, and inline values.

Key config sections: `providers[]`, `ai` (backend + openai/claude/anthropic sub-configs), `rules`, `trivial`, `server` (port, webhook_secret for `serve` command), `github_app` (app_id, private_key for GitHub App auth).

For CI/CD environments without a config file, all settings can be provided via `CODE_GURU_*` environment variables (see README for full list).
