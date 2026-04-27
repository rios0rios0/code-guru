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
ENTRYPOINT ["/code-guru"]
CMD ["serve"]
