SCRIPTS_DIR ?= $(HOME)/Development/github.com/rios0rios0/pipelines
-include $(SCRIPTS_DIR)/makefiles/common.mk
-include $(SCRIPTS_DIR)/makefiles/golang.mk

VERSION ?= $(shell git describe --tags --abbrev=0 2>/dev/null | sed 's/^v//' || echo "dev")
LDFLAGS := -X main.version=$(VERSION)

.PHONY: build debug run install

build:
	mkdir -p bin && rm -rf bin/code-guru
	go mod tidy
	go build -ldflags "$(LDFLAGS) -s -w" -o bin/code-guru ./cmd/code-guru

debug:
	mkdir -p bin && rm -rf bin/code-guru
	go build -gcflags "-N -l" -ldflags "$(LDFLAGS)" -o bin/code-guru ./cmd/code-guru

run:
	go run ./cmd/code-guru

install:
	$(MAKE) build
	mkdir -p ~/.local/bin
	cp -v bin/code-guru ~/.local/bin/code-guru
