<h1 align="center">Code Guru</h1>
<p align="center">
    <a href="https://github.com/rios0rios0/code-guru/releases/latest">
        <img src="https://img.shields.io/github/release/rios0rios0/code-guru.svg?style=for-the-badge&logo=github" alt="Latest Release"/></a>
    <a href="https://github.com/rios0rios0/code-guru/blob/main/LICENSE">
        <img src="https://img.shields.io/github/license/rios0rios0/code-guru.svg?style=for-the-badge&logo=github" alt="License"/></a>
    <a href="https://github.com/rios0rios0/code-guru/actions/workflows/default.yaml">
        <img src="https://img.shields.io/github/actions/workflow/status/rios0rios0/code-guru/default.yaml?branch=main&style=for-the-badge&logo=github" alt="Build Status"/></a>
    <a href="https://sonarcloud.io/summary/overall?id=rios0rios0_code-guru">
        <img src="https://img.shields.io/sonar/coverage/rios0rios0_code-guru?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarqubecloud" alt="Coverage"/></a>
    <a href="https://sonarcloud.io/summary/overall?id=rios0rios0_code-guru">
        <img src="https://img.shields.io/sonar/quality_gate/rios0rios0_code-guru?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarqubecloud" alt="Quality Gate"/></a>
    <a href="https://www.bestpractices.dev/projects/12023">
        <img src="https://img.shields.io/cii/level/12023?style=for-the-badge&logo=opensourceinitiative" alt="OpenSSF Best Practices"/></a>
</p>

A CLI tool that leverages AI (Claude Code CLI or OpenAI API) to automatically review pull requests across GitHub and Azure DevOps, enforcing coding standards from configurable rule files.

## Features

- Multi-provider support via [gitforge](https://github.com/rios0rios0/gitforge): GitHub and Azure DevOps
- Dual AI backend: Claude Code CLI (`claude --print`) or OpenAI Chat Completions API
- Rule-based reviews using Markdown files from [guide](https://github.com/rios0rios0/guide) (or any directory)
- YAML `frontmatter` in rules for file-glob-based filtering (e.g., `paths: ["**/*.go"]`)
- Inline and general PR comments posted back via gitforge
- Three modes: single PR review, batch review-all, and discover (list open PRs)

## Installation

```bash
go install github.com/rios0rios0/codeguru/cmd/code-guru@latest
```

Or build from source:

```bash
git clone https://github.com/rios0rios0/code-guru.git
cd code-guru
go build -o code-guru ./cmd/code-guru/
```

## Configuration

Create a `.code-guru.yaml` file (searched in `.`, `.config`, `configs`, `~`, `~/.config`):

```yaml
providers:
  - type: 'github'
    token: '${GITHUB_TOKEN}'
    organizations:
      - 'rios0rios0'
  - type: 'azuredevops'
    token: '${AZURE_DEVOPS_PAT}'
    organizations:
      - 'MyOrg'

ai:
  backend: 'claude'
  claude:
    binary_path: 'claude'
    model: 'sonnet'
    max_turns: 1
  openai:
    api_key: '${OPENAI_API_KEY}'
    model: 'gpt-4o'

rules:
  path: '${HOME}/Development/github.com/rios0rios0/guide/.ai/claude/rules'
  categories: []
```

### Token Resolution

Tokens support three resolution strategies:
1. **Environment variable**: `${GITHUB_TOKEN}` expands from the environment
2. **File path**: if the resolved string is a file path, its contents are read
3. **Inline**: literal token string

## Usage

### Discover open PRs

```bash
code-guru discover -c .code-guru.yaml
```

### Review all open PRs (batch mode)

```bash
code-guru review-all -c .code-guru.yaml --dry-run
code-guru review-all -c .code-guru.yaml
```

### Review a single PR

```bash
code-guru review https://github.com/org/repo/pull/123
code-guru review https://dev.azure.com/org/project/_git/repo/pullrequest/456
```

### Flags

| Flag            | Description                                  |
|-----------------|----------------------------------------------|
| `-c, --config`  | Path to config file (default: auto-discover) |
| `--dry-run`     | Run review without posting comments          |
| `-v, --verbose` | Enable debug logging                         |

## Supported Providers

| Provider     | Type Key      | PR Comments | Inline Comments |
|--------------|---------------|-------------|-----------------|
| GitHub       | `github`      | Yes         | Yes             |
| Azure DevOps | `azuredevops` | Yes         | Yes             |

## AI Backends

| Backend     | Key      | How It Works                                             |
|-------------|----------|----------------------------------------------------------|
| Claude Code | `claude` | Invokes `claude --print` CLI as a subprocess             |
| OpenAI      | `openai` | Calls the Chat Completions API with JSON response format |

## Rules

Rules are Markdown files loaded from the configured `rules.path`. Each file represents a rule category (e.g., `security.md`, `golang.md`, `testing.md`).

Rules can include YAML `frontmatter` with `paths` globs for file-specific filtering:

```markdown
---
paths:
  - "**/*.go"
---
# Go Conventions

Use `gofmt` for formatting...
```

Universal categories (always included): `architecture`, `ci-cd`, `code-style`, `design-patterns`, `documentation`, `git-flow`, `security`, `testing`.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

See [LICENSE](LICENSE) for details.
