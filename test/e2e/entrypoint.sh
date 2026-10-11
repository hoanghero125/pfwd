#!/bin/sh
# Trust the test's public key for the pfwd user, then run sshd in the foreground.
set -eu
: "${AUTHORIZED_KEY:?set AUTHORIZED_KEY to the public key the test logs in with}"
mkdir -p /home/pfwd/.ssh
printf '%s\n' "$AUTHORIZED_KEY" > /home/pfwd/.ssh/authorized_keys
chown -R pfwd:pfwd /home/pfwd/.ssh
chmod 700 /home/pfwd/.ssh
chmod 600 /home/pfwd/.ssh/authorized_keys
ssh-keygen -A > /dev/null
exec /usr/sbin/sshd -D -e
