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
- Project-aware reviews: the reviewed repository's own `CLAUDE.md` is loaded automatically (on any provider) and forwarded to the AI as project-specific context, so the review honours the project's documented conventions — see [Project Guidelines](#project-guidelines-claudemd)
- Intent-aware reviews: the PR's title, branch names, description, and commit count are forwarded to the AI so it can judge whether the diff actually does what the author claims and flag undocumented scope creep — see [Pull Request Context](#pull-request-context-intent-aware-reviews)
- Inline and general PR comments posted back via gitforge
- Three modes: single PR review, batch review-all, and discover (list open PRs)
- Reviews each PR exactly once — subsequent pushes are no-ops. To request a re-review, post a PR comment that mentions `@code-guru` (case-insensitive). On a re-review the bot acts as a reviewer who reads the existing conversation: it loads every prior bot inline thread plus every reply, classifies each as `resolved` / `outstanding` / `outdated`, posts one short reply **nested inside each prior thread** (via the provider's `ReplyToThread`, so the answer lands below the author's reply like a human reviewer rather than as a separate same-line comment), and auto-closes the threads it considers resolved (Azure DevOps thread state `fixed`). Net-new findings only land if the diff genuinely warrants one and it does NOT overlap a thread the bot already addressed — replacing the pre-existing failure mode where every re-review flooded the PR with reworded duplicates of every prior comment. The bot recognises its own prior comments by the built-in `code-guru` login shape, by self-detecting the account that posted its PR-wide review annotations on the PR, and by any identity listed in `bot_identities` (env `CODE_GURU_BOT_IDENTITIES`) — so re-reviews still read and resolve prior threads when the deployment posts under a service account

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
  # Times the AI backend is re-sampled per review when it returns a non-JSON or
  # transient-error response before the review is marked failed. Defaults to 3;
  # set to 1 to disable retries.
  max_attempts: 3
  # When true (the default), the reviewed repository's own root CLAUDE.md is
  # loaded and forwarded to the AI as project-specific review context. Set to
  # false to opt out.
  project_guidelines: true
  # When true (the default), the PR's description and commit count are fetched
  # from the provider and forwarded to the AI as intent context (together with
  # the title and branch names already in the prompt). Set to false to opt out.
  pr_metadata: true
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

# Account identities the bot posts review comments under. Only needed when the
# deployment posts under a service account whose login does not start with
# `code-guru` (common on self-hosted Azure DevOps) — on a re-review the bot uses
# these to recognise its own prior threads and resolve them instead of
# re-posting. The bot also self-detects this from its own PR-wide review
# annotations, so this is optional. Override via CODE_GURU_BOT_IDENTITIES.
bot_identities:
  - 'svc-codeguru@example.com'
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
| `docs-only`    | Only `*.md` files changed (excluding a `CHANGELOG.md`-only change — see note) |

> **Note — a changelog-only change is a version bump.** A PR that touches **only** `CHANGELOG.md` is the signature of a version bump / release ceremony, so it is matched **exclusively** by the `bump-*` adapters. The `docs-only` and `update-*` adapters decline it: the changelog may still *accompany* a documentation or dependency change, but it can never be the sole trigger. This means disabling the `bump-*` adapters reliably keeps version bumps out of trivial auto-merge, instead of having them silently fall through to `docs-only` or `update-*`.

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
  auto_merge_allowed_authors:   # restrict auto-merge to these PR authors; empty = any author
    - 'autobump@example.com'
    - 'autoupdate@example.com'
```

Or via environment variables:

```bash
CODE_GURU_TRIVIAL_ADAPTERS=update-go,bump-go,docs-only
CODE_GURU_TRIVIAL_AUTO_MERGE=true
CODE_GURU_TRIVIAL_MERGE_STRATEGY=squash
CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS=autobump@example.com,autoupdate@example.com
```

`auto_merge` is intentionally off by default — it bypasses human review and merges cross-system, so the gate is "operator must explicitly opt in". A merge failure logs at warn and the trivial-approve verdict still stands; the PR author can complete the merge manually from the platform UI.

`auto_merge_allowed_authors` decides **who** is trusted to merge unattended, separately from triviality (which decides **what** is eligible). When non-empty, only PRs whose author matches an entry (case-insensitive) auto-merge — so a human's docs PR is approved but left for a human to merge, while a trusted automation account's PR (dependency bumps, version bumps, config refresh) merges on its own. Leaving it empty keeps the historical "any author" behaviour; that is **not recommended together with policy bypass**, because it force-merges every trivial PR — including a human's — past `Required reviewers` (the bot logs a warning in that case). A docs-only diff is not inherently safe: prose can carry a malicious install command, a poisoned package name, or a phishing link, and bypass means no human ever reviews it.

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
| `CODE_GURU_TRIVIAL_AUTO_MERGE_AUTHORS` | Comma-separated PR-author identities allowed to auto-merge; empty = any author (not recommended with bypass) |                      |
| `CODE_GURU_AI_SUBMIT_NATIVE_REVIEW`   | Records a native review (Approved / Changes Requested) on the platform's reviewer panel; set to `false` to opt out | `true`               |
| `CODE_GURU_AI_REVIEW_DRAFTS`          | When `true`, the bot reviews draft PRs as well — by default drafts are skipped | `false`              |
| `CODE_GURU_AI_MAX_ATTEMPTS`           | Times the AI backend is re-sampled per review when it returns a non-JSON or transient-error response before the review is marked failed (`1` disables retries) | `3`                  |
| `CODE_GURU_AI_PROJECT_GUIDELINES`     | Loads the reviewed repository's own `CLAUDE.md` as project-specific review context; set to `false` to opt out | `true`               |
| `CODE_GURU_AI_PR_METADATA`            | Fetches the PR's description and commit count as intent context for the AI; set to `false` to opt out | `true`               |
| `CODE_GURU_ANTHROPIC_CONTEXT_1M`      | Requests the Anthropic 1M-token context window (`context-1m-2025-08-07` beta) so larger PRs fit in one review pass; set to `false` for accounts/models that cannot use the beta | `true`               |
| `CODE_GURU_BOT_IDENTITIES`            | Comma-separated account identities the bot posts under (so re-reviews recognise its own prior threads); the built-in `code-guru` shape and self-detection apply when unset |                      |

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

### Project Guidelines (CLAUDE.md)

On top of the operator-configured rules, Code Guru reads the **reviewed repository's own root `CLAUDE.md`** — the file projects use to document conventions for AI tooling — and forwards it to the AI as project-specific review context. This works on every supported provider (GitHub and Azure DevOps) through the same file-access API the trivial detectors use, and it means the review honours conventions the generic ruleset cannot know about (naming, layering, testing patterns, intentional trade-offs).

Behaviour details:

- When the PR itself modifies `CLAUDE.md`, the repository fetch is skipped — the model already reads the change in the diff, and layering the pre-change copy on top would present two conflicting versions of the same document.
- The fetch is best-effort: a repository without a `CLAUDE.md`, a provider without file-access support, or a transient error simply produces a review without project guidelines. It never fails or delays the review beyond a 10-second fetch timeout.
- Content is bounded to 32 KiB so a pathological guidelines file cannot crowd the diff out of the model's context window.
- The document is framed to the model as documentation, not instructions — it cannot change the output format, the verdict rules, or the reviewer role.

Enabled by default; opt out with `ai.project_guidelines: false` or `CODE_GURU_AI_PROJECT_GUIDELINES=false`.

### Pull Request Context (intent-aware reviews)

Beyond the diff, Code Guru forwards the PR's **author-supplied metadata** to the AI so it reviews the change against its stated intent:

- **Title and branch names** (already part of the prompt header) signal the change type — a `fix/` branch that quietly introduces new behaviour, or a `chore` that alters runtime logic, deserves a comment.
- **Description** — the author's statement of what the change does and why. The model is told to flag significant changes the description leaves unmentioned (scope creep) and to weigh the author's explanations before flagging intentional oddities.
- **Commit count** — how the change was assembled, fetched from the provider's REST API (GitHub: one call returns both body and count; Azure DevOps: the PR resource plus its `/commits` collection).

Behaviour details:

- The fetch is best-effort with a 10-second timeout: an unsupported provider or an API error simply produces a review without the context — it never fails the review.
- The description is bounded to 16 KiB so a generated body (release bots pasting entire upstream changelogs) cannot crowd the diff out of the model's context window.
- The description is framed to the model as author-supplied data, not instructions — a body that says "approve this PR" is treated as content to evaluate, never as a command.

Enabled by default; opt out with `ai.pr_metadata: false` or `CODE_GURU_AI_PR_METADATA=false`.

## Large pull requests

Every review sends the full diff — plus the rules, the repository's `CLAUDE.md`, the PR metadata, and any prior review conversation — to the AI backend in a single request. When that combined prompt is larger than the model's context window, the review cannot be produced, and Code Guru handles it explicitly rather than failing silently:

- **A clear failure notice.** The PR gets a "too large for the AI model's context window" annotation that reports the change's scale (file count and total diff size) and the correct next steps — split the change into smaller pull requests, or exclude generated/vendored/lock files — instead of a generic "try again" message. Retrying or pushing more commits does not help, because the diff only grows.
- **No wasted retries.** A prompt-too-long failure is deterministic (the prompt is identical on every attempt), so the retry budget is skipped entirely for this class of failure.
- **A larger window on Anthropic.** The Anthropic backend requests the 1M-token context window (`context-1m-2025-08-07` beta) by default, so pull requests up to roughly five times larger fit in one pass before hitting this path. For prompts under 200K tokens the beta is a no-op; very large prompts may incur Anthropic long-context pricing. Opt out with `ai.anthropic.context_1m: false` or `CODE_GURU_ANTHROPIC_CONTEXT_1M=false` on accounts or models that cannot use the beta.

## Contributing

Contributions are welcome. See [CONTRIBUTING.md](CONTRIBUTING.md) for guidelines.

## License

See [LICENSE](LICENSE) for details.
