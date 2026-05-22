# localrouter

```
   _                 _                 _
  | | ___   ___ __ _| |_ __ ___  _   _| |_ ___ _ __
  | |/ _ \ / __/ _` | | '__/ _ \| | | | __/ _ \ '__|
  | | (_) | (_| (_| | | | | (_) | |_| | ||  __/ |
  |_|\___/ \___\__,_|_|_|  \___/ \__,_|\__\___|_|

  localhost:11434  ──▶  remote ollama, transparently

   ┌──────────┐      ┌──────────────┐      ┌──────────┐
   │  local   │ ───▶ │  localrouter │ ───▶ │  ollama  │
   │  :11434  │ ◀─── │  proxy+pull  │ ◀─── │   host   │
   └──────────┘      └──────────────┘      └──────────┘
```

`localrouter` is a small Go reverse proxy that lets local AI tools keep
talking to `http://localhost:11434` while the actual Ollama server runs on
another machine.

It forwards normal Ollama traffic to the remote host and intercepts
`/api/chat` and `/api/generate` so it can confirm the requested model is
installed first. If `auto_pull` is enabled, `localrouter` asks the remote
Ollama host to pull the model and then resumes the original request.

## Support Matrix

| Area | Linux | macOS | Windows |
| --- | --- | --- | --- |
| `localrouter` client binary | Yes | Yes | Yes |
| Remote Ollama host behind `localrouter` | Yes | Yes | Yes |
| Dedicated setup guide in this repo | Client + host | Client + host | Host |

Host-side support means "Ollama is installed on that machine and exposed to
the network correctly". `localrouter` itself normally runs on the user or
client machine, not on the GPU host.

## Quick Path

1. Prepare the Ollama host:
   - [Linux host guide](./docs/setup-host-linux.md)
   - [macOS host guide](./docs/setup-host-macos.md)
   - [Windows host guide](./docs/setup-host-windows.md)
2. Install `localrouter` on the client machine:
   - [Linux client guide](./docs/setup-client-linux.md)
   - [macOS client guide](./docs/setup-client-macos.md)
3. Run the shortest end-to-end pipeline:

```bash
localrouter init-config \
  --remote http://HOST_IP:11434 \
  --default-model qwen2.5-coder:7b \
  --auto-pull=true && \
localrouter serve
```

4. Point your editor, chat app, or script at:

```text
http://localhost:11434
```

## Command Pipelines

| Task | Pipeline |
| --- | --- |
| First interactive setup | `localrouter init-config && localrouter serve` |
| First scripted setup | `localrouter init-config --remote http://HOST_IP:11434 --default-model qwen2.5-coder:7b --auto-pull=true && localrouter serve` |
| Update an existing config in place | `localrouter init-config --force --remote http://HOST_IP:11434 --default-model qwen2.5-coder:7b --auto-pull=true` |
| Background start | `localrouter start && localrouter status` |
| Background restart | `localrouter restart && localrouter status` |
| Stop background proxy | `localrouter stop` |
| Print resolved config paths and values | `localrouter config` |
| Show configured plus installed models | `localrouter list` |
| Show only models installed on the remote | `localrouter list --installed` |
| Inspect one remote model | `localrouter info qwen2.5-coder:7b` |
| Pull one model on the remote now | `localrouter pull qwen2.5-coder:7b` |
| Persist a default model | `localrouter use qwen2.5-coder:7b` |
| Persist a default model and start | `localrouter use qwen2.5-coder:7b --start` |
| Health check the proxy itself | `curl http://localhost:11434/__localrouter/healthz` |
| Interactive picker and background start | `localrouter` |

## Install Pipelines

### Linux or macOS: user-local install

```bash
git clone https://github.com/fede-h/localrouter &&
cd localrouter &&
PREFIX="$HOME/.local" ./install.sh &&
"$HOME/.local/bin/localrouter" version
```

### Linux or macOS: system-wide install

```bash
git clone https://github.com/fede-h/localrouter &&
cd localrouter &&
sudo ./install.sh &&
localrouter version
```

### Manual build from source

Requires Go 1.22 or newer.

```bash
git clone https://github.com/fede-h/localrouter &&
cd localrouter &&
mkdir -p dist &&
go build -trimpath -o dist/localrouter ./cmd/localrouter &&
./dist/localrouter version
```

### Cross-build

```bash
mkdir -p dist &&
GOOS=linux GOARCH=amd64 go build -trimpath -o dist/localrouter-linux-amd64 ./cmd/localrouter &&
GOOS=linux GOARCH=arm64 go build -trimpath -o dist/localrouter-linux-arm64 ./cmd/localrouter &&
GOOS=darwin GOARCH=arm64 go build -trimpath -o dist/localrouter-darwin-arm64 ./cmd/localrouter &&
GOOS=darwin GOARCH=amd64 go build -trimpath -o dist/localrouter-darwin-amd64 ./cmd/localrouter &&
GOOS=windows GOARCH=amd64 go build -trimpath -o dist/localrouter-windows-amd64.exe ./cmd/localrouter
```

## Configuration

`localrouter config` prints the exact paths for the current user. By default
the config lives under `os.UserConfigDir()` and runtime state lives under
`os.UserCacheDir()`.

### `config.json`

```json
{
  "listen_addr": "localhost:11434",
  "remote_url": "http://192.168.1.50:11434",
  "auto_pull": true,
  "pull_timeout_secs": 1800,
  "reach_timeout_ms": 1500,
  "default_model": "qwen2.5-coder:7b"
}
```

| Field | Meaning |
| --- | --- |
| `listen_addr` | Local bind address for the proxy. |
| `remote_url` | Base URL for the remote Ollama host. |
| `auto_pull` | Pull missing models on demand before forwarding the request. |
| `pull_timeout_secs` | Maximum duration for one remote pull. |
| `reach_timeout_ms` | Timeout for TCP reachability checks in `status`. |
| `default_model` | Model remembered by `localrouter use` and the no-arg interactive flow. |

### `models.list`

`models.list` sits next to `config.json` and is a plain text file with one
model tag per line:

```text
qwen2.5-coder:7b
llama3.1:8b
mistral:7b
```

### Environment Overrides

These override the current run only and are never written back to
`config.json`.

| Variable | Effect |
| --- | --- |
| `LOCALROUTER_LISTEN` | Override `listen_addr` |
| `LOCALROUTER_REMOTE` | Override `remote_url` |
| `LOCALROUTER_AUTO_PULL` | Override `auto_pull` |
| `LOCALROUTER_DEFAULT_MODEL` | Override `default_model` |
| `LOCALROUTER_PULL_TIMEOUT` | Override pull timeout in seconds |
| `LOCALROUTER_REACH_TIMEOUT_MS` | Override reachability timeout in milliseconds |

## Behavior Contract

For every `POST /api/chat` and `POST /api/generate`, `localrouter`:

1. Reads and restores the request body.
2. Extracts the `model` field if present.
3. Calls the remote `/api/tags`.
4. Forwards immediately if the model already exists.
5. Returns `404` if the model is missing and `auto_pull=false`.
6. Calls remote `/api/pull` if the model is missing and `auto_pull=true`.
7. Returns `502` if the remote becomes unreachable.
8. Returns `500` if the remote pull itself fails.

All other Ollama paths are forwarded as-is. The proxy also exposes:

```bash
curl http://localhost:11434/__localrouter/healthz
```

Example response:

```json
{"ok":true,"service":"localrouter","remote":"http://192.168.1.50:11434","auto_pull":true}
```

## Security Notes

- `localrouter` binds to `localhost:11434` by default. Keep it that way unless
  you intentionally want to expose the proxy itself.
- The remote hop is plain HTTP unless you put TLS, WireGuard, SSH tunneling,
  or another secure transport in front of Ollama.
- Exposing Ollama on `0.0.0.0:11434` means anything on that reachable network
  can hit its API unless you protect it elsewhere.

See [SECURITY.md](./SECURITY.md) and [FUTURES.md](./FUTURES.md) for the
longer-term auth and TLS work.

## Repository Layout

```text
cmd/localrouter/      CLI entrypoint and subcommands
internal/config/      Config, models list, and runtime state paths
internal/daemon/      PID file, liveness probe, start/stop helpers
internal/ollama/      Stdlib client for /api/tags, /api/show, /api/pull
internal/proxy/       Reverse proxy, model interception, pull coalescing
docs/                 Host and client setup guides
```

The project uses only the Go standard library.
