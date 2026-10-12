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
| `lsof-macos.txt` | Captured: `lsof -nP -iTCP -sTCP:LISTEN -F pcn` as a normal user, GitHub macos-latest runner (2026-10-13); root's processes are not listed |
| `netstat-macos.txt` | Captured: `netstat -anv -p tcp` LISTEN lines, same runner |
| `netstat-gnu.txt` | Captured: `netstat -tlnp` (net-tools) as root, GitHub ubuntu-latest runner |
| `netstat-busybox.txt` | Captured: busybox `netstat -tln`, Alpine container on the same runner |
| `netstat-busybox-p.txt` | Captured: busybox `netstat -tlnp`, same container |
| `netstat-windows.txt` | Captured: `netstat -ano -p TCP`, GitHub windows-latest runner (Windows 10.0.26100), all states |
| `netstat6-windows.txt` | Captured: `netstat -ano -p TCPv6`, same runner |
| `tasklist-windows.txt` | Captured: `tasklist /fo csv /nh`, same runner, cut to the processes that listen |
| `sockstat-freebsd.txt` | Hand-written from sockstat(1)'s columns (`sockstat -46 -l -P tcp`); CI has no FreeBSD runner |
| `netstat-openbsd.txt` | Hand-written from OpenBSD netstat(1) (`netstat -an -p tcp`) |

The captured listeners are `python3 -m http.server` on `0.0.0.0:8123`,
`127.0.0.1:5173`, `:: 8000` (dual-stack, shown as `*`) and `::1 6006`.

The runner captures add `python3 -m http.server 8123` (Windows: `python`), and on
macOS `nc -l 127.0.0.1 9001` and a Python listener on `::1 9002`.
