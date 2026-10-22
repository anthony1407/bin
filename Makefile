GO      ?= go
IMAGE   ?= bin:dev
VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
COMMIT  ?= $(shell git rev-parse --short HEAD 2>/dev/null)
DATE    ?= $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS := -s -w -X bin/internal/buildinfo.Version=$(VERSION) \
           -X bin/internal/buildinfo.Commit=$(COMMIT) -X bin/internal/buildinfo.Date=$(DATE)

.PHONY: web build test vet image integration crash check

## web: build the panel into internal/webui/dist (needs Node; embedded by build)
web:
	cd web && npm ci --no-audit --no-fund && npm run build

## build: compile the server into dist/ (run make web first to include the panel)
build:
	$(GO) build -trimpath -ldflags "$(LDFLAGS)" -o dist/bin$(shell $(GO) env GOEXE) ./cmd/bin

## test: unit and end-to-end tests (no Docker needed)
test:
	$(GO) test ./...

## vet: static checks and formatting
vet:
	$(GO) vet ./...
	@test -z "$$(gofmt -l cmd internal test)" || (gofmt -l cmd internal test; echo "run gofmt -w"; exit 1)

## image: build the container image used by the integration tests
image:
	docker build -t $(IMAGE) --build-arg VERSION=$(VERSION) --build-arg COMMIT=$(COMMIT) --build-arg DATE=$(DATE) .

## integration: real aws-cli and rclone against the image (needs Docker)
integration: image
	BIN_IMAGE=$(IMAGE) bash test/integration/run-all.sh

## crash: SIGKILL durability tests (needs Docker and Go)
crash: image
	BIN_IMAGE=$(IMAGE) bash test/integration/crash.sh buckets
	BIN_IMAGE=$(IMAGE) bash test/integration/crash.sh objects

## check: everything
check: vet test integration crash
