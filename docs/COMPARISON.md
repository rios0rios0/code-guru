<h1 align="center">Code Guru vs. GitHub Copilot vs. Claude Code Action</h1>

> **TL;DR:** Code Guru is the only one of the three that ships natively for Azure DevOps, swaps AI backends per deployment, and (after PR #130) treats prior review threads as a structured conversation it must reconcile before re-emitting comments. It is also the only one that does NOT execute code, edit files, or open follow-up commits. The roadmap below catalogues every feature the other two have and Code Guru does not, so contributors can pick a gap and close it.

## Purpose

This document compares Code Guru against the two most prominent peer products in the AI-PR-review space and turns that comparison into a concrete, prioritised improvement backlog.

The three subjects are:

| Subject | What it is | Where it runs |
|---------|------------|---------------|
| **Code Guru** | Go CLI / webhook server that posts AI reviews on PRs | Self-hosted: CLI, GitHub Actions, Lambda, Azure Functions, Kubernetes (webhook) |
| **GitHub Copilot code review** | Hosted feature on `github.com` that reviews PRs | GitHub-hosted, GitHub-only |
| **Claude Code Action** (`anthropics/claude-code-action`) | GitHub Action that runs Claude inside the user's CI | GitHub Actions runner, GitHub-only |

The findings in the "Re-review behaviour" section below are based on the public docs at [docs.github.com/en/copilot/how-tos/use-copilot-agents/request-a-code-review/use-code-review](https://docs.github.com/en/copilot/how-tos/use-copilot-agents/request-a-code-review/use-code-review) and [github.com/anthropics/claude-code-action](https://github.com/anthropics/claude-code-action) (capabilities-and-limitations.md).

---

## Feature matrix

Legend: ✅ supported · ⚠️ partial / opt-in · ❌ not supported · — not applicable

### Hosting & deployment

| Feature | Code Guru | GitHub Copilot | Claude Code Action |
|---------|:---------:|:-------------:|:------------------:|
| GitHub support | ✅ | ✅ | ✅ |
| Azure DevOps support | ✅ | ❌ | ❌ |
| GitLab support | ❌ | ❌ | ❌ |
| Self-hostable | ✅ | ❌ | ✅ (GitHub Actions runner) |
| Runs as a CLI | ✅ | ❌ | ❌ |
| Runs as a webhook server | ✅ | — (managed) | ❌ |
| Multi-pod deployment with cross-instance dedup | ✅ (Kubernetes Lease) | — | ❌ |
| Bring-your-own AI provider | ✅ (Anthropic / OpenAI / Claude CLI) | ❌ (GitHub-managed) | ⚠️ (Anthropic / Bedrock / Vertex / Foundry) |
| Editor / IDE integration | ❌ | ✅ (VS Code, JetBrains, Visual Studio, Xcode, GitHub Mobile) | ❌ |

### Triggering

| Feature | Code Guru | GitHub Copilot | Claude Code Action |
|---------|:---------:|:-------------:|:------------------:|
| Manual review on PR open | ✅ (CLI / `review-all`) | ✅ (assign reviewer) | ⚠️ (workflow trigger) |
| Auto-review every push | ✅ (webhook) | ⚠️ (configurable per repo/org) | ⚠️ (workflow trigger) |
| `@<bot>` mention in PR comment | ✅ (`@code-guru`) | ❌ (Copilot does not respond to user replies) | ✅ (`@claude`) |
| Issue assignment trigger | ❌ | — | ✅ |
| Scheduled / cron trigger | ❌ | ❌ | ✅ |
| Skip drafts by default | ✅ (configurable) | ⚠️ (configurable) | ❌ (workflow-defined) |
| Per-path trigger filtering | ❌ | ⚠️ (via `applyTo` instructions) | ✅ (workflow `paths`) |

### Review output

| Feature | Code Guru | GitHub Copilot | Claude Code Action |
|---------|:---------:|:-------------:|:------------------:|
| Inline file:line comments | ✅ | ✅ | ✅ |
| PR-wide summary comment | ✅ (completion annotation) | ✅ | ✅ (single live-updating comment) |
| Native PR review verdict (Approved / Changes Requested) | ✅ | ✅ | ❌ (explicitly blocked) |
| Auto-approval of trivial PRs (docs-only, dep bumps) | ✅ | ❌ | ❌ |
| GitHub `suggestion` blocks (one-click apply) | ❌ (field exists, not rendered) | ✅ | ⚠️ (via implemented commits) |
| Severity tags (`error`/`warning`/`info`) | ✅ | ⚠️ | ❌ |
| Comment threading on existing threads | ✅ (resolution reply path) | ❌ ("won't reply") | ✅ |
| Auto-close resolved threads | ✅ (PR #130, ADO `fixed`) | ❌ | ❌ |
| Streaming / progress indicator while reviewing | ⚠️ ("reviewing" marker is static) | ❌ | ✅ (live-updating checklist) |
| Cost / token-usage reporting | ❌ | ❌ | ⚠️ (visible in workflow logs) |

### Re-review behaviour (the load-bearing comparison)

| Feature | Code Guru (after PR #130) | GitHub Copilot | Claude Code Action |
|---------|:--------------------------:|:-------------:|:------------------:|
| Reads its own prior comments before re-reviewing | ✅ | ❌ ("not visible to Copilot") | ✅ |
| Reads user replies on its own comments | ✅ | ❌ | ✅ |
| Classifies prior threads as resolved / outstanding / outdated | ✅ | ❌ | ❌ (free-form) |
| Drops new comments overlapping classified threads | ✅ | ❌ ("may repeat the same comments again") | ⚠️ (single-comment model side-steps the issue) |
| Auto-closes resolved threads | ✅ | ❌ ("doesn't track resolved threads") | ❌ |
| Treats user dismissal / `Resolve conversation` as a signal | ❌ (planned) | ❌ | ❌ |

> **Notable**: The published Copilot docs explicitly state that on re-review, Copilot "may repeat the same comments again, even if they have been dismissed with the 'Resolve conversation' button or downvoted" and that "any comments you add to Copilot's review comments will be visible to humans, but they won't be visible to Copilot, and Copilot won't reply." Code Guru's PR #130 work fixes both of those failure modes.

### Context the model receives

| Feature | Code Guru | GitHub Copilot | Claude Code Action |
|---------|:---------:|:-------------:|:------------------:|
| Diff hunks of changed files | ✅ | ✅ | ✅ |
| Surrounding context lines (beyond hunk) | ❌ | ⚠️ (full project context gathering) | ✅ (full repo clone) |
| Full file contents | ⚠️ (only via trivial detector adapters' `FileContentFetcher`) | ⚠️ | ✅ |
| Repo-wide cross-file analysis | ❌ | ✅ | ✅ |
| PR description / linked issues | ⚠️ (title, branches, description, commit count — linked issues not yet parsed) | ✅ | ✅ |
| Commit history awareness | ⚠️ (commit count as an assembly signal; no per-commit messages) | ⚠️ | ✅ |
| Related PR / past review awareness | ⚠️ (only this PR's threads) | ⚠️ ("Copilot Memory" preview) | ❌ |

### Action capabilities

| Feature | Code Guru | GitHub Copilot | Claude Code Action |
|---------|:---------:|:-------------:|:------------------:|
| Edit files / propose patches | ❌ | ⚠️ ("pass to Copilot cloud agent" preview) | ✅ |
| Commit fixes to the PR branch | ❌ | ⚠️ (preview) | ✅ |
| Open new branches & PRs | ❌ | ⚠️ (preview) | ✅ |
| Run shell / build / tests | ❌ | ❌ | ⚠️ (allowlisted via `allowed_tools`) |
| Read CI / Actions logs | ❌ | ❌ | ✅ (with `actions: read`) |
| MCP server tool extensions | ❌ | ❌ | ✅ |
| Auto-merge approved PRs | ✅ (trivial fast path only) | ❌ | ❌ |
| Cross-PR memory | ❌ | ⚠️ (Copilot Memory preview, Pro/Pro+) | ❌ |

### Customisation

| Feature | Code Guru | GitHub Copilot | Claude Code Action |
|---------|:---------:|:-------------:|:------------------:|
| Custom rules per repo | ✅ (Markdown directory + frontmatter `paths`) | ✅ (`copilot-instructions.md`, `applyTo` glob) | ✅ (workflow `prompt`) |
| Custom system prompt | ❌ (hardcoded template) | ❌ | ✅ |
| Path-glob-targeted instructions | ✅ (`paths:` in frontmatter) | ✅ | ⚠️ (per-workflow) |
| Per-org policy | ⚠️ (config file shared) | ✅ (org policies) | ❌ |
| Auto-skip generated / lockfiles | ❌ | ✅ (`package.json`, `Gemfile.lock`, `*.svg`, log files) | ⚠️ (manual) |

### Operations

| Feature | Code Guru | GitHub Copilot | Claude Code Action |
|---------|:---------:|:-------------:|:------------------:|
| Webhook deduplication across pods | ✅ (in-memory + K8s Lease) | — | — |
| Graceful shutdown / drain | ✅ | — | — |
| Self-update (`code-guru self-update`) | ✅ | — | — |
| Native review verdict on submission | ✅ | ✅ | ❌ |
| Configurable timeout / retry | ⚠️ (timeouts only) | ❌ | ⚠️ |
| Model fallback chain (e.g. Anthropic → OpenAI on 5xx) | ❌ | — (managed) | ❌ |

---

## What Code Guru does best

These are the dimensions on which Code Guru ships ahead of both peers.

1. **Multi-vendor (GitHub + Azure DevOps).** Both Copilot and Claude Code Action are GitHub-only. Code Guru supports both providers behind one configuration via [`gitforge`](https://github.com/rios0rios0/gitforge).
2. **Backend-agnostic.** Switch between Anthropic Messages API, OpenAI Chat Completions, and Claude Code CLI per deployment. Copilot is GitHub-managed (no choice); Claude Code Action is Claude-only.
3. **Resolution-aware re-review.** After PR #130, Code Guru classifies every prior bot thread as `resolved` / `outstanding` / `outdated`, posts one short reply per thread, and auto-closes the resolved ones. Copilot explicitly does not do this. Claude Code Action does not surface the concept either — its single-comment model side-steps the problem rather than solving it.
4. **Trivial PR fast path.** Built-in detectors (`docs-only`, `bump-go`, `bump-node`, `bump-python`, `update-go`, `update-node`, `update-python`) skip the LLM entirely and (optionally) auto-merge. Neither peer offers a comparable token-cost optimisation.
5. **Native PR verdict submission.** Copilot supports this; Claude Code Action explicitly cannot. Code Guru maps `approve` / `request_changes` / `comment` / `waiting_for_author` to the platform's reviewer panel.
6. **Multi-pod operations primitives.** Kubernetes-Lease cross-pod webhook dedup, bounded async worker pool, graceful drain. Neither peer ships these because they are hosted (Copilot) or single-shot (Claude Code Action).
7. **Cobra CLI + webhook server + Lambda + Azure Functions entry points.** One binary, several deployment shapes.

---

## Feature gaps and improvement backlog

Each gap below names the missing capability, the peer that has it, and a suggested implementation path. Items are tagged by impact (P0 = highest user value).

### P0 — High-impact gaps

#### 1. GitHub `suggestion` blocks for one-click apply

**Where peers stand**: Copilot natively renders inline GitHub suggestion blocks. Claude Code Action goes one step further by committing the change.

**Code Guru today**: `entities.ReviewComment.Suggestion` exists in the domain model but `postComments` never wraps it in a ` ```suggestion ` Markdown fence, so the LLM's suggested replacements are dropped on the floor.

**Suggested implementation**:
- Update `postComments` (and any helper that renders inline bodies) to append:
  ```markdown
  ```suggestion
  <suggestion text>
  ```
  ```
  when `Suggestion != ""` AND the platform is GitHub. (Azure DevOps does not render the suggestion fence — keep it gated on provider type.)
- Update the prompt schema example so the LLM knows that `suggestion` should be a literal replacement for the line(s) at `line` / `end_line`.

#### 2. Surrounding-file context, not just the diff hunk

**Where peers stand**: Copilot uses "full project context gathering". Claude Code Action has the entire repo cloned.

**Code Guru today**: Only the unified diff is sent to the model. Findings often miss that the surrounding 30 lines already handle the case the bot wants to flag — driving the false-positive rate that the resolution-aware path now has to mop up after the fact.

**Suggested implementation**:
- Extend `entities.FileDiff` with an optional `Context string` field.
- Add a setting `ai.context_window_lines: 50` (default 0 = current behaviour).
- In `buildDiffs`, when the window is non-zero, fetch the file content through the existing `forgeEntities.FileAccessProvider` (already used by trivial detectors) and inject `±N` lines of context around each hunk.
- Cap total prompt size; on overflow, fall back to diff-only.

#### 3. Auto-skip uninteresting files

**Where peers stand**: Copilot explicitly skips `package.json`, `Gemfile.lock`, log files, SVG files.

**Code Guru today**: The LLM sees lockfiles and burns tokens "reviewing" generated content.

**Suggested implementation**:
- Add `ai.skip_paths: []string` to settings (default list: `**/package-lock.json`, `**/yarn.lock`, `**/pnpm-lock.yaml`, `**/poetry.lock`, `**/go.sum`, `**/*.svg`, `**/*.min.js`, `**/*.snap`, `**/CHANGELOG.md`, `**/dist/**`, `**/vendor/**`).
- Filter `diffs` in `buildDiffs` before prompt assembly. Surface dropped paths in the completion annotation footer so the operator sees what was excluded.

#### 4. Streaming / live progress comment

**Where peers stand**: Claude Code Action posts one comment that updates with a checklist as the model works through its plan.

**Code Guru today**: A static "reviewing" marker is posted at the start; the next signal is the completion annotation 1-10 minutes later. On a slow review the author cannot tell whether the bot is alive.

**Suggested implementation**:
- Replace the marker with a "progress" comment whose ID is captured (`PostPullRequestComment` already returns one on Azure DevOps; extend the GitHub provider).
- Stream key milestones (`fetched diffs`, `loaded N rules`, `calling AI`, `parsed response`, `dropping K duplicates`) by editing the existing comment body. Use the new `gitforge.UpdatePullRequestComment` (does not exist yet — depends on a gitforge follow-up).
- Fall back to the static marker on providers that do not support edits.

#### 5. Per-repo system-prompt overrides

**Where peers stand**: Claude Code Action lets the workflow author the entire prompt. Copilot honours `.github/copilot-instructions.md` and path-targeted `.github/instructions/*.instructions.md`.

**Code Guru today**: The system prompt is hardcoded in `internal/support/prompt_builder.go`. Repos cannot tune voice, severity gating, or focus areas without forking Code Guru.

**Suggested implementation**:
- Extend the rules loader to recognise a special filename (`.code-guru/system.md` or `system.md` at the rules root) that is **prepended** to the system prompt as a "repository-specific guidance" block, kept separate from the rules block.
- Document the precedence: system template → repo guidance → rules block → schema instructions.

### P1 — Medium-impact gaps

#### 6. Operator-gated fix commits (`@code-guru fix`)

**Where peers stand**: Claude Code Action implements changes and pushes commits; Copilot has a preview "pass to Copilot cloud agent" feature.

**Code Guru today**: Read-only — the bot can identify a fix but cannot apply it.

**Suggested implementation**:
- Add a slash-command parser to the comment-event webhook: `@code-guru fix <thread-id|file:line>` triggers a follow-up job that asks the LLM for a unified diff scoped to that anchor, applies it via gitforge to the PR's source branch, and replies on the thread with the commit URL.
- Gate behind a new `Settings.FixOnRequest` flag (default `false`) because cross-system writes deserve explicit opt-in.

#### 7. CI/checks awareness

**Code Guru today**: `gitforge.GetPullRequestCheckStatus` exists but is unused.

**Suggested implementation**:
- Read the check status before the review starts. When checks are red, prepend a sentence to the user prompt: "CI is currently failing — focus on whether the diff explains the failure".
- Surface the check state in the completion annotation so the PR author sees "CI: ❌ failing" alongside the verdict.

#### 8. PR description and linked-issue context — ✅ SHIPPED (description + commit count)

**Where peers stand**: Copilot and Claude Code Action both pull the PR description.

**Code Guru today**: The PR's description and commit count are fetched through the `prmetadata` fetcher registry (GitHub / Azure DevOps REST) and rendered — together with the title and branch names already in the prompt header — as an intent-context section that instructs the model to verify the diff against the stated intent and flag scope creep. Best-effort, bounded to 16 KiB, escape-proofed, opt-out via `ai.pr_metadata: false`.

**Remaining slice**:
- Parse `Closes #N` / `AB#N` / `JIRA-N` markers from the description; fetch the linked issue / work item and surface it as a separate prompt block so the model can check the PR actually fulfils the ticket (Qodo Merge's "ticket compliance" and CodeRabbit's "linked-issue assessment" are the reference implementations).

#### 9. Conversation continuation without explicit `@code-guru`

**Where peers stand**: Claude Code Action stays in the thread once invoked. Copilot does not engage at all.

**Code Guru today**: Only `@code-guru` (with word-boundary check) re-triggers a review. Replies on bot threads are silently ignored.

**Suggested implementation**:
- Add a per-thread "ongoing dialogue" mode: if the most recent comment on a bot-rooted thread is from a non-bot user AND was posted in the last `Settings.ReplyDialogueWindow` (default 30 min), treat it as an implicit `@code-guru` for that single thread.
- Restrict the resulting review to that one thread (use the existing `ThreadResolution` shape) so it does not re-run the whole PR.

#### 10. Severity-based routing

**Code Guru today**: `Severity` is captured in the response and rendered in the body, but every comment lands on the PR.

**Suggested implementation**:
- Add `ai.severity_filter: error` (default `info` = post everything) so operators can choose to post only errors inline and roll warnings/info into a digest paragraph in the completion annotation.
- Add a separate "post info findings as a single PR-wide comment" mode for noise-sensitive teams.

#### 11. Token-cost reporting

**Code Guru today**: No visibility into cost per review.

**Suggested implementation**:
- Capture per-call `usage.input_tokens` / `usage.output_tokens` from each backend (Anthropic and OpenAI return these; Claude CLI's `--output-format json` exposes them as `usage` too).
- Emit per-PR cost telemetry as a debug log line and an optional footer in the completion annotation: `_review used 18,420 in / 1,204 out tokens._`.

#### 12. Model fallback chain

**Code Guru today**: A single backend is configured. A 5xx from the upstream means the review fails.

**Suggested implementation**:
- Wrap the active backend in a `FallbackReviewer` that retries on the secondary backend on transient errors (429, 5xx, network).
- Keep the chain operator-controlled: `ai.fallback: ['anthropic', 'openai']`.

### P2 — Lower-impact / nice-to-have

| # | Feature | Notes |
|---|---------|-------|
| 13 | Cross-PR repository memory (Copilot Memory style) | Track recurring patterns (e.g. "this team always uses `errors.Is`") so first-pass reviews on new PRs are warmer. Vector store TBD. |
| 14 | GitLab support | New `gitforge` provider; once that lands, code-guru gets it for free. |
| 15 | Bitbucket Cloud / Server support | Same as above. |
| 16 | Image / screenshot understanding from PR description | Multimodal model on Anthropic / OpenAI; gate by config because cost rises. |
| 17 | CWE / CVE lookup for security findings | Optional enrichment step; cross-references finding against an offline DB. |
| 18 | Coverage delta detection | Parse coverage XML if present in the PR; flag uncovered new lines. |
| 19 | Slack / Teams / email notification adapter | "Verdict + summary" digest to a channel after every review. |
| 20 | SARIF output mode for CI | Same finding stream, machine-readable. |
| 21 | Digest mode | Single PR-wide comment instead of per-line. Useful for low-noise teams. |
| 22 | Larger-than-context-window diffs | Chunked multi-pass review with shared system prompt. |
| 23 | Replace `make lint` script crash on non-x86 hosts | Out of scope for code-guru itself but tracked because contributors hit it. |

---

## Out of scope (intentional non-goals)

These are features the peers have that Code Guru deliberately does NOT plan to add.

| Non-goal | Why |
|----------|-----|
| Editor / IDE integration | Out of scope — Code Guru is a server-side reviewer, not a coding assistant. |
| Becoming a CI runner | Code Guru reviews; CI runs tests. We integrate via `GetPullRequestCheckStatus`, we do not replace pipelines. |
| Hosted SaaS | Code Guru is meant to be self-hosted so the PR diff and reviewer rules never leave the operator's network. |
| Generic "ask-the-bot" Q&A | The mention path is for re-review, not free-form chat. Use the `claude` CLI directly for that. |

---

## Roadmap rollup

A suggested ordering for shipping the backlog. Each row is independent, so contributors can pick from the top of any tier without waiting for predecessors.

| Tier | Items |
|------|-------|
| **Now** (next 2-3 PRs) | #1 suggestion blocks · #3 auto-skip uninteresting files · #11 token-cost reporting |
| **Next** (1 month) | #2 surrounding-file context · #4 streaming progress comment · #5 per-repo system-prompt overrides · #8 linked-issue context (description + commit count already shipped) |
| **Later** (1 quarter) | #6 fix commits · #7 CI awareness · #9 conversation continuation · #10 severity-based routing · #12 model fallback chain |
| **Backlog** | Everything in the P2 table |

---

## How to contribute against this list

1. Pick an item; open an issue in this repo titled `feat(<area>): <item summary>` and link back to the line in this document.
2. Branch using the project's [Git Flow conventions](../CONTRIBUTING.md): `feat/<short-hyphenated-name>` for new capabilities, `fix/<...>` for parity fixes.
3. Keep each PR scoped to ONE row in the backlog. Cross-cutting refactors that span several rows are fine but should land in a separate `refactor/...` PR before the feature work.
4. Update the relevant cell in the feature matrix above as part of the PR so this document does not drift out of date.

---

_This document is a living comparison and should be reviewed every quarter. The peer products evolve fast — entries marked ⚠️ in the matrix above are the most likely to flip to ✅ between revisions._
