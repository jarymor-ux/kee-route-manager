FROM golang:1.27.3-bookworm@sha256:972b4110ba0f47539a975c31e52c64765e1ff19f2d74469ae4a595c6676e80a5
RUN apt-get update -qq && apt-get install -y -qq python3 curl openssl shellcheck qemu-user && rm -rf /var/lib/apt/lists/*

# Runtime container tests intentionally run with --network none. Download module
# dependencies while the image is built so later go build/test commands are
# satisfied entirely from the image's module cache.
COPY go.mod go.sum /tmp/krm-mod/
RUN cd /tmp/krm-mod && go mod download

# CI bind-mounts its runner-owned checkout here; keep Go VCS stamping enabled.
RUN git config --system --add safe.directory /src
WORKDIR /src
