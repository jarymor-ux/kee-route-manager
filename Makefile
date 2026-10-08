SHELL := /bin/bash
.PHONY: fmt test vet check build release cross-build clean
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
	@set -e; build_version=$$(python3 scripts/build-version.py); for component in kee-route-managerd kee-route-manager-ui kee-route-managerctl kee-route-manager-launcher krm-release-tool; do CGO_ENABLED=0 go build -trimpath -ldflags "-X main.version=$$build_version" -o dist/$$component ./cmd/$$component; done
release:
	./scripts/build-release.sh
cross-build:
	./scripts/cross-build.sh
clean:
	rm -rf dist release
