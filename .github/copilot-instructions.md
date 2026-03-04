# Copilot Instructions

## Project Overview

Code Guru is an AI-powered CLI tool written in Go that automatically reviews pull requests.
It supports GitHub and Azure DevOps as Git hosting providers, and Claude Code CLI or OpenAI Chat Completions API as AI backends.
Review rules are loaded from configurable Markdown files with optional YAML frontmatter for file-glob filtering.

## Architecture

The project follows **Clean Architecture** with strict layer separation:

- **`cmd/code-guru/`** — Application entry point. Builds the DI container, wires Cobra commands, and starts the CLI.
- **`internal/domain/`** — Core business logic. Contains entity definitions, repository interfaces, and command implementations. This layer has no infrastructure dependencies.
  - `entities/` — Domain models (`Review`, `ReviewRequest`, `Rule`, `Settings`, `Controller` interface).
  - `repositories/` — Abstract interfaces (`AIReviewerRepository`, `RulesRepository`).
  - `commands/` — Use-case implementations (`ReviewCommand`, `ReviewAllCommand`, `DiscoverCommand`).
- **`internal/infrastructure/`** — Concrete implementations of domain interfaces.
  - `repositories/` — AI backend implementations (`openai/`, `claude/`, `rules/`) and factory types.
  - `controllers/` — Cobra CLI controllers that bridge CLI input to domain commands.
- **`internal/support/`** — Shared utility functions (URL parsing, diff splitting, file classification, prompt building).
- **`test/domain/doubles/`** — Test doubles (stubs) for domain repository interfaces.
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
- **Test doubles**: Place stubs in `test/domain/doubles/repositories/`. Name them with the `Stub` prefix (e.g., `StubAIReviewerRepository`). Stubs store the last request and return canned responses.

## Logging

Use `github.com/sirupsen/logrus` aliased as `logger`. Use structured log levels: `logger.Infof`, `logger.Errorf`, `logger.Debugf`, `logger.Warnf`.

## Configuration

Settings are loaded from YAML files (`.code-guru.yaml`). Tokens support environment variable expansion (`${VAR_NAME}`), file path resolution, and inline values. Validate required fields in `validateSettings`.

## Build and CI

- **Build**: `go build -o bin/code-guru ./cmd/code-guru/`
- **Test**: `go test -tags unit ./...`
- **CI**: GitHub Actions workflow in `.github/workflows/default.yaml` using reusable pipelines from `rios0rios0/pipelines`. Includes golangci-lint, CodeQL SAST, and SonarCloud quality gates.
- **Makefile**: Includes external makefiles from `rios0rios0/pipelines` for common and Go-specific targets.

## Dependencies

Only add new dependencies when strictly necessary. Prefer the standard library. Current key dependencies:

- `github.com/rios0rios0/gitforge` — Multi-provider Git abstraction
- `github.com/sashabaranov/go-openai` — OpenAI API client
- `github.com/sirupsen/logrus` — Structured logging
- `github.com/spf13/cobra` — CLI framework
- `github.com/stretchr/testify` — Testing assertions
- `go.uber.org/dig` — Dependency injection
- `gopkg.in/yaml.v3` — YAML parsing

## Contributing

See [CONTRIBUTING.md](../CONTRIBUTING.md) for the development workflow and the [Development Guide](https://github.com/rios0rios0/guide/wiki) for coding standards.
