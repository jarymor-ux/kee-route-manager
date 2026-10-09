FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61
RUN apt-get update -qq && apt-get install -y -qq python3 curl openssl nftables iproute2 shellcheck && rm -rf /var/lib/apt/lists/*

# Runtime container tests intentionally run with --network none. Download module
# dependencies while the image is built so later go build/test commands are
# satisfied entirely from the image's module cache.
COPY go.mod go.sum /tmp/krm-mod/
RUN cd /tmp/krm-mod && go mod download

# CI bind-mounts its runner-owned checkout here; keep Go VCS stamping enabled.
RUN git config --system --add safe.directory /src
WORKDIR /src
