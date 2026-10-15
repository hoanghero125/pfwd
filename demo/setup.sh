#!/bin/sh
# Starts the end-to-end tests' sshd container as "devbox" for the README
# demos: a fresh key, an ssh config entry, and a few servers already
# listening there. CI's demo workflow runs it; it needs Docker.
set -eu
here=$(cd "$(dirname "$0")" && pwd)
mkdir -p "$HOME/.ssh" "$HOME/.config/pfwd"
chmod 700 "$HOME/.ssh"
rm -f "$HOME/.ssh/pfwd_demo" "$HOME/.ssh/pfwd_demo.pub"
ssh-keygen -q -t ed25519 -N '' -C pfwd-demo -f "$HOME/.ssh/pfwd_demo"
PFWD_E2E_AUTHORIZED_KEY=$(cat "$HOME/.ssh/pfwd_demo.pub")
export PFWD_E2E_AUTHORIZED_KEY
# Later steps and start-server.sh run docker compose too.
if [ -n "${GITHUB_ENV:-}" ]; then
  echo "PFWD_E2E_AUTHORIZED_KEY=$PFWD_E2E_AUTHORIZED_KEY" >> "$GITHUB_ENV"
fi
docker compose -f "$here/../test/e2e/compose.yaml" up -d --build --wait ss
port=$(docker compose -f "$here/../test/e2e/compose.yaml" port ss 22 | sed 's/.*://')
cat >> "$HOME/.ssh/config" <<CONFIG
Host devbox
  HostName 127.0.0.1
  Port $port
  User pfwd
  IdentityFile ~/.ssh/pfwd_demo
  IdentitiesOnly yes
  StrictHostKeyChecking accept-new
CONFIG
chmod 600 "$HOME/.ssh/config"
printf '[ui]\nnotifications = false\n' > "$HOME/.config/pfwd/config.toml"
for port in 3000 6006 8888; do
  "$here/start-server.sh" "$port"
done
