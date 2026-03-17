# Contributing

Contributions are welcome. By participating, you agree to maintain a respectful and constructive environment.

For coding standards, testing patterns, architecture guidelines, commit conventions, and all
development practices, refer to the **[Development Guide](https://github.com/rios0rios0/guide/wiki)**.

## Prerequisites

- [Go](https://go.dev/dl/) 1.26+

## Development Workflow

1. Fork and clone the repository
2. Create a branch: `git checkout -b feat/my-change`
3. Install dependencies:
   ```bash
   go mod download
   ```
4. Build the binary:
   ```bash
   go build -o bin/code-guru .
   ```
5. Make your changes
6. Run tests:
   ```bash
   go test ./...
   ```
7. Commit following the [commit conventions](https://github.com/rios0rios0/guide/wiki/Life-Cycle/Git-Flow)
8. Open a pull request against `main`

## Local Environment

| Variable                       | Description                    | Required           |
|--------------------------------|--------------------------------|--------------------|
| `GITHUB_TOKEN`                 | GitHub personal access token   | For GitHub PRs     |
| `OPENAI_API_KEY`               | OpenAI API key                 | For OpenAI backend |
| `CODE_GURU_ANTHROPIC_API_KEY`  | Anthropic API key              | For Anthropic backend |

## Adding a New Trivial Adapter

Trivial adapters detect PRs that can be auto-approved without calling the LLM (e.g., dependency bumps, docs-only changes).

1. Create a new file in `internal/infrastructure/repositories/trivial/` (e.g., `bump_java_detector.go`)
2. Implement the `TrivialDetector` interface from `internal/domain/repositories/trivial_detector_repository.go`:
   ```go
   type TrivialDetector interface {
       Name() string
       IsTrivial(files []string) bool
       Summary(files []string) string
   }
   ```
3. Register your adapter in `internal/infrastructure/repositories/trivial/registry.go` by adding an entry to the `allDetectors` map:
   ```go
   var allDetectors = map[string]repositories.TrivialDetector{
       // ... existing adapters ...
       "bump-java": &BumpJavaDetector{},
   }
   ```
4. Add unit tests following BDD structure (`// given`, `// when`, `// then`) with `t.Parallel()` and `t.Run()`
5. Update `CHANGELOG.md` under `[Unreleased] > Added`
