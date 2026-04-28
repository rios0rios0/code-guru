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

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /out/code-guru /code-guru

EXPOSE 8080
USER nonroot:nonroot

# Probe the local /health endpoint via the binary itself. The distroless base
# image ships no shell, no curl, and no wget, so the binary doubles as its own
# healthcheck client. --start-period gives the listener time to bind; the per
# request timeout is shorter than HEALTHCHECK --timeout so the controller's
# error message is the one Docker captures.
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/code-guru", "health", "--timeout=4s"]

ENTRYPOINT ["/code-guru"]
CMD ["serve"]
