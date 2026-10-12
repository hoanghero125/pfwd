package config

// Template returns the file `pfwd config edit` creates: every key at its
// default value, with a comment on what it does.
func Template() string {
	return `# pfwd configuration. Every key is optional; the values below are the
# defaults. Check this file with: pfwd config validate

[detect]
interval = "2s"            # how often to list the server's ports (at least 500ms)
http_probe = true          # check whether forwarded ports speak HTTP or HTTPS
debounce_scans = 2         # snapshots a new port must appear in before it is auto-forwarded
ignore_ports = [22]        # ports or ranges like "9000-9100": hidden, never auto-forwarded
ignore_processes = ["sshd", "systemd-resolved", "cupsd", "rpcbind", "containerd", "dnsmasq", "chronyd"]

[forward]
auto = true                # forward new ports automatically
auto_ports = []            # if not empty, only auto-forward these, e.g. ["3000-3999", 5173, 8888]
bind = "127.0.0.1"         # local address; anything else exposes forwards to your network
on_conflict = "ask"        # when a local port is taken: ask, next, random or fail
privileged_offset = 10000  # a local port below 1024 you can't bind moves up: 80 becomes 10080 (0 turns it off)
auto_expire = "10m"        # drop auto-forwards whose port has been gone this long, unless pinned
max_conns = 256            # open connections per forward
restore = true             # remember forwards in state.json and restore them on connect
allow_lan_socks = false    # let a SOCKS proxy listen on a non-loopback address

[ssh]
use_openssh_config = true  # resolve hosts with "ssh -G" when OpenSSH is installed
keepalive_interval = "15s" # check the connection this often (ServerAliveInterval wins when set)
keepalive_count_max = 3    # reconnect after this many unanswered checks
connect_timeout = "10s"    # used when the ssh config sets no ConnectTimeout
reconnect_max_backoff = "30s" # longest wait between reconnect attempts

[ui]
theme = "auto"             # auto, dark or light
mouse = true               # click a row to select it, scroll with the wheel
confirm_quit = true        # ask before quitting while forwards are active
notifications = true       # desktop notification when a port is auto-forwarded
show_hidden = false        # show ignored ports (toggle with h)
glyphs = "auto"            # auto, unicode or ascii

[labels]                   # your labels win over the built-in ones
# 8000 = "API"

# Per-host overrides, matched against the ssh config alias:
# [hosts.devbox]
# auto = false
# auto_ports = [8888, 6006]
# forwards = ["5432", "15432:db.internal:5432"]   # always on, pinned
`
}
