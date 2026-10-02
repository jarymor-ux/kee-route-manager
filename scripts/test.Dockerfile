FROM golang:1.26-bookworm@sha256:a688600ca24f8a4d3ca77f95b0dd40704a9fc787c826660eb7ba0b641b8b175d
RUN apt-get update -qq && apt-get install -y -qq python3 curl openssl nftables iproute2 shellcheck && rm -rf /var/lib/apt/lists/*
WORKDIR /src
