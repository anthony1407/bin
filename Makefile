GO      ?= go
IMAGE   ?= bin:dev
VERSION ?= dev

.PHONY: build test vet image integration crash check

## build: compile the server into dist/
build:
	$(GO) build -trimpath -ldflags "-s -w -X bin/internal/buildinfo.Version=$(VERSION)" \
		-o dist/bin$(shell $(GO) env GOEXE) ./cmd/bin

## test: unit and end-to-end tests (no Docker needed)
test:
	$(GO) test ./...

## vet: static checks and formatting
vet:
	$(GO) vet ./...
	@test -z "$$(gofmt -l cmd internal test)" || (gofmt -l cmd internal test; echo "run gofmt -w"; exit 1)

## image: build the container image used by the integration tests
image:
	docker build -t $(IMAGE) --build-arg VERSION=$(VERSION) .

## integration: real aws-cli and rclone against the image (needs Docker)
integration: image
	BIN_IMAGE=$(IMAGE) bash test/integration/run-all.sh

## crash: SIGKILL durability tests (needs Docker and Go)
crash: image
	BIN_IMAGE=$(IMAGE) bash test/integration/crash.sh buckets
	BIN_IMAGE=$(IMAGE) bash test/integration/crash.sh objects

## check: everything
check: vet test integration crash
