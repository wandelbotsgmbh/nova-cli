#!/usr/bin/env bash
# The "script" for the asciinema recording embedded in the main README.
#
# This is a SIMULATION: it never runs the real `nova` binary. The commands are
# typed out for real pacing, but the output blocks are canned text matching the
# actual CLI's message formats (see README.md in this folder for how that was
# verified). Doing it this way means the clip never depends on a live instance,
# a container registry login, or build/push timing - a real end-to-end run of
# this flow takes several minutes; this takes seconds.
set -uo pipefail

TYPING_DELAY="${TYPING_DELAY:-0.035}"
# Prompt color is NOVA's brand violet (--wb-zero-gravity-primary-main, #8e56fc
# in @wandelbots/design-tokens), truecolor so it renders as-is regardless of
# terminal/svg-term profile.
PROMPT=$'\033[1m\033[38;2;142;86;252m$\033[0m '

type_line() {
  printf '%b' "$PROMPT"
  local text="$1" i
  for ((i = 0; i < ${#text}; i++)); do
    printf '%s' "${text:$i:1}"
    sleep "$TYPING_DELAY"
  done
  printf '\n'
}

# $1 = typed command, $2 = canned output (may be empty), $3 = pause after (default 1.1s)
step() {
  type_line "$1"
  sleep 0.25
  if [[ -n "$2" ]]; then
    printf '%s\n' "$2"
  fi
  printf '\n'
  sleep "${3:-1.1}"
}

step "nova instance create --name my-example-instance" \
"InstanceId: x7k2m9pq
Host: x7k2m9pq.instance.wandelbots.io
Waiting for instance to be in running state...
Waiting for core services to be ready..." 1.6

step "nova config set image-registry ghcr.io/my-org" "" 0.6

step "nova app create my-example-app -g golang_app" \
"Updated my-example-app/.env.local: access token refreshed
  NOVA_API=https://x7k2m9pq.instance.wandelbots.io
  CELL_NAME=cell
  BASE_PATH=/cell/my-example-app" 1.4

step "cd my-example-app" "" 0.4

step "nova app install" \
"Using container runtime: docker
[+] Building 12.4s (14/14) FINISHED
 => [internal] load build definition from Dockerfile
 => [build 1/4] FROM docker.io/library/golang:1.23-alpine
 => [build 2/4] COPY go.mod go.sum ./
 => [build 3/4] RUN go mod download
 => [build 4/4] RUN go build -o app .
 => exporting to image
 => => naming to ghcr.io/my-org/my-example-app:latest
The push refers to repository [ghcr.io/my-org/my-example-app]
latest: digest: sha256:3f9a1c8b2e7d4a05f1c9b6e2d8a47f31 size: 1372
Installing app 'my-example-app'..." 1.6

step "nova app list" "my-example-app" 1.8
