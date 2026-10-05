FROM golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
RUN apt-get update -qq && apt-get install -y -qq python3 curl openssl nftables iproute2 shellcheck && rm -rf /var/lib/apt/lists/*

# Runtime container tests intentionally run with --network none. Download module
# dependencies while the image is built so later go build/test commands are
# satisfied entirely from the image's module cache.
COPY go.mod go.sum /tmp/krm-mod/
RUN cd /tmp/krm-mod && go mod download

# CI bind-mounts its runner-owned checkout here; keep Go VCS stamping enabled.
RUN git config --system --add safe.directory /src
WORKDIR /src
