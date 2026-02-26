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

| Variable | Description | Required |
|----------|-------------|----------|
| `GITLAB_API_TOKEN` | GitLab personal access token | Yes |
| `OPENAI_API_KEY` | OpenAI API key for code review | Yes |
