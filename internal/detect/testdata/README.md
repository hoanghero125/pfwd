# Detection samples

Parser tests read these files. Captured files are real output; hand-written
ones copy formats seen on real machines that we could not capture here.

| File | Source |
| --- | --- |
| `ss-alpine-root.txt` | Captured: `ss -tlnp` as root, Alpine 3.22, iproute2-ss (2026-10-11) |
| `ss-alpine-noheader.txt` | Captured: `ss -Htlnp`, same container |
| `ss-alpine-user.txt` | Captured: `ss -tlnp` as a non-root user; root's socket has no process |
| `ss-debian.txt` | Captured: `ss -tlnp` as root, debian:stable-slim, iproute2 |
| `proc-alpine.txt` | Captured: `cat /proc/net/tcp /proc/net/tcp6`, Alpine 3.22 |
| `proc-debian.txt` | Captured: same command, debian:stable-slim (includes TIME_WAIT rows) |
| `ss-ubuntu-resolved.txt` | Hand-written: Ubuntu with systemd-resolved (`127.0.0.53%lo`) and a Java socket on `[::ffff:127.0.0.1]` |
| `ss-multi-users.txt` | Hand-written: nginx master and worker sharing a socket |
| `ss-old-format.txt` | Hand-written: old iproute2 (CentOS 6/7 era): `:::22` and `users:(("sshd",1158,3))` |

The captured listeners are `python3 -m http.server` on `0.0.0.0:8123`,
`127.0.0.1:5173`, `:: 8000` (dual-stack, shown as `*`) and `::1 6006`.
