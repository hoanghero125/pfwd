# Using pfwd

The [README](../README.md) has the full list of keys and the JSON formats. This page goes through the common jobs.

## Reach a dev server on a remote machine

```sh
pfwd devbox
```

`devbox` is an alias from `~/.ssh/config`; `user@host` works too, and so do ssh's `-p`, `-l`, `-i`, `-J` and `-F`. Start the server on devbox (`npm run dev`, `jupyter lab`, `python -m http.server`) and a few seconds later the TUI shows its port forwarded: `localhost:5173` on your machine reaches it. `o` opens it in the browser and `y` copies the URL.

Ports that were already listening when pfwd connected wait in the "not forwarded" group: `enter` forwards one to the same local port, `F` to another.

## Reach a database behind the server

A forward can go past the server to another machine it reaches:

```sh
pfwd fwd devbox 15432:db.internal:5432
```

`localhost:15432` now reaches `db.internal:5432` as the server sees it. To have it every time you connect, put it in `config.toml`:

```toml
[hosts.devbox]
forwards = ["15432:db.internal:5432"]
```

## Show a local port to the server

`r` in the TUI lists your machine's listening ports; `enter` exposes the selected one on the server under the same number. From a script: `pfwd fwd devbox -R 9000`. Programs on the server then reach your port at `localhost:9000`.

## Browse through the server

`d` starts a SOCKS5 proxy on `localhost:1080` whose connections leave from the server; `pfwd fwd devbox -D 1080` does the same from a script. Point a browser, or `curl --socks5-hostname localhost:1080`, at it.

## Several hosts

```sh
pfwd devbox gpu-node
```

Each host gets a tab: `tab` and `shift+tab` switch, `ctrl+a` shows every host in one table, and `+` connects another host from your ssh config.

## Keep forwards after the terminal closes

```sh
pfwd fwd devbox 5432 8080:3000 --detach   # the daemon keeps them
pfwd ls                                  # what it serves
pfwd attach                              # the TUI on the daemon; q leaves it running
pfwd stop devbox:5432
pfwd daemon stop                         # ends the daemon and its forwards
```

## Use a browser instead of the terminal

```sh
pfwd ui
```

The web UI has the TUI's controls, its keys and its words, and shows the daemon's forwards, so it agrees with `pfwd attach` and `pfwd ls`. Host-key questions and passwords are answered in the page.

## Remember forwards

`p` pins a forward: it comes back, on the same local port, the next time you connect to that host. Forwards listed under `[hosts.<alias>]` in `config.toml`, and the `LocalForward`, `RemoteForward` and `DynamicForward` lines of your ssh config, are always on.

## Scripts

```sh
pfwd ls devbox --json | jq '.hosts[0].ports[].port'
pfwd fwd devbox 5432 --json      # one JSON object per line, until Ctrl+C
```

The commands work without a terminal and their exit codes say what failed: 3 for the connection, 4 for the login, 5 for the host key, 6 when a forward can't listen.

## Settings

Everything in [config.md](config.md) is optional. The ones people change most:

```toml
[forward]
auto = false                     # forward only what you ask for
auto_ports = ["3000-3999", 8888] # or only these

[detect]
ignore_ports = [22, 6000]

[labels]
8000 = "API"
```
