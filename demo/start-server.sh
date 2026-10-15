#!/bin/sh
# Starts an HTTP server on the demo host "devbox", on port $1.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
docker compose -f "$here/../test/e2e/compose.yaml" exec -d -u pfwd ss python3 -m http.server "$1" --bind 127.0.0.1
