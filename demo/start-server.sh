#!/bin/sh
# Starts an HTTP server on the demo host "devbox", on port $1, serving
# /tmp (where the web screenshots' traffic comes from).
set -eu
here=$(cd "$(dirname "$0")" && pwd)
docker compose -f "$here/../test/e2e/compose.yaml" exec -d -u pfwd -w /tmp ss python3 -m http.server "$1" --bind 127.0.0.1
