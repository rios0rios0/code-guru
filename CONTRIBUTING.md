# Contributing

Contributions are welcome. By participating, you agree to maintain a respectful and constructive environment.

For coding standards, testing patterns, architecture guidelines, commit conventions, and all
development practices, refer to the **[Development Guide](https://github.com/rios0rios0/guide/wiki)**.

## Prerequisites

- [Go](https://go.dev/dl/) 1.26+
- [Git](https://git-scm.com/) 2.0+

## Development Workflow

1. Fork and clone the repository
2. Create a branch: `git checkout -b feat/my-change`
3. Install dependencies:
   ```bash
   go mod download
   ```
4. Build the project:
   ```bash
   go build -o codeguru main.go
   ```
5. Set up environment variables:
   ```bash
   export GITLAB_API_TOKEN="your-gitlab-api-token"
   export GITLAB_PROJECT_ID="your-gitlab-project-id"
   export OPENAI_API_KEY="your-openai-api-key"
   ```
6. Run the application:
   ```bash
   ./codeguru
   ```
7. Run tests:
   ```bash
   go test ./...
   ```
8. Commit following the [commit conventions](https://github.com/rios0rios0/guide/wiki/Life-Cycle/Git-Flow)
9. Open a pull request against `main`

## Local Environment

| Variable | Description | Required |
|----------|-------------|----------|
| `GITLAB_API_TOKEN` | GitLab personal access token | Yes |
| `GITLAB_PROJECT_ID` | GitLab project ID to review | Yes |
| `OPENAI_API_KEY` | OpenAI API key for code review | Yes |
