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

Create config files:

```bash
localrouter --init-config
```

Edit `~/.config/localrouter/config`:

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
```

Edit model choices in `~/.config/localrouter/models.list`, or use:

```bash
localrouter --edit-models
```

## Use

Interactive selector:

```bash
localrouter
```

Prepare a model and open the tunnel:

```bash
localrouter --model qwen2.5-coder:7b-instruct-q4_K_M
```

Keep the current remote model and only open or reuse the tunnel:

```bash
localrouter --keep
```

Show status:

```bash
localrouter --status
```

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

`localrouter` does not store passwords. Prefer SSH keys with passphrases and host-key verification. Do not disable SSH host-key checking in shared scripts or docs.

Model names and target tags are validated before being sent to the remote shell. Keep that validation strict if you add more remote operations.

## Current Limitations

- Windows setup is documented only as requirements, not automated. The exact PowerShell commands for enabling OpenSSH, configuring firewall rules, and validating Ollama are still intentionally left for curation.
- The local port conflict handler only knows how to stop a systemd `ollama` service.
- This is a Bash CLI for Linux/macOS clients. Native PowerShell client support is out of scope for now.
