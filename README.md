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
- Reviews each PR exactly once — subsequent pushes are no-ops. To request a re-review, post a PR comment that mentions `@code-guru` (case-insensitive). The bot dedups inline comments against its own previous postings on re-reviews so the PR is never flooded

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
  # When true, the bot also records a native pull request review (Approved /
  # Changes Requested) on the platform's reviewer panel in addition to the
  # text completion annotation. Defaults to true; set to false to opt out.
  submit_native_review: true
  # When false (the default), draft PRs are skipped entirely — set to true to
  # opt back in.
  review_drafts: false
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

| Flag            | Description                                    |
|-----------------|------------------------------------------------|
| `-c, --config`  | Path to config file (default: auto-discover)   |
| `--backend`     | AI backend: `openai`, `claude`, or `anthropic` |
| `--rules-path`  | Path to rules directory                        |
| `--dry-run`     | Run review without posting comments            |
| `-v, --verbose` | Enable debug logging                           |

## Supported Providers

| Provider     | Type Key      | PR Comments | Inline Comments |
|--------------|---------------|-------------|-----------------|
| GitHub       | `github`      | Yes         | Yes             |
| Azure DevOps | `azuredevops` | Yes         | Yes             |

## AI Backends

| Backend     | Key         | How It Works                                             |
|-------------|-------------|----------------------------------------------------------|
| Anthropic   | `anthropic` | Calls the Anthropic Messages API directly via Go SDK     |
| Claude Code | `claude`    | Invokes `claude --print` CLI as a subprocess             |
| OpenAI      | `openai`    | Calls the Chat Completions API with JSON response format |

## AI Verdict

Each review returns a verdict alongside comments:

| Verdict            | Meaning                                        |
|--------------------|------------------------------------------------|
| `approve`          | No blocking issues, safe to merge              |
| `request_changes`  | Error-level issues that must be fixed          |
| `comment`          | Informational feedback only, not blocking      |

The verdict is printed as `VERDICT:<value>` for machine parsing.

## Trivial PR Detection

When trivial detection is enabled and CI has passed, PRs matching built-in adapters are handled **without calling the LLM**, saving tokens. In webhook mode, CI status is provided by the webhook event. In CLI mode, CI status detection is planned via gitforge's `GetPullRequestCheckStatus()` (not yet available).

There are two categories of trivial adapters:

### Update Adapters (Dependency Updates)

These detect dependency update PRs and auto-approve them.

| Adapter          | Matches When                                                  |
|------------------|---------------------------------------------------------------|
| `update-go`      | Only `go.mod`, `go.sum`, `CHANGELOG.md` changed              |
| `update-node`    | Only `package.json`, lock files, `CHANGELOG.md` changed      |
| `update-python`  | Only `pyproject.toml`, `requirements*.txt`, `CHANGELOG.md`   |

### Bump Adapters (Version Bumps / Releases)

These detect version bump (release ceremony) PRs. If the repo contains an `.autobump.yaml` config file, the adapter validates that all version files declared in the config are present in the PR. Missing files result in a **reject** verdict.

| Adapter          | Default Files                          | AutoBump Language Key |
|------------------|----------------------------------------|-----------------------|
| `bump-go`        | `CHANGELOG.md`                         | `go`                  |
| `bump-node`      | `package.json`, `CHANGELOG.md`         | `typescript`          |
| `bump-python`    | `*/__init__.py`, `CHANGELOG.md`        | `python`              |

### Other Adapters

| Adapter        | Matches When                                                    |
|----------------|-----------------------------------------------------------------|
| `docs-only`    | Only `*.md` files changed                                       |

### Configuration

Configure in `.code-guru.yaml`:

```yaml
trivial:
  enabled: true
  adapters:
    - 'update-go'
    - 'bump-go'
    - 'docs-only'
  auto_merge: false      # opt-in; true completes the PR after a trivial-approve verdict
  merge_strategy: ''     # 'merge' / 'squash' / 'rebase' — empty falls back to platform default
```

Or via environment variables:

```bash
CODE_GURU_TRIVIAL_ADAPTERS=update-go,bump-go,docs-only
CODE_GURU_TRIVIAL_AUTO_MERGE=true
CODE_GURU_TRIVIAL_MERGE_STRATEGY=squash
```

`auto_merge` is intentionally off by default — it bypasses human review and merges cross-system, so the gate is "operator must explicitly opt in". A merge failure logs at warn and the trivial-approve verdict still stands; the PR author can complete the merge manually from the platform UI.

## Server / Webhook Mode

Code Guru can run as a long-lived HTTP server that receives webhook events from
GitHub Apps and Azure DevOps Service Hooks. Each event is enqueued onto a bounded
worker pool and the HTTP response returns immediately (`202 Accepted`), so the
review runs asynchronously and never blocks the sender.

```bash
code-guru serve --port 8080
```

### Endpoints

| Endpoint                | Method | Auth                           | Notes                                                                                                                       |
|-------------------------|--------|--------------------------------|-----------------------------------------------------------------------------------------------------------------------------|
| `/health`               | GET    | none                           | Liveness probe                                                                                                              |
| `/webhooks/github`      | POST   | HMAC-SHA256                    | Validates the `X-Hub-Signature-256` header against `server.webhook_secret`. Acts on `pull_request` `opened`/`synchronize`/`reopened`. |
| `/webhooks/azuredevops` | POST   | HTTP Basic                     | Username must be `code-guru`; password must equal `server.webhook_secret`. Acts on `git.pullrequest.created`/`git.pullrequest.updated` for active PRs. |

### Authentication Models

- **GitHub** -- the secret is the value configured on the GitHub App webhook
  ("Webhook secret" in the App settings). When `github_app.app_id` and
  `github_app.private_key` are configured the server signs an RS256 JWT and
  exchanges it for a per-installation access token, cached until 5 minutes
  before expiry. Without `github_app.*` the handler falls back to the configured
  `github` PAT in `providers[]`.
- **Azure DevOps** -- ADO does not sign Service Hooks, so it uses HTTP Basic.
  Configure the Service Hook with username `code-guru` and password equal to
  `server.webhook_secret`.

### Configuration

```yaml
server:
  port: 8080
  webhook_secret: '${CODE_GURU_WEBHOOK_SECRET}'
  workers: 8
  queue_size: 100
  shutdown_timeout: 30s
  allowed_organizations:
    - 'ExampleOrg'
  allowed_projects:
    - 'Platform'

github_app:
  app_id: 123456
  private_key: '${CODE_GURU_GITHUB_PRIVATE_KEY}'
```

### Webhook Environment Variables

| Variable                                  | Description                                                       | Default              |
|-------------------------------------------|-------------------------------------------------------------------|----------------------|
| `CODE_GURU_PORT`                          | HTTP port to listen on                                            | `8080`               |
| `CODE_GURU_WEBHOOK_SECRET`                | Shared secret for HMAC (GitHub) and Basic Auth password (ADO)     |                      |
| `CODE_GURU_SERVER_WORKERS`                | Worker count draining the review queue                            | `runtime.NumCPU()`   |
| `CODE_GURU_SERVER_QUEUE_SIZE`             | Maximum buffered jobs before submitters get `503 Service Unavailable` | `100`            |
| `CODE_GURU_SERVER_SHUTDOWN_TIMEOUT`       | Maximum drain time on `SIGINT`/`SIGTERM`                          | `30s`                |
| `CODE_GURU_SERVER_ALLOWED_ORGANIZATIONS`  | Comma-separated allowlist of org/owner names (empty = allow all)  |                      |
| `CODE_GURU_SERVER_ALLOWED_PROJECTS`       | Comma-separated allowlist of ADO project names (empty = allow all) |                     |
| `CODE_GURU_GITHUB_APP_ID`                 | Numeric GitHub App ID                                             |                      |
| `CODE_GURU_GITHUB_PRIVATE_KEY`            | PEM-encoded RSA private key for the GitHub App                    |                      |

### Running with Docker

```bash
docker build -t code-guru:latest .
docker run --rm -p 8080:8080 \
  -e CODE_GURU_BACKEND=anthropic \
  -e CODE_GURU_ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY \
  -e CODE_GURU_WEBHOOK_SECRET=$WEBHOOK_SECRET \
  -e CODE_GURU_PROVIDER_TOKEN=$AZURE_DEVOPS_PAT \
  -e CODE_GURU_SERVER_ALLOWED_ORGANIZATIONS=ExampleOrg \
  code-guru:latest
```

The Dockerfile uses a multi-stage build (`golang:1.26-alpine` builder,
`gcr.io/distroless/static-debian12:nonroot` runtime) and runs as the
non-root user.

The image ships with a `HEALTHCHECK` directive that calls `code-guru
health` against the local listener every 30 seconds. The `health`
subcommand can also be invoked directly for ad-hoc smoke tests:

```bash
code-guru health --url http://127.0.0.1:8080/health --timeout 4s
```

Exit codes: `0` on `200`, `1` on any other status, network error, or
timeout.

### Kubernetes deployment

When the `serve` controller starts inside a Kubernetes pod (detected
via the standard `KUBERNETES_SERVICE_HOST` env var that the kubelet
always injects), the dispatcher automatically swaps its default
per-pod in-memory webhook dedup for a cross-pod backend backed by
`coordination.k8s.io/v1` `Lease` objects. This is required when the
deployment runs with `replicas > 1` because Azure DevOps fires both
`git.pullrequest.created` and `git.pullrequest.updated` for every
new PR; without a shared lock the K8s `Service` round-robins one
delivery to each replica and the bot posts duplicate reviews. The
lease is named `code-guru-{sanitised-key}-{hash}` (the SHA-256 suffix
prevents collisions from the lossy character substitution) and is
created with `leaseDurationSeconds: 900` (must exceed the bot's
maximum review wall-time so the takeover path never steals an
actively-held lease — the worst review observed was ≈8 minutes)
as freshness metadata —
Kubernetes does NOT auto-delete `Lease` objects when that duration
elapses, so the dedup contract relies on two explicit pieces of work:

- The owning pod `Delete`s the lease after the worker finishes
  (success or failure), so a real follow-up push minutes later
  re-acquires immediately.
- A subsequent webhook delivery whose `Create` returns
  `AlreadyExists` runs a stale-lease takeover: it `Get`s the holding
  lease, checks whether `acquireTime + leaseDurationSeconds` has
  already passed, and if so `Delete`s the stale lease (with a UID
  precondition for race safety) and retries `Create`. This recovers
  from a pod crash mid-review — the maximum window during which a
  crashed lease blocks new work is `leaseDurationSeconds`.

The K8s API server's optimistic concurrency on `Create` is what
makes the dedup atomic across replicas: exactly one `Create` for a
given `(namespace, name)` succeeds and every concurrent `Create`
returns `409 AlreadyExists`.

Required RBAC (apply once per namespace the bot runs in):

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: code-guru
  name: code-guru-webhook-dedup
rules:
  - apiGroups: ["coordination.k8s.io"]
    resources: ["leases"]
    verbs: ["get", "list", "create", "delete", "update", "patch"]
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  namespace: code-guru
  name: code-guru-webhook-dedup
subjects:
  - kind: ServiceAccount
    name: code-guru
    namespace: code-guru
roleRef:
  kind: Role
  name: code-guru-webhook-dedup
  apiGroup: rbac.authorization.k8s.io
```

If the RBAC is missing, the bot logs a `Warn` at startup and falls
back to the per-pod in-memory cache: still safe, but cross-pod
duplicates will not be suppressed. Outside a pod (local CLI runs,
unit tests) no Kubernetes API is contacted.

## Environment Variable Configuration

For CI/CD environments without a config file, all settings can be provided via `CODE_GURU_*` environment variables:

| Variable                       | Description                       | Default              |
|--------------------------------|-----------------------------------|----------------------|
| `CODE_GURU_BACKEND`                   | AI backend                                                               | `openai`             |
| `CODE_GURU_OPENAI_API_KEY`            | OpenAI API key                                                           |                      |
| `CODE_GURU_ANTHROPIC_API_KEY`         | Anthropic API key                                                        |                      |
| `CODE_GURU_RULES_PATH`                | Path to rules directory                                                  |                      |
| `CODE_GURU_PROVIDER_TOKEN`            | Git provider token                                                       |                      |
| `CODE_GURU_TRIVIAL_ADAPTERS`          | Comma-separated adapter names                                            |                      |
| `CODE_GURU_TRIVIAL_AUTO_MERGE`        | Opt-in flag that completes the PR after a trivial-approve verdict        | `false`              |
| `CODE_GURU_TRIVIAL_MERGE_STRATEGY`    | gitforge merge strategy (`merge` / `squash` / `rebase`); empty = default |                      |
| `CODE_GURU_AI_SUBMIT_NATIVE_REVIEW`   | Records a native review (Approved / Changes Requested) on the platform's reviewer panel; set to `false` to opt out | `true`               |
| `CODE_GURU_AI_REVIEW_DRAFTS`          | When `true`, the bot reviews draft PRs as well — by default drafts are skipped | `false`              |

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
