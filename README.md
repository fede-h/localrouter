# localrouter

`localrouter` is a small Go reverse proxy that sits between local AI tools
and a remote [Ollama](https://ollama.com) instance.

Clients keep talking to `http://localhost:11434` as if Ollama were running
on the local machine. `localrouter` forwards the traffic to the upstream
host, and — if the client requests a model the remote does not yet have —
it pulls the model on the remote first and then resumes streaming.

```
  ┌─────────────┐    http://localhost:11434     ┌────────────────┐
  │ editor /    │ ─────────────────────────────►│  localrouter   │
  │ chat client │ ◄──── streamed chunks ────────│  (this tool)   │
  └─────────────┘                               └───────┬────────┘
                                                        │
                              http://gpu-host:11434     │
                                                        ▼
                                              ┌────────────────┐
                                              │  Ollama (any   │
                                              │  OS, any GPU)  │
                                              └────────────────┘
```

It is a single, statically linked Go binary (stdlib only) and runs the
same way on Linux, macOS, and Windows.

> **Heads up.** This is a from-scratch rewrite of the original
> SSH-tunnel Bash tool. SSH tunneling is no longer part of the picture
> — `localrouter` speaks HTTP directly to the remote Ollama. If you
> still need the encrypted hop, terminate TLS in front of Ollama or
> open your own SSH tunnel and point `--remote` at the local end of
> it.

## Features

- Transparent reverse proxy with proper chunked / streaming response
  forwarding (Ollama's `/api/chat` and `/api/generate` work as
  expected, including token-by-token streams).
- Intercepts `/api/chat` and `/api/generate`, peeks at the `model`
  field, and queries the remote's `/api/tags` to confirm the model is
  installed before forwarding.
- Optional auto-pull: missing models are downloaded on the remote via
  `/api/pull` (non-streaming) and the original request is resumed
  automatically once the pull completes.
- Concurrent requests for the same missing model share a single pull
  (no thundering-herd of `/api/pull` calls).
- Cross-platform process lifecycle: `start` / `stop` / `status` /
  `restart`, with PID file + HTTP health probe.
- OS-agnostic config: `os.UserConfigDir` for settings, `os.UserCacheDir`
  for runtime state. No SSH, systemd, or PowerShell dependencies.
- 502 when the remote is unreachable, 500 when an auto-pull fails,
  404 when a model is missing and auto-pull is off — never crashes the
  proxy.

## Install

### Build from source

Requires Go 1.22 or newer.

```bash
git clone https://github.com/fede-h/localrouter
cd localrouter
go build -o localrouter ./cmd/localrouter
sudo install -m 0755 localrouter /usr/local/bin/localrouter
```

Or use the bundled `install.sh` (Linux/macOS):

```bash
git clone https://github.com/fede-h/localrouter
cd localrouter
./install.sh                       # installs to /usr/local/bin (sudo)
PREFIX="$HOME/.local" ./install.sh # or to a user prefix, no sudo
```

### Cross-compile

```bash
# Linux x86_64
GOOS=linux   GOARCH=amd64 go build -o dist/localrouter-linux-amd64 ./cmd/localrouter

# Linux arm64
GOOS=linux   GOARCH=arm64 go build -o dist/localrouter-linux-arm64 ./cmd/localrouter

# macOS Apple Silicon
GOOS=darwin  GOARCH=arm64 go build -o dist/localrouter-darwin-arm64 ./cmd/localrouter

# macOS Intel
GOOS=darwin  GOARCH=amd64 go build -o dist/localrouter-darwin-amd64 ./cmd/localrouter

# Windows x86_64
GOOS=windows GOARCH=amd64 go build -o dist/localrouter-windows-amd64.exe ./cmd/localrouter
```

### Verify

```bash
localrouter version
```

## Quickstart

On the remote machine (the host with the GPU), you need to make Ollama's API reachable from your local network.

**Linux Host:**
By default Ollama binds to `127.0.0.1:11434`. You must set `OLLAMA_HOST=0.0.0.0:11434` (e.g., via `systemctl edit ollama`) and open port 11434 in your firewall.

**Windows Host:**
1. Open an elevated PowerShell as Administrator.
2. Allow incoming traffic through the Windows Defender Firewall:
   ```powershell
   New-NetFirewallRule -DisplayName "Ollama API Inbound" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 11434 -Profile Any -Enabled True
   ```
3. Set the environment variable so Ollama binds to all network interfaces globally:
   ```powershell
   [System.Environment]::SetEnvironmentVariable("OLLAMA_HOST", "0.0.0.0", "Machine")
   ```
4. Restart the Ollama app (exit it from the system tray and launch it again).

On the local machine:

```bash
# 1. Write a config file (interactive prompts for remote URL, default model, etc.)
localrouter init-config

# 2. Run the proxy in the foreground; Ctrl+C to stop.
localrouter serve

# Or run it in the background:
localrouter start
localrouter status
localrouter stop
```

Point your AI tool at `http://localhost:11434` exactly like a local
Ollama. The first time you call `/api/chat` or `/api/generate` with a
model the remote does not have, `localrouter` will pull it for you (this
can take a while for large models — increase `pull_timeout_secs` if
needed).

## Subcommands

| Command                                  | What it does                                              |
| ---------------------------------------- | ---------------------------------------------------------- |
| `localrouter`                            | Interactive: pick a model, set as default, start daemon.   |
| `localrouter serve` (alias `watch`)      | Run the proxy in the foreground.                           |
| `localrouter start`                      | Spawn the proxy in the background; write PID file.         |
| `localrouter stop` (alias `kill`)        | Stop the running daemon.                                   |
| `localrouter restart`                    | `stop` + `start`.                                          |
| `localrouter status`                     | Daemon state + remote reachability + recent timestamps.    |
| `localrouter list [--installed]`         | Configured + installed model list with sizes.              |
| `localrouter info <model>`               | `/api/show` for a model.                                   |
| `localrouter pull <model>`               | Synchronous pull on the remote (mirrors the auto-pull).    |
| `localrouter use <model> [--start]`      | Set the default model; optionally start the proxy.         |
| `localrouter config`                     | Print resolved paths + current config.                     |
| `localrouter init-config`                | Write a starter `config.json` + `models.list`.             |
| `localrouter version`                    | Print version.                                             |

Every subcommand accepts `--help` to list its flags.

### Foreground vs background

For unattended use under `systemd`, `launchd`, Windows Service Manager, or
`tmux`/`nohup`, run `localrouter serve` (or `watch`) — it stays in the
foreground, logs to stdout/stderr, and shuts down on SIGINT/SIGTERM.

`localrouter start` is a convenience for "just background it on this
shell". On Linux/macOS the child gets its own session
(`setsid`-equivalent) so it survives the launching terminal closing. On
Windows the child runs in a new process group with no console.

## Configuration

Config lives at the user-config dir for the platform:

| Platform | Config file                                                    | State / cache                                    |
| -------- | -------------------------------------------------------------- | ------------------------------------------------ |
| Linux    | `$XDG_CONFIG_HOME/localrouter/config.json` (`~/.config/...`)   | `$XDG_CACHE_HOME/localrouter/` (`~/.cache/...`) |
| macOS    | `~/Library/Application Support/localrouter/config.json`        | `~/Library/Caches/localrouter/`                  |
| Windows  | `%AppData%\localrouter\config.json`                            | `%LocalAppData%\localrouter\`                    |

`localrouter config` prints the resolved paths and current values.

### `config.json`

```json
{
  "listen_addr":        "localhost:11434",
  "remote_url":         "http://192.168.1.50:11434",
  "auto_pull":          true,
  "pull_timeout_secs":  1800,
  "reach_timeout_ms":   1500,
  "default_model":      "qwen2.5-coder:7b"
}
```

| Field               | What it controls                                                                |
| ------------------- | ------------------------------------------------------------------------------- |
| `listen_addr`       | Local bind address. Use `0.0.0.0:11434` to expose on the LAN (consider auth).   |
| `remote_url`        | Upstream Ollama base URL. Scheme + host + port.                                 |
| `auto_pull`         | When `true`, missing models are pulled on demand. When `false`, return 404.     |
| `pull_timeout_secs` | Per-pull timeout. Increase for huge models on slow links.                       |
| `reach_timeout_ms`  | TCP probe deadline used by `status` and pre-flight checks.                      |
| `default_model`     | Used by the interactive picker; persisted across runs.                          |

### `models.list`

A plain text file in the same config dir, one Ollama tag per line. Used
by the interactive picker and `localrouter list`. Lines beginning with
`#` are comments.

```
qwen2.5-coder:7b
llama3.1:8b
mistral:7b
```

### Environment overrides (transient — never persisted)

Useful for one-off invocations under wrappers and editors:

| Variable                       | Effect                                                  |
| ------------------------------ | ------------------------------------------------------- |
| `LOCALROUTER_LISTEN`           | Override `listen_addr`.                                 |
| `LOCALROUTER_REMOTE`           | Override `remote_url`.                                  |
| `LOCALROUTER_AUTO_PULL`        | `true`/`false`.                                         |
| `LOCALROUTER_DEFAULT_MODEL`    | Override `default_model`.                               |
| `LOCALROUTER_PULL_TIMEOUT`     | Pull timeout in seconds.                                |
| `LOCALROUTER_REACH_TIMEOUT_MS` | TCP probe timeout in milliseconds.                      |

`localrouter use <model>` and `init-config` deliberately write **only**
fields you set explicitly — env-var overrides do not leak into
`config.json`.

## Behavior contract (per request)

For every `POST /api/chat` and `POST /api/generate`:

1. Read the body into memory (capped at 8 MB) and restore it with
   `io.NopCloser(bytes.NewReader(body))` so the reverse proxy sees an
   identical request.
2. Extract the `model` field. Missing field → forward as-is and let
   Ollama answer.
3. Call `GET /api/tags` on the remote. Match the requested model
   against `name` (and `name:latest` if the requester omitted the tag).
4. If installed → forward.
5. If not installed and `auto_pull=false` → return `404` with a JSON
   error.
6. If not installed and `auto_pull=true` → call `POST /api/pull` with
   `stream:false`, wait for completion (subject to `pull_timeout_secs`),
   then forward. Concurrent requests for the same model share one pull.
7. If the remote is unreachable at any point → `502 Bad Gateway`.
8. If the pull itself fails → `500 Internal Server Error` with the
   remote's error message.

All other paths (`/api/tags`, `/api/show`, `/api/embed`, ...) are
forwarded untouched. The proxy also exposes
`GET /__localrouter/healthz` for `status` to verify it's the right
process on the listen port.

## Health endpoint

```bash
$ curl http://localhost:11434/__localrouter/healthz
{"ok":true,"service":"localrouter","remote":"http://192.168.1.50:11434","auto_pull":true}
```

## Repository layout

```
cmd/localrouter/      Main entrypoint, CLI subcommands, flag parsing.
internal/proxy/       httputil.ReverseProxy + interception + pull coalescing.
internal/ollama/      Stdlib HTTP client for /api/tags, /api/show, /api/pull.
internal/config/      OS-agnostic config + models.list + state.json.
internal/daemon/      PID file, liveness probe, cross-platform Spawn/Stop.
```

All packages use only the Go standard library.

## Security notes

- The proxy binds to `localhost:11434` by default. If you switch
  `listen_addr` to a non-loopback address, anything on that network can
  reach the upstream Ollama through you (including auto-pulling models
  you don't want pulled). See `FUTURES.md` for the planned auth /
  TLS work.
- The upstream call uses plain HTTP. If the remote is across an
  untrusted network, terminate TLS in front of it or open your own
  SSH/WireGuard tunnel and point `--remote` at the tunnel endpoint.
- `pull_timeout_secs` is the only governor on auto-pull duration. On
  a shared machine, treat each `/api/chat` for an unknown tag as
  "this client can make the remote spend disk space and bandwidth."

## Roadmap

See [`FUTURES.md`](./FUTURES.md) for the list of planned features and
deliberate non-features (things the original Bash tool did that the new
tool no longer does).
