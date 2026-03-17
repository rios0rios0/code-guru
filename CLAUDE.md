# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Project Does

Code Guru is a Go CLI tool that uses AI (Anthropic API, Claude Code CLI, or OpenAI API) to automatically review pull requests across GitHub and Azure DevOps. It loads review rules from Markdown files, sends PR diffs to an AI backend, and posts review comments back on the PR. It also supports trivial PR detection (dependency bumps, docs-only changes) to auto-approve without calling the LLM.

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

**Trivial PR flow:** CLI input → Controller → Command → DetectorRegistry (no AI) → Post approval comment via gitforge

### Domain Layer (`internal/domain/`)
- `entities/` — Framework-agnostic domain models: `Settings`, `ReviewRequest`, `ReviewResult` (with `Verdict`), `ReviewComment`, `FileDiff`, `Rule`, `Controller` interface
- `repositories/` — Interfaces only: `AIReviewerRepository` (AI engine contract), `RulesRepository` (rule loading contract), `TrivialDetector` (trivial PR detection contract)
- `commands/` — Business logic: `ReviewCommand` (single PR with trivial detection), `ReviewAllCommand` (batch), `DiscoverCommand` (list PRs)

### Infrastructure Layer (`internal/infrastructure/`)
- `controllers/` — Cobra CLI controllers implementing `entities.Controller`
- `repositories/anthropic/` — Anthropic Messages API backend (via official Go SDK)
- `repositories/claude/` — Claude Code CLI backend (invokes `claude --print`)
- `repositories/openai/` — OpenAI Chat Completions API backend
- `repositories/rules/` — Loads Markdown rule files from filesystem with YAML frontmatter glob filtering
- `repositories/trivial/` — Built-in trivial PR detectors: `bump-go`, `bump-node`, `bump-python`, `docs-only`

### Support Package (`internal/support/`)
Shared utilities: `diff_splitter.go` (parse unified diffs), `file_classifier.go` (detect language via langforge), `url_parser.go` (parse PR URLs via gitforge), `prompt_builder.go` (build AI system/user prompts), `response_parser.go` (shared JSON response parsing for all AI backends)

### Dependency Injection
Each layer has a `container.go` with `RegisterProviders(*dig.Container) error`. Registration order: repositories → entities → commands → controllers → app. Entry point: `cmd/code-guru/dig.go`.

## Testing

- Build tag `//go:build unit` required on every test file
- External test packages (e.g., `package support_test`)
- BDD structure: `// given`, `// when`, `// then`
- `t.Parallel()` + `t.Run()` for unit tests
- Test doubles in `test/domain/doubles/repositories/` (stubs with canned responses)
- Entity builders in `test/domain/entitybuilders/` (fluent API via testkit.BaseBuilder)

## Key Dependencies

- `github.com/anthropics/anthropic-sdk-go` — Anthropic Messages API client
- `github.com/rios0rios0/gitforge` — Multi-provider Git abstraction (GitHub, Azure DevOps)
- `github.com/rios0rios0/langforge` — Language classification by file extension
- `github.com/rios0rios0/testkit` — Test builder base utilities
- `go.uber.org/dig` — Dependency injection
- `github.com/spf13/cobra` — CLI framework
- `github.com/sirupsen/logrus` — Logging (always aliased as `logger`)

## Configuration

YAML config file (`.code-guru.yaml`) searched in: `.`, `.config`, `configs`, `~`, `~/.config`. Override with `-c` flag. Token fields support `${ENV_VAR}` expansion, file path resolution, and inline values.

For CI/CD environments without a config file, all settings can be provided via `CODE_GURU_*` environment variables (see README for full list).
