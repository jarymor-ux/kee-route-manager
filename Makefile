SHELL := /bin/bash
VERSION := $(shell cat VERSION)
BINARY := dist/kee-route-manager
RELEASE_TOOL := dist/krm-release-tool

.PHONY: fmt test vet check build release clean

fmt:
	gofmt -w $$(find cmd internal -name '*.go' -type f)

test:
	go test ./...

vet:
	go vet ./...

check:
	./scripts/check.sh

build:
	mkdir -p dist
	CGO_ENABLED=0 go build -trimpath -o $(BINARY) ./cmd/kee-route-manager
	CGO_ENABLED=0 go build -trimpath -o $(RELEASE_TOOL) ./cmd/krm-release-tool

release:
	./scripts/build-release.sh

clean:
	rm -rf dist release
