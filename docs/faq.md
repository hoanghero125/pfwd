# Questions

**What does pfwd install or run on the server?**
Nothing is installed. pfwd runs one probe to learn the server's OS, then a small loop on one SSH channel that lists listening sockets every 2 seconds (`ss`, `/proc/net/tcp`, `lsof`, `sockstat`, `netstat`, or PowerShell on Windows servers). It writes no files there.

**How is it different from `ssh -L`?**
`ssh -L` forwards the ports you name when you connect. pfwd watches the server and forwards ports as they appear, shows what listens where, and keeps forwards alive through dropped connections. It also does `-R` and `-D`, and it remembers the forwards you pin.

**How is it different from the VS Code Ports panel?**
Same idea, without an editor and without a server-side agent: it works on machines where you can't run the VS Code server, and from any terminal or a browser tab.

**Does it work without OpenSSH installed?**
Yes. pfwd has its own SSH client. When OpenSSH is there, it uses `ssh -G` to read your config exactly as `ssh` would, and otherwise it reads `~/.ssh/config` itself.

**Can other people on my network reach my forwards?**
Not by default: forwards listen on 127.0.0.1. `forward.bind` or `--bind` changes that, and the TUI and the web UI mark such forwards. A SOCKS proxy refuses any non-loopback address unless you set `forward.allow_lan_socks`.

**Where are my passwords kept?**
In memory only, so a reconnect doesn't ask again. They are never written to disk or to the log, and pfwd zeroes them when it quits.

**Is the web UI safe on a shared machine?**
It listens on 127.0.0.1 only. A login link works once, for ten minutes, and each tab gets its own token, which every request must carry. Requests from other sites, or for another host name, are refused. Anyone who can run programs as you can still use your SSH keys, as with `ssh` itself.

**Why didn't a port get forwarded automatically?**
Ports already listening when pfwd connected aren't auto-forwarded, only new ones. A port you stopped, or whose auto-forward failed, isn't tried again that session. Ports in `detect.ignore_ports` and processes in `detect.ignore_processes` are skipped, and with `forward.auto_ports` set, only those are forwarded.

**How do I keep forwards after closing the terminal?**
`pfwd fwd <host> <ports> --detach` hands them to a background daemon. `pfwd attach` opens the TUI on it, `pfwd ui` the web UI, and `pfwd daemon stop` ends it.

**Can I use it in scripts?**
Yes: `pfwd ls`, `pfwd fwd`, `pfwd stop` and `pfwd hosts` work without a terminal, print JSON with `--json`, and have documented exit codes. See the README's Scripting section.

**Does it forward UDP or Unix sockets?**
No. SSH forwards TCP; Unix-socket forwards aren't supported yet.
