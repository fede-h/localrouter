# Future features

A running list of things the new Go port does *not* do yet, roughly grouped
by what they would cost. None of these are blocking — the core proxy +
auto-pull contract is complete — but they're called out so we don't lose
them.

## Proxy and routing

- **Multi-remote pools.** Today `--remote` is a single URL. A pool with
  health checks and least-busy or round-robin routing would let one
  localrouter front several GPU machines.
- **Per-model routing.** Pin specific tags to specific upstreams (e.g.
  large vision models on the box with the big GPU, code models on the
  smaller one).
- **Failover.** If the chosen upstream fails mid-stream, optionally
  retry against another upstream from the pool — only safe for
  idempotent paths (no partial token replay).
- **Local fallback.** If a `127.0.0.1:11434` Ollama is also installed,
  fall back to it when the remote is unreachable. Today an unreachable
  remote returns 502; the user has to flip configs manually.
- **Request rewriting / model aliasing.** Map a friendly alias
  (`local-coder`) to a concrete tag (`qwen2.5-coder:7b`) so clients can
  keep their config stable while the underlying model rotates.
- **Hot config reload.** SIGHUP (POSIX) / named-pipe nudge (Windows) to
  re-read config.json without dropping the listener.
- **Native Authentication.** Introduce optional Bearer Token or Basic Auth logic natively within the localrouter proxy. This secures the listen socket on shared workstations, or allows gating access to the upstream Ollama without relying on external reverse proxies.
- **Native TLS/HTTPS (terminator and client).**
  - **Local Listen:** Bind the proxy on HTTPS (using `crypto/tls` with auto-generated self-signed certificates or provided certs) so that client editors demanding `https://localhost` work out of the box natively.
  - **Remote Transport:** Add native support for mutual TLS (mTLS) or simplified pinned-certificate verification for the upstream HTTP client. This would securely encrypt the hop between the local machine and the GPU host, recovering the exact security guarantees lost by removing SSH tunnels, but natively in Go.

## Observability

- **Per-request structured logs.** Today log lines are free-text; JSON
  with `request_id`, `model`, `bytes_in/out`, `pull_triggered`,
  `upstream_status` would make a Loki/Grafana pipeline trivial.
- **`/__localrouter/metrics` endpoint.** Prometheus exposition for
  request counts, latencies, active pulls, upstream errors.
- **`localrouter tail`** subcommand that follows the daemon log file
  with structured filtering (`--model qwen…`, `--errors-only`).

## Pull and cache UX

- **Streaming pull progress.** `Pull()` currently uses `stream:false`
  per the comeback prompt. Switching to `stream:true` and surfacing
  layer/percentage progress would give better feedback for multi-GB
  pulls.
- **Pre-warm on start.** Optional config field listing tags to pull
  during `serve` startup so the first chat request doesn't pay the
  cold pull cost.
- **Cache-aware listing.** `localrouter list` is a live `/api/tags`
  call every time. A short-TTL cache (like the original Bash tool's
  60s `installed.cache`) would speed up shells that call `list`
  repeatedly.
- **`localrouter rm <model>`.** Currently you have to SSH into the
  remote and `ollama rm`. Wire up `DELETE /api/delete` for symmetry.

## Daemon lifecycle and platform integration

- **systemd unit template.** A user-level unit (`~/.config/systemd/user/
  localrouter.service`) plus an `install.sh --systemd` switch.
- **launchd plist** for macOS, dropped into `~/Library/LaunchAgents`.
- **Windows Service install path** via `sc.exe` or the native
  `golang.org/x/sys/windows/svc` package (would force a dependency).
- **`localrouter logs` subcommand.** Right now we tell users to tail
  the log file by path — give them a built-in tail.

## CLI ergonomics

- **Interactive picker improvements.** The current numeric picker is
  the minimum that works without a TUI dependency. Arrow-key navigation
  + filtering would be a clear win — likely worth picking up
  `golang.org/x/term` (stdlib-adjacent).
- **Shell completions.** `localrouter completion bash|zsh|fish|powershell`
  that emits the completion script.
- **`localrouter doctor`.** Runs the pre-flight checks (config sane,
  remote reachable, port free, tags responding) and prints a tidy
  report.
- **Model search.** Hit ollama.com's library JSON to suggest tags from
  a partial name when the user runs `localrouter pull part…`.

## Distribution

- **GoReleaser config + GitHub releases.** Build cross-platform
  binaries (linux/amd64, linux/arm64, darwin/arm64, darwin/amd64,
  windows/amd64) on tag push.
- **Homebrew formula** and **Scoop manifest** so the install line is
  one command on each platform.
- **Container image.** `ghcr.io/.../localrouter` so it can run as a
  sidecar in dev environments.

## From the old Bash tool, deliberately not ported

These existed in the original SSH-tunnel tool and were dropped on the move
to a direct HTTP reverse proxy. Listed here so it is clear the omission is
intentional, not a regression:

- `--setup-windows`, `--setup-ssh-key`: the new proxy reaches the
  remote over plain HTTP. SSH and OpenSSH configuration are no longer
  this tool's job — set them up with `ssh`/your config-management
  tool of choice. (See **Native TLS/HTTPS** above if you need the hop
  encrypted.)
- `ollama cp` to a stable target tag: in the new flow the proxy
  forwards the user-requested model verbatim, so the "rename downloads
  to a stable alias" step is moot. If we want aliases again, see
  **Model aliasing** above.
- Sudo / `systemctl stop ollama` on local port conflict: the new tool
  fails fast with a clear "port in use" message instead of trying to
  manage the user's local services.
