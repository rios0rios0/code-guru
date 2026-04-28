# syntax=docker/dockerfile:1.7

FROM golang:1.26-alpine AS builder
WORKDIR /src

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=docker
RUN CGO_ENABLED=0 GOOS=linux go build \
    -trimpath \
    -ldflags "-s -w -X main.version=${VERSION}" \
    -o /out/code-guru \
    ./cmd/code-guru

FROM debian:12-slim

# Pinning a Claude Code release rather than tracking the floating `stable`
# channel so version upgrades are an explicit, reviewable change. Bump in
# lockstep with the toolbox `image_tag`.
ARG CLAUDE_VERSION=2.1.89

# Install the Claude Code native binary to a system path so it survives the
# Kubernetes emptyDir mount on /home/nonroot/.claude (which masks anything the
# image puts under that subtree). The official installer drops the binary into
# `$HOME/.local/bin`, so we install with HOME=/opt/claude-install and then
# move the result onto PATH. `curl` is removed at the end of the same RUN to
# keep it out of the final image layer.
RUN set -eux; \
    apt-get update; \
    apt-get install -y --no-install-recommends ca-certificates curl libstdc++6; \
    groupadd --system --gid 65532 nonroot; \
    useradd --system --gid nonroot --uid 65532 --create-home \
        --home-dir /home/nonroot --shell /sbin/nologin nonroot; \
    mkdir -p /opt/claude-install; \
    HOME=/opt/claude-install bash -c \
        "curl -fsSL https://claude.ai/install.sh | bash -s ${CLAUDE_VERSION}"; \
    install -m 0755 /opt/claude-install/.local/bin/claude /usr/local/bin/claude; \
    rm -rf /opt/claude-install; \
    apt-get purge -y --auto-remove curl; \
    apt-get clean; \
    rm -rf /var/lib/apt/lists/*

# HOME drives where claude looks for `~/.claude/` (token cache, sessions,
# settings) and `~/.local/share/claude/` (version metadata). Keep the value
# stable across the image and the K8s manifest.
ENV HOME=/home/nonroot

COPY --from=builder /out/code-guru /usr/local/bin/code-guru

USER 65532:65532
WORKDIR /home/nonroot

EXPOSE 8080

# Probe the local /health endpoint via the Go binary itself.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/code-guru", "health", "--timeout=4s"]

ENTRYPOINT ["/usr/local/bin/code-guru"]
CMD ["serve"]
