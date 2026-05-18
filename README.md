# localrouter

`localrouter` opens a local SSH tunnel from `localhost:11434` to a remote Ollama server, and can optionally ask the remote host to pull an Ollama model and copy it to a stable target tag before the tunnel opens.

The original workflow is meant for a Linux client using a Windows machine as the GPU host, but the command only assumes that the remote machine is reachable over SSH and has `ollama` on its remote shell `PATH`.

## Why this exists

Some local AI tools assume Ollama is available at `http://localhost:11434`. When Ollama is actually running on another machine, `localrouter` lets those tools keep using the local endpoint while SSH forwards traffic to the remote host.

## Install

Clone the repo and run:

```bash
sudo ./install.sh
```

This installs:

- `/usr/local/bin/localrouter`
- `/usr/local/bin/win-ai` as a compatibility symlink

To install somewhere else:

```bash
PREFIX="$HOME/.local" ./install.sh
```

## Configure

The recommended path is the interactive wizard:

```bash
localrouter --setup
```

It prompts for `WINDOWS_USER`, `WINDOWS_HOST`, SSH/local/remote ports, target tag, retry behaviour, and writes `~/.config/localrouter/config` atomically (0600). Each value is validated before being saved.

For a non-interactive scaffold, use:

```bash
localrouter --init-config        # writes a commented config template
```

Either way you can edit `~/.config/localrouter/config` directly:

```bash
WINDOWS_USER="your-windows-user"
WINDOWS_HOST="192.168.1.50"

# Optional defaults:
SSH_PORT="22"
LOCAL_PORT="11434"
REMOTE_HOST="localhost"
REMOTE_PORT="11434"
TARGET_TAG="qwen2.5-coder:7b"
STOP_LOCAL_OLLAMA="ask"
SSH_OPTS="-o ServerAliveInterval=30 -o ExitOnForwardFailure=yes"
RETRY_COUNT="3"
RETRY_BACKOFF="2"
PULL_TIMEOUT="1800"
CACHE_TTL="60"
WATCH_BACKOFF_MAX="30"
```

Environment-variable overrides for any of these: prefix with `LOCALROUTER_` (e.g. `LOCALROUTER_PULL_TIMEOUT=3600 localrouter use big-model`).

Edit model choices in `~/.config/localrouter/models.list`, or use:

```bash
localrouter --edit-models
```

### Passwordless SSH

```bash
localrouter --setup-ssh-key
```

Generates `~/.ssh/localrouter_<host>` (ed25519, no passphrase), installs the public key on the Windows host (auto-detects whether the SSH user is an admin and writes to `%USERPROFILE%\.ssh\authorized_keys` or `C:\ProgramData\ssh\administrators_authorized_keys` accordingly), tightens ACLs with `icacls`, and merges `-i <key>` into `SSH_OPTS` so future commands skip the password prompt.

### Bootstrap the Windows host

```bash
localrouter --setup-windows
```

Pipes a vetted, idempotent PowerShell script through your SSH session that:

- ensures the `sshd` service is set to **Automatic** startup and is **Running**
- sets any non-`Private` network connection profiles to `Private` (so the firewall rule applies on this network)
- adds a firewall rule named `localrouter SSH inbound` allowing TCP `SSH_PORT` inbound on all profiles (only if a rule with that name does not already exist)
- reports whether `ollama` is on the SSH user's `PATH`
- reports whether `sshd` is listening on `SSH_PORT`

It will **not** install OpenSSH Server itself — that needs to happen once on the Windows host with `Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0` (requires admin). Without it there is no SSH session to pipe through.

All remote operations (`--setup-windows`, `--setup-ssh-key`, `--model …`) are wrapped in a retry loop. Override with `--retries N`, `LOCALROUTER_RETRIES`, or `LOCALROUTER_RETRY_BACKOFF` (seconds, linear). Default: 3 retries, 2s backoff.

## Use

`localrouter` uses a `<verb> [args]` shape. Run with no arguments for the interactive selector.

```bash
localrouter                              # interactive: pick a model + open tunnel
localrouter use qwen2.5-coder:7b         # pull/cp + open tunnel for this model
localrouter use qwen2.5-coder:7b --force # re-pull even if installed
localrouter use --keep                   # open/reuse tunnel, no model swap
localrouter list                         # configured models + installed status + size
localrouter list --refresh               # bypass the cache
localrouter status                       # remote, tunnel, last-tunnel/pull/use times
localrouter kill                         # close any running localrouter tunnel
localrouter restart                      # kill + reopen with the tracked model
localrouter info qwen2.5-coder:7b        # ollama metadata for the model
localrouter watch                        # foreground supervisor: auto-reconnect on drop
```

Pre-checks and timeouts: before any pull, `localrouter` runs `ollama list` over SSH as a fast preflight (skip with `--no-preflight`). If the model is already installed it skips the pull (override with `--force`). The pull itself is wrapped in `timeout` (default 30 min; `--pull-timeout SECS` or `LOCALROUTER_PULL_TIMEOUT`).

Installed-model state is cached at `~/.local/state/localrouter/installed.cache` (60 s TTL by default; `LOCALROUTER_CACHE_TTL`). `--refresh` invalidates it.

Auto-reconnect: `localrouter watch` blocks the terminal, opens the tunnel, restarts it on drop with exponential backoff capped at `LOCALROUTER_WATCH_BACKOFF_MAX` (default 30 s). Ctrl+C exits cleanly. Run under `tmux`/`nohup` if you want it detached.

### Legacy flag aliases

For backwards compatibility:

| Legacy flag             | New verb                |
|------------------------ |------------------------ |
| `--model MODEL`         | `use MODEL`             |
| `--status`              | `status`                |
| `--setup`               | `setup`                 |
| `--setup-windows`       | `setup windows`         |
| `--setup-ssh-key`       | `setup ssh-key`         |
| `--edit-models`         | `edit-models`           |
| `--init-config`         | `init-config`           |
| `--keep` (positional)   | `use --keep`            |

## Remote Requirements

On the remote host:

- SSH server is enabled and reachable from the local machine.
- The configured user can run `ollama pull` and `ollama cp`.
- Ollama listens on the remote host/port configured by `REMOTE_HOST` and `REMOTE_PORT`.

For Windows hosts, OpenSSH Server must be installed and running. Ollama must be available to the SSH session, which may require adding Ollama to the system `PATH` or using the shell profile for the SSH user.

## Security Model

This tool can touch privileged surfaces:

- It may run `sudo systemctl stop ollama` locally if local Ollama owns the forwarded port.
- It connects to a remote host over SSH.
- It asks the remote host to run `ollama pull` and `ollama cp`.
- `--setup-windows` asks the remote host to: change the `sshd` service start type, start `sshd`, set network connection profiles to `Private`, and create a Windows Firewall rule for `SSH_PORT`. Each of those steps is idempotent (checks current state before changing it) and the script is printed in `bin/localrouter` for inspection.
- `--setup-ssh-key` writes to the remote `authorized_keys` (or `administrators_authorized_keys`) file and runs `icacls` to tighten its ACL.

`localrouter` does not store passwords. Prefer SSH keys with passphrases and host-key verification. Do not disable SSH host-key checking in shared scripts or docs. `--setup-ssh-key` generates a key with no passphrase by design (passwordless automation); only run it on hosts where that trade-off is acceptable.

`WINDOWS_USER`, `WINDOWS_HOST`, ports, model names, and target tags are validated before being passed to `ssh` or PowerShell. Keep that validation strict if you add more remote operations.

## Current Limitations

- Installing the OpenSSH Server capability itself on Windows is still manual (chicken-and-egg with SSH connectivity).
- The local port conflict handler only knows how to stop a systemd `ollama` service.
- This is a Bash CLI for Linux/macOS clients. Native PowerShell client support is out of scope for now.
- `--setup-ssh-key` generates a passphrase-less key; if you want a passphrase, generate the key manually and add `-i <path>` to `SSH_OPTS` yourself.
- IPv6 literals in `WINDOWS_HOST` are not yet supported.
