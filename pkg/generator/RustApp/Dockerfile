# syntax=docker/dockerfile:1
# Builds natively on the build machine and cross-compiles for the target platform,
# so building the linux/amd64 image on an arm64 machine does not run rustc under emulation.
FROM --platform=$BUILDPLATFORM docker.io/library/rust:1.99-trixie AS builder

ARG TARGETARCH
WORKDIR /app

RUN set -eu; \
    case "$TARGETARCH" in \
      amd64) arch=x86_64 ;; \
      arm64) arch=aarch64 ;; \
      *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac; \
    echo "$arch-unknown-linux-gnu" > /target; \
    if [ "$(uname -m)" != "$arch" ]; then \
      apt-get update; \
      apt-get install -y --no-install-recommends "gcc-$(echo $arch | tr _ -)-linux-gnu" "libc6-dev-$TARGETARCH-cross"; \
      rm -rf /var/lib/apt/lists/*; \
    fi; \
    rustup target add "$(cat /target)"

COPY Cargo.toml Cargo.lock ./
COPY static static
COPY src src

RUN --mount=type=cache,target=/usr/local/cargo/registry \
    --mount=type=cache,target=/app/target \
    set -eu; \
    target="$(cat /target)"; \
    arch="${target%%-*}"; \
    if [ "$(uname -m)" != "$arch" ]; then \
      cc="$arch-linux-gnu-gcc"; \
      export "CARGO_TARGET_$(echo $target | tr a-z- A-Z_)_LINKER=$cc" "CC_$(echo $target | tr - _)=$cc"; \
    fi; \
    cargo build --release --locked --target "$target"; \
    cp "target/$target/release/app" /app/app

FROM gcr.io/distroless/cc-debian13:nonroot

EXPOSE 8080

COPY --from=builder /app/app /app

ENTRYPOINT ["/app"]
