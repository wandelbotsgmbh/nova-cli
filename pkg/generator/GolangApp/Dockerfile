# Builds natively on the build machine and cross-compiles for the target platform.
FROM --platform=$BUILDPLATFORM docker.io/library/golang:1.27.4 AS builder

ARG TARGETOS TARGETARCH

WORKDIR /app

ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download

COPY static static
COPY internal internal
COPY main.go .

RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build go build -o app .

FROM scratch

EXPOSE 8080

# Copy the necessary CA certificates for HTTPS (if required)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/static static
COPY --from=builder /app/app .

ENTRYPOINT ["/app"]
