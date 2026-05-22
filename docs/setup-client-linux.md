# Linux Client Setup

Use this guide when `localrouter` runs on the user's Linux machine and Ollama
runs on another host.

## Install Pipeline

User-local install:

```bash
git clone https://github.com/fede-h/localrouter &&
cd localrouter &&
PREFIX="$HOME/.local" ./install.sh
```

System-wide install:

```bash
git clone https://github.com/fede-h/localrouter &&
cd localrouter &&
sudo ./install.sh
```

If you install to `~/.local/bin` and that directory is not on `PATH`, add it
to your shell profile and reload the shell:

```bash
printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >> ~/.bashrc &&
. ~/.bashrc
```

Check the binary:

```bash
localrouter version
```

## Configure Pipeline

Replace `HOST_IP` with the address from the Linux, macOS, or Windows host
guide:

```bash
localrouter init-config \
  --remote http://HOST_IP:11434 \
  --default-model qwen2.5-coder:7b \
  --auto-pull=true
```

Print the resolved config after writing it:

```bash
localrouter config
```

To update an existing config non-interactively later:

```bash
localrouter init-config \
  --force \
  --remote http://HOST_IP:11434 \
  --default-model qwen2.5-coder:7b \
  --auto-pull=true
```

## Run Pipeline

Foreground:

```bash
localrouter serve
```

Background:

```bash
localrouter start &&
localrouter status
```

Stop:

```bash
localrouter stop
```

## Verify The Local Endpoint

```bash
curl http://localhost:11434/__localrouter/healthz &&
curl http://localhost:11434/api/tags
```

Now point your editor or app at:

```text
http://localhost:11434
```

## Notes

- `localrouter start` is the convenience background mode for a user shell.
- For long-running supervision, prefer `localrouter serve` under your own
  service manager, terminal multiplexer, or session supervisor.
- `localrouter config` prints the exact config and cache paths for the current
  user on Linux.
