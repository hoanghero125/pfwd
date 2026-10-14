# When something doesn't work

Start with `pfwd doctor <host>`. It checks each step in order and stops at the first one that fails, with the fix: how your ssh config resolves the host, whether it answers, its host key, the login, port detection, `-L` and `-R` forwarding on the server, and listening on this machine. It never writes `known_hosts`. Its exit code is 3, 4 or 5 when the connection, the login or the host key fails.

For a bug report, `pfwd doctor --bundle <host>` writes `pfwd-doctor-<time>.zip` with the version, your config file, the end of the log and the report. Secrets and your home directory are taken out, but look inside before you attach it.

The log is `pfwd.log` in pfwd's state directory (see [install.md](install.md#where-pfwd-keeps-its-files)). `--log-level debug` or `PFWD_LOG=debug` makes it more detailed. Passwords, passphrases, tokens and keys are taken out of every line.

## Connecting

**"no such host" or the wrong machine.** pfwd resolves hosts with `ssh -G`, so it sees what `ssh` sees, `Match` and `Include` included. Check with `ssh -G <host> | grep -i '^hostname\|^port\|^user'`. Without OpenSSH, pfwd reads `~/.ssh/config` itself; `-F` names another file.

**"host key changed".** pfwd refuses to connect, like `ssh`. If you know why the key changed (the server was reinstalled), remove the old key with `ssh-keygen -R <host>` and connect again.

**An unknown host key without a terminal.** `pfwd fwd`, `pfwd ls` and the daemon can't ask. Connect once with `pfwd <host>` or `ssh <host>`, pass `--accept-new`, or answer the question in `pfwd ui`.

**Authentication failed.** pfwd tries ssh-agent, then your `IdentityFile` keys, then a password or one-time code if the server asks. The error says what it tried and what to change. On Windows, start the OpenSSH agent service (`Get-Service ssh-agent`) or Pageant. A key with a passphrase is asked for once per session; `ssh-add` it to skip the question.

**A line of `known_hosts` that pfwd can't read** is skipped, with a warning in the log, like OpenSSH does.

## Ports

**"Detection unavailable".** The server has none of `ss`, `netstat`, `lsof`, `sockstat` or a readable `/proc/net/tcp`, as in minimal containers. You can still forward by hand: New forward in the web UI, `pfwd fwd <host> <port>`, or the host's `forwards` list in `config.toml`.

**A port doesn't show up.** Check that it is in neither `detect.ignore_ports` nor `detect.ignore_processes`; `h` in the TUI shows hidden ports. Servers listening only on a Unix socket aren't TCP ports and don't show.

**"server disallows TCP forwarding".** The server's sshd has `AllowTcpForwarding no`. Ask its admin; pfwd can't work around it.

**A remote forward (`-R`) says "port busy".** After a dropped connection, the server can hold the port for the old session a while longer. pfwd keeps trying until it is free; `ClientAliveInterval` in the server's sshd_config shortens the wait. To listen on an address other than loopback on the server, sshd also needs `GatewayPorts`.

**The local port is taken.** `forward.on_conflict` decides: `ask` (the TUI offers the next free port), `next`, `random` or `fail`. A port below 1024 that needs root moves up by `forward.privileged_offset` (80 becomes 10080).

## The daemon and the web UI

**`pfwd ui` says the daemon is older than pfwd.** You updated pfwd while the old daemon kept running. Run `pfwd daemon stop`, then `pfwd ui` again.

**"This tab isn't logged in to pfwd".** Each login link works once, for ten minutes, and belongs to the tab that opened it. Run `pfwd ui` for a new one; a reloaded tab keeps its login.

**The page says it lost the connection to the daemon.** The daemon stopped or crashed. `pfwd daemon status` says whether it runs; `pfwd ui` starts it again. Its log says why it stopped.

**Settings saved in the web UI don't seem to apply.** The daemon reads `config.toml` when it starts: `pfwd daemon stop`, then `pfwd ui`.

## Windows

**An antivirus blocks pfwd or quarantines it.** Some antivirus products flag new, unsigned Go binaries that open network ports. Install through Scoop or from the release archive, check it against the signed checksums (see [install.md](install.md#checking-a-download)), and allow it in the antivirus.

**Boxes or question marks instead of symbols.** The old Windows console lacks some glyphs. pfwd switches to ASCII there on its own; `ui.glyphs = "ascii"` forces it. Windows Terminal shows the Unicode ones.
