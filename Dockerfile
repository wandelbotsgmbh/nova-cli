# build on the native platform, cross-compile for the target platform
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.3 AS builder

ARG FLAGS
ARG TARGETOS
ARG TARGETARCH

WORKDIR /app

ENV CGO_ENABLED=0

COPY go.mod go.sum ./

RUN --mount=type=cache,target=$GOPATH go mod download

COPY nova.go .
COPY pkg ./pkg

RUN --mount=type=cache,target=$GOPATH \
    : "${FLAGS:?please provide --build-args FLAGS.}" && \
    GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="${FLAGS}" -o nova .

FROM docker.io/library/alpine:3.24

# links the GHCR package to the repo (README, visibility, permissions)
LABEL org.opencontainers.image.source="https://github.com/wandelbotsgmbh/nova-cli"

WORKDIR /nova

COPY --from=builder /app/nova .

ENV PATH="/nova:$PATH"

# Set the entrypoint to the nova command
ENTRYPOINT ["nova"]
# Default command if none is provided
CMD ["--help"]
