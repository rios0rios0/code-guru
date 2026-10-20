<h1 align="center">Agentic reviews and the Kubernetes Job executor</h1>

> **TL;DR:** `ai.claude.mode: agentic` makes Code Guru check the pull request out and run Claude Code as a
> read-only agent over the whole repository — six review passes, a confidence filter, one thread per finding and a
> report of what was checked and what was dropped. `server.executor: kubernetes` makes the webhook server run each of
> those reviews in its own Kubernetes Job, created from a template you own, at most `max_concurrent_jobs` at a time,
> deleted when it finishes.

## Contents

- [Why](#why)
- [The agentic review](#the-agentic-review)
- [Configuration](#configuration)
- [The sandbox](#the-sandbox)
- [The Kubernetes Job executor](#the-kubernetes-job-executor)
- [Example manifests](#example-manifests)
- [Operating it](#operating-it)
- [Requirements](#requirements)

## Why

The default (`diff`) review sends the model the hunks of a pull request and reads a JSON answer back in one
tool-less turn. It is fast and cheap, but the model sees nothing the hunks do not show: not the rest of the changed
file, not the caller of the function that changed, not the commit that fixed the same bug last year, not the
repository's own review checklist. The agentic review closes that gap the way a human reviewer would — by reading the
repository — at the cost of minutes instead of seconds.

Running that review inside the webhook server would put a long, memory-hungry agent next to every other review, with
the server's credentials in reach. The Kubernetes executor moves each one into a disposable pod instead.

## The agentic review

For each pull request the review:

1. **Checks the pull request out** into a fresh workspace: the pull request's head and the target branch fetched
   with git, the head checked out detached, the merge-base with the target branch resolved. On GitHub the head always
   comes from the `refs/pull/<id>/head` ref, which is right for a fork too — fetching the source branch by name would
   pick up a different branch of the same name on the target repository (`fork:main` into `main` is the usual case).
   Whatever the provider, a checkout whose changed files share nothing with the files the provider lists for the pull
   request is refused rather than reviewed. The provider token reaches git only through an environment-scoped
   `http.<host>.extraHeader`, never the command line or `.git/config`, and only for the provider's own host.
   Symlinks are checked out as plain files.
2. **Prepares a sandboxed Claude Code configuration** next to the checkout (never inside it): the operator's
   engineering rules — the same per-language selection the diff review uses — go into the system prompt, the
   operator's skills and commands are installed with their frontmatter rebuilt from an allowlist of harmless keys
   (or not at all, when it cannot be read with certainty), and the bot writes the settings file itself.
3. **Runs Claude Code headless as a read-only agent** in the checkout, with a prompt that asks for six sequential
   passes:
   1. *Guidelines compliance* — the repository's `AGENTS.md` / `CLAUDE.md` (root and changed directories),
      `CONTRIBUTING.md`, its own code-review skill (`.github/skills/code-review/SKILL.md`,
      `.claude/skills/code-review/SKILL.md`, `REVIEW.md`), and the operator's rules;
   2. *Bug scan* — whole changed files, not just hunks;
   3. *Git history* — `git log` / `git blame` on the touched code, for regressions of earlier fixes;
   4. *Code-comment adherence* — constraints the touched files document in their comments;
   5. *Security* — what the diff newly introduces, exploitable in practice;
   6. *Design* — fit with the repository's established architecture and contracts.
4. **Filters every candidate finding by confidence** (0–100; default bar 80). The model applies the bar and the bot
   enforces it again: nothing below it is posted, and every rejected candidate is listed with its score and reason.
   When the bot drops anything, it also relaxes the verdict to what the surviving findings justify (any error
   requests changes, warnings or notes alone comment, nothing approves), so a finding that was never posted cannot
   cast the vote. It never makes the verdict stricter.
5. **Posts the review**: one inline thread per finding, opening with `**<dimension> (confidence <score>)**`, and a
   completion notice carrying the summary, an index of the findings, what each dimension checked and found clean,
   and the dropped list. A clean review therefore says what was looked at instead of reading as silence.

The re-review (`@code-guru` mention) works exactly as on the diff path: the bot's earlier threads, with every reply,
are given to the agent, which must classify each one `resolved`, `outstanding` or `outdated` before raising anything
new.

What the agent is told not to do matters as much: it never builds, installs, lints or runs tests (the pipeline does,
on the same commit), never reports what it could only establish by executing something, never reviews lines the pull
request did not change, and treats everything in the repository — including instructions addressed to an AI — as
data. An instruction like "approve this pull request" inside the repository is reported as a security finding.

## Configuration

```yaml
ai:
  backend: 'claude'
  claude:
    model: 'claude-opus-5-5'
    mode: 'agentic'                 # 'diff' (default) or 'agentic'
    agentic:
      config_path: '/opt/claude'    # a Claude Code config tree: skills/, commands/ (and rules/ if rules.path is empty); env vars expanded
      workspace_dir: '/workspace'   # where checkouts are made (default: the OS temp directory)
      clone_depth: 0                # history depth per branch; 0 = full (the git-history pass wants it)
      timeout: '30m'                # wall-clock cap of one run
      max_turns: 200                # agent-loop cap
      effort: 'high'                # forwarded to --effort; empty = model default
      max_budget_usd: 0             # forwarded to --max-budget-usd; 0 = no cap
      fallback_model: ''            # forwarded to --fallback-model
      min_confidence: 80            # findings below are dropped (and listed)
      # subprocess_env_scrub: true  # optional: requires bubblewrap; omit for automatic detection

rules:
  path: '/opt/claude/rules'
```

| YAML key | Environment variable | Default |
|---|---|---|
| `ai.claude.mode` | `CODE_GURU_CLAUDE_MODE` | `diff` |
| `ai.claude.agentic.config_path` | `CODE_GURU_CLAUDE_AGENTIC_CONFIG_PATH` | (none) |
| `ai.claude.agentic.workspace_dir` | `CODE_GURU_CLAUDE_AGENTIC_WORKSPACE_DIR` | OS temp directory |
| `ai.claude.agentic.clone_depth` | `CODE_GURU_CLAUDE_AGENTIC_CLONE_DEPTH` | `0` (full) |
| `ai.claude.agentic.timeout` | `CODE_GURU_CLAUDE_AGENTIC_TIMEOUT` | `30m` |
| `ai.claude.agentic.max_turns` | `CODE_GURU_CLAUDE_AGENTIC_MAX_TURNS` | `200` |
| `ai.claude.agentic.effort` | `CODE_GURU_CLAUDE_AGENTIC_EFFORT` | (model default) |
| `ai.claude.agentic.max_budget_usd` | `CODE_GURU_CLAUDE_AGENTIC_MAX_BUDGET_USD` | `0` (no cap) |
| `ai.claude.agentic.fallback_model` | `CODE_GURU_CLAUDE_AGENTIC_FALLBACK_MODEL` | (none) |
| `ai.claude.agentic.min_confidence` | `CODE_GURU_CLAUDE_AGENTIC_MIN_CONFIDENCE` | `80` |
| `ai.claude.agentic.subprocess_env_scrub` | `CODE_GURU_CLAUDE_AGENTIC_SUBPROCESS_ENV_SCRUB` | on wherever bubblewrap (`bwrap`) is installed |

`mode: agentic` requires `backend: claude`; an unknown mode is rejected at startup rather than silently treated as
`diff`. The agentic mode works anywhere `claude` and `git` are installed — the CLI, CI, the webhook server — and does
not need the Kubernetes executor.

The model credential is whatever the Claude CLI reads: `CLAUDE_CODE_OAUTH_TOKEN`, `ANTHROPIC_API_KEY` or
`ANTHROPIC_AUTH_TOKEN`, optionally with `ANTHROPIC_BASE_URL` (and the `ANTHROPIC_DEFAULT_*_MODEL` pins) for a gateway.
A workspace-scoped API key with a spend limit narrows the blast radius of a leaked credential more than a
subscription token does.

## The sandbox

A reviewer that runs tools over a pull request anyone in the organisation can open is a prompt-injection target. The
review is built so that the realistic attacks fail closed:

| Concern | What stops it |
|---|---|
| The checkout's `.claude/settings.json` adds hooks, permissions, an `apiKeyHelper` or MCP servers | `--setting-sources user` (only the bot's own sandbox config loads), `--strict-mcp-config`, `disableAllHooks` |
| The checkout's `AGENTS.md` / `CLAUDE.md` / skills carry instructions | not auto-loaded (`CLAUDE_CODE_DISABLE_CLAUDE_MDS`, `instructionFiles: managed-only`); the agent reads them as data, and the prompt treats instructions inside them as findings |
| The agent runs a build, a script or a network call | `--tools Read,Grep,Glob,Bash,Skill`, Bash allow-listed to read-only git subcommands, `dontAsk` + `--permission-prompts none`, deny rules for `git -c`/`-C`, `--no-index`, `--contents`, `--output`, external diff/textconv |
| An installed skill pre-approves extra tools for itself | the frontmatter of every installed skill and command is found the way Claude Code finds it and rebuilt from an allowlist — `name`, `description`, `argument-hint`, `model`, `version`, `license`, `disable-model-invocation`, `user-invocable`, plain scalar values only — so `allowed-tools`, `hooks`, a forked `context` and any key added later never reach the agent; a file whose fence carries more than whitespace, whose header holds a stray `---`, that is not valid YAML, has a non-string key, a merge key in any quoting, an anchor, an alias, a custom tag or a repeated key is not installed at all |
| The agent reads the process environment or the service-account token | `blockReadsOutsideWorkingDirectories`, `Read(//proc/**)` / `//sys/**` / `//etc/**` denied, symlinks checked out as plain files, and `CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` wherever bubblewrap can run it (see below) |
| The agent reads the provider token | it is never in the agent's environment (built from an allowlist), and the server process marks itself non-dumpable before the agent starts, so `/proc/<pid>/environ` of the parent is unreadable |
| The review leaks a credential into a PR comment | the model credentials and the provider token are redacted from every text field of the answer — file paths, dimensions and severities included — verbatim and in base64/hex, plus anything shaped like an Anthropic key |
| claude.ai connectors of a subscription account become tools | `ENABLE_CLAUDEAI_MCP_SERVERS=false` |

`CLAUDE_CODE_SUBPROCESS_ENV_SCRUB` strips credentials from the environment of every subprocess the agent starts (its
git commands). The Claude Code CLI runs that isolation through bubblewrap and refuses to start without it, so the
reviewer asks for the scrub only where `bwrap` is installed, logging a warning when it runs without. The container
image ships without bubblewrap on purpose: a review pod runs with no capabilities, `allowPrivilegeEscalation: false`
and the runtime's default seccomp profile, which leaves bubblewrap no user namespace to create, so installing it
would only move the failure to the first git command. Without the scrub the agent's git commands inherit the model
credential, and the other layers above still hold: Bash runs only read-only git subcommands, `/proc` cannot be
read, and the credential is redacted from everything posted. `subprocess_env_scrub: true` requires the scrub (a run
without bubblewrap then fails); `false` never asks for it.

The residual risk is the model credential itself, which the CLI process has to hold. Redaction covers the encodings
an instruction can ask for without tools; an egress-restricted NetworkPolicy (below) removes every channel but the
model API and the provider.

## The Kubernetes Job executor

```yaml
server:
  executor: 'kubernetes'            # 'inprocess' (default) or 'kubernetes'
  kubernetes:
    job_template: '/etc/code-guru/review-job.yaml'
    namespace: ''                   # default: the server pod's own namespace
    max_concurrent_jobs: 3          # fleet-wide cap on running review Jobs
    poll_interval: '15s'            # scheduler cadence
```

| YAML key | Environment variable | Default |
|---|---|---|
| `server.executor` | `CODE_GURU_SERVER_EXECUTOR` | `inprocess` |
| `server.kubernetes.job_template` | `CODE_GURU_SERVER_KUBERNETES_JOB_TEMPLATE` | required |
| `server.kubernetes.namespace` | `CODE_GURU_SERVER_KUBERNETES_NAMESPACE` | the pod's namespace |
| `server.kubernetes.max_concurrent_jobs` | `CODE_GURU_SERVER_KUBERNETES_MAX_CONCURRENT_JOBS` | `3` |
| `server.kubernetes.poll_interval` | `CODE_GURU_SERVER_KUBERNETES_POLL_INTERVAL` | `15s` |

### How it flows

1. A webhook arrives at the server as before: authentication, allow-lists, the per-PR dedup lease.
2. The server runs the **cheap gates in-process** — draft, review-once, no files, trivial detection (with its own
   verdict and auto-merge). Most deliveries end here: Azure DevOps alone sends two per new pull request and one per
   push, and none of those should cost a pod.
3. A pull request that still needs the AI gets **one review Job**, unless an unfinished one already exists for it —
   except that an `@code-guru` mention arriving while a review is **running** queues a re-review behind it, which
   starts after that review has posted and reads its threads. Only a re-review that is still **queued** absorbs a
   further mention: it has not checked anything out yet, so it will read the newest code anyway. The list-then-create
   runs under a per-pull-request lock, so two deliveries on two replicas do not both create a Job (an enqueue that
   cannot get the lock within ten seconds goes ahead anyway: a rare duplicate costs less than a dropped request). Every Job is
   created **suspended**.
4. The **scheduler** (in every server replica) releases suspended Jobs oldest-first while fewer than
   `max_concurrent_jobs` are running, never two of the same pull request at once (the queue moves past one that has
   to wait for its own pull request). Its passes are serialised across replicas by a `coordination.k8s.io` Lease:
   whether a slot is free depends on every Job in a listing, so two replicas planning at once from listings taken a
   moment apart could otherwise start one Job each into the same last slot. A replica that finds the lock held skips
   its pass. Inside a pass every write is still conditional on the Job's `resourceVersion`. A start the API server
   turns down (an admission policy rejecting the Job, say) hands its slot to the next Job in line, so one Job that
   can never start cannot hold the queue; a start that conflicts or gets no answer keeps its slot until the next
   pass, since that Job may have started after all. A pull request whose Job has to wait gets a "queued" notice with
   its position.
5. The Job's pod runs `code-guru review <pr-url> --job-name <job>` (plus `--mentioned` for a re-review): the gates
   again — the pull request may have changed while it waited — then the review, then it exits. The Job's name is
   stamped on the completion or failure notice it posts (`_Completed at <time> · review job <job>._`).
   `ttlSecondsAfterFinished` deletes the Job and its pod.
6. A pod that dies without reporting — deadline, out of memory, eviction — fails its Job; the scheduler notices,
   claims it with an annotation (exactly one replica does) and posts a "review job failed" notice on the pull request,
   unless a notice stamped with that Job's name is already there. Matching by name rather than by time is what keeps
   a re-review that dies seconds after the review ahead of it completed from going unreported. A "reviewing" notice
   is never left without a sequel, unless the provider also fails the server's own report: each report gets its own
   time limit and one retry, outside the scheduler lock and within a per-pass budget, so a burst of failures cannot
   starve the later ones.

Review pods scale from zero to `max_concurrent_jobs`: a per-PR pod exists only while its review runs, so there is no
idle minimum to keep warm. The always-on minimum is the server Deployment's own replica floor, scaled by its HPA as
before. Queued reviews survive server restarts — they are suspended Jobs in the API server, not in memory.

### The template contract

You own the pod: image, resources, volumes, security context, service account, node placement, environment and the
Job-level policies (`activeDeadlineSeconds`, `backoffLimit`, `podFailurePolicy`, `ttlSecondsAfterFinished`). The
executor sets only:

| Field | Value |
|---|---|
| `metadata.name` | `code-guru-review-<pr-hash>-<time>` (any `generateName`/`namespace` is dropped) |
| `metadata.labels`, `spec.template.metadata.labels` | `code-guru/review-job: "true"`, `code-guru/pull-request: <hash>`, `app.kubernetes.io/component: review`, `app.kubernetes.io/managed-by: code-guru` (your own labels are kept) |
| `metadata.annotations` | `code-guru/provider`, `organization`, `project`, `repository`, `repository-id`, `pull-request-id`, `pull-request-url`, `user-mentioned`, `github-installation-id` |
| `spec.suspend` | `true` (only the scheduler releases a Job) |
| `args` of the container named `review` (else the first) | `["review", "<pr-url>", "--job-name", "<job>"]`, plus `--mentioned` |
| `env` of that container | `CODE_GURU_GITHUB_INSTALLATION_ID` for GitHub App deliveries |
| `metadata.annotations["code-guru/enqueued-at"]` | the time the Job was queued, to the millisecond, which orders a burst |
| defaults when absent | `backoffLimit: 0`, `ttlSecondsAfterFinished: 900`, `activeDeadlineSeconds: 3600`, `restartPolicy: Never` |

`spec.parallelism` and `spec.completions` must be unset or `1`, and a template that sets either to anything else is
rejected at startup. Every pod runs the whole review, so a second pod would post every finding again, and the
scheduler counts Jobs, not pods, against `max_concurrent_jobs`.

A Job's deadline only runs while it is not suspended, so it bounds a review's run, not its wait in the queue.
Without one, a pod that never starts — an image it cannot pull, a node pool with no room — would hold a review slot
forever, which is why the executor sets one when the template does not. Keep it above
`ai.claude.agentic.timeout` plus the checkout and the posting.

Keep `ttlSecondsAfterFinished` well above `poll_interval`: a failed Job that Kubernetes deletes before the scheduler
has listed it cannot be reported on its pull request.

The pull request URL is built from validated names, never copied from the webhook payload. The template is parsed
and validated at startup, and the server proves it may take its Lease locks by creating, reading and deleting one of its own;
a broken template, a server without the Lease RBAC, or the executor configured outside a pod stops the server rather
than falling back to in-process reviews — or, worse, queueing every review behind a scheduler that can never run.

The review pod needs **no** Kubernetes API access (`automountServiceAccountToken: false`) and none of the server's
webhook secret — only the provider token and the model credential. It authenticates the way the server did for the
same delivery: when the pull request came through the GitHub App (`CODE_GURU_GITHUB_INSTALLATION_ID` is set) and the
pod has the App's credentials, it mints that installation's token and uses it even if a PAT is configured too, since
the installation is what received the webhook and has access to the repository. If the token cannot be minted, the
review fails rather than switching to the PAT, which is a different identity with different access.

## Example manifests

The server's RBAC grows by one rule. The dedup-lease rule stays as it is, and the executor's own locks — one Lease
around every scheduler pass, one per pull request around every enqueue — run on it too:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  namespace: code-guru
  name: code-guru-review-jobs
rules:
  - apiGroups: ['batch']
    resources: ['jobs']
    verbs: ['create', 'list', 'patch']
---
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  namespace: code-guru
  name: code-guru-review-jobs
subjects:
  - kind: ServiceAccount
    name: code-guru
    namespace: code-guru
roleRef:
  kind: Role
  name: code-guru-review-jobs
  apiGroup: rbac.authorization.k8s.io
```

A review Job template, mounted into the server (for example from a ConfigMap) at the `job_template` path:

```yaml
apiVersion: batch/v1
kind: Job
spec:
  activeDeadlineSeconds: 2400        # keep ai.claude.agentic.timeout below it
  backoffLimit: 0
  ttlSecondsAfterFinished: 900
  podFailurePolicy:
    rules:
      - action: Ignore               # a node drain or preemption retries without counting
        onPodConditions:
          - type: DisruptionTarget
  template:
    spec:
      restartPolicy: Never
      serviceAccountName: code-guru-review
      automountServiceAccountToken: false
      securityContext:
        runAsNonRoot: true
        runAsUser: 65532
        runAsGroup: 65532
        fsGroup: 65532
        seccompProfile:
          type: RuntimeDefault
      initContainers:
        - name: claude-config        # stage the rules and skills the review installs
          image: alpine/git:2.47.1
          command: ['sh', '-c']
          args:
            - git clone --depth 1 --branch generated https://github.com/rios0rios0/guide.git /tmp/guide
              && cp -R /tmp/guide/claude/. /claude/
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: ['ALL'] }
          volumeMounts:
            - { name: claude-config, mountPath: /claude }
            - { name: tmp, mountPath: /tmp }
      containers:
        - name: review
          image: ghcr.io/rios0rios0/code-guru:<version>
          env:
            - { name: CODE_GURU_BACKEND, value: claude }
            - { name: CODE_GURU_CLAUDE_MODEL, value: claude-opus-5-5 }
            - { name: CODE_GURU_CLAUDE_MODE, value: agentic }
            - { name: CODE_GURU_CLAUDE_AGENTIC_CONFIG_PATH, value: /claude }
            - { name: CODE_GURU_CLAUDE_AGENTIC_WORKSPACE_DIR, value: /workspace }
            - { name: CODE_GURU_CLAUDE_AGENTIC_TIMEOUT, value: 35m }
            - { name: CODE_GURU_CLAUDE_AGENTIC_EFFORT, value: high }
            - { name: CODE_GURU_RULES_PATH, value: /claude/rules }
            - name: CODE_GURU_PROVIDER_TOKEN
              valueFrom: { secretKeyRef: { name: code-guru-config, key: CODE_GURU_PROVIDER_TOKEN } }
            - name: CLAUDE_CODE_OAUTH_TOKEN
              valueFrom: { secretKeyRef: { name: code-guru-config, key: CLAUDE_CODE_OAUTH_TOKEN } }
          resources:
            requests: { cpu: 500m, memory: 1Gi }
            limits: { cpu: '2', memory: 4Gi }
          securityContext:
            readOnlyRootFilesystem: true
            allowPrivilegeEscalation: false
            capabilities: { drop: ['ALL'] }
          volumeMounts:
            - { name: workspace, mountPath: /workspace }
            - { name: claude-config, mountPath: /claude, readOnly: true }
            - { name: tmp, mountPath: /tmp }
            - { name: home, mountPath: /home/nonroot }
      volumes:
        - { name: workspace, emptyDir: { sizeLimit: 10Gi } }
        - { name: claude-config, emptyDir: { sizeLimit: 64Mi } }
        - { name: tmp, emptyDir: { medium: Memory, sizeLimit: 256Mi } }
        - { name: home, emptyDir: { medium: Memory, sizeLimit: 128Mi } }
```

And a NetworkPolicy that leaves review pods nothing but DNS and HTTPS:

```yaml
apiVersion: networking.k8s.io/v1
kind: NetworkPolicy
metadata:
  namespace: code-guru
  name: code-guru-review
spec:
  podSelector:
    matchLabels:
      code-guru/review-job: 'true'
  policyTypes: ['Ingress', 'Egress']
  egress:
    - to:
        - namespaceSelector:
            matchLabels: { kubernetes.io/metadata.name: kube-system }
      ports:
        - { protocol: UDP, port: 53 }
        - { protocol: TCP, port: 53 }
    - to:
        - ipBlock:
            cidr: 0.0.0.0/0
            except: ['10.0.0.0/8', '172.16.0.0/12', '192.168.0.0/16', '169.254.0.0/16']
      ports:
        - { protocol: TCP, port: 443 }
```

## Operating it

```bash
# every review Job, queued and running
kubectl -n code-guru get jobs -l code-guru/review-job=true

# queued (suspended) Jobs only
kubectl -n code-guru get jobs -l code-guru/review-job=true \
  -o jsonpath='{range .items[?(@.spec.suspend==true)]}{.metadata.name}{"\n"}{end}'

# the review log of one pull request's Job
kubectl -n code-guru logs job/<name>

# drop a queued review
kubectl -n code-guru delete job <name>
```

- A Job shows `Complete` when the review was posted (or a gate settled the pull request) and `Failed` when it was
  not: `review` exits non-zero on every failure. The scheduler then posts the "review job failed" notice — unless the
  pod already posted its own failure notice before exiting, which it does for every failure it can see (a timeout, a
  backend error), so a pull request gets one notice, never two. Every outcome notice ends with the name of the Job
  that produced it, which is also the Job whose logs to read.
- The executor's Leases are named `code-guru-review-job-scheduler-…` and `code-guru-review-job-enqueue-…`. A server
  replica that dies holding one blocks the others for at most a minute.
- `DEBUG=true` on the review pod logs the agent's raw answer (truncated) for diagnosis; it is never posted.
- A burst of `permission_denials` in the run statistics the pod logs means the agent asked for a tool the sandbox does
  not grant — usually harmless (the prompt says what it has), but worth a look after a CLI upgrade.

## Requirements

- The Claude Code CLI **2.1.280** or later (Opus 5.5 model ids; `--permission-prompts`, `--json-schema` and
  `--setting-sources` are older), and `git` on `PATH`. The published image ships both.
- Kubernetes **1.24** or later for the executor (Job `suspend` and `ttlSecondsAfterFinished`); `podFailurePolicy`
  in the example needs **1.31**.
- Pull requests from forks are reviewed on GitHub, where the head is fetched from `refs/pull/<id>/head`. Azure
  DevOps publishes no such ref, so a fork's source branch could only be fetched by name from the target repository,
  where a branch of the same name may be a different one: there, the pull request's `forkSource` is read first and a
  fork's pull request is refused before anything is cloned.
