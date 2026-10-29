FROM golang:1.24.2 AS builder

WORKDIR /app

ENV CGO_ENABLED=0 GOOS=linux GOARCH=amd64

COPY go.mod go.sum ./
RUN --mount=type=cache,target=$GOPATH go mod download

COPY static static
COPY internal internal
COPY main.go .

RUN --mount=type=cache,target=$GOPATH go build -o app .

FROM scratch

EXPOSE 8080

# Copy the necessary CA certificates for HTTPS (if required)
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /app/static static
COPY --from=builder /app/app .

ENTRYPOINT ["/app"]
