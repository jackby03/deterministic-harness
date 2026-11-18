.PHONY: all build test lint verify fmt clean

GO ?= go
GOFLAGS ?= -trimpath

all: build verify

build:
	@mkdir -p bin
	CGO_ENABLED=0 $(GO) build $(GOFLAGS) -o bin/ ./cmd/...

test:
	CGO_ENABLED=0 $(GO) test ./...

# The registry's `lint` gate runs the same command.
lint:
	CGO_ENABLED=0 golangci-lint run ./...

# Runs the repository's own profile. Exits non-zero unless the tier is green.
verify:
	CGO_ENABLED=0 $(GO) run ./cmd/harness-verify -registry gates.yaml -tier 2 -backend host \
		-out .artifacts/verify-report.json

fmt:
	gofmt -s -l .

clean:
	rm -rf bin .artifacts
