# Linux Host Setup

Use this guide when the machine running Ollama is Linux and the user or client
machine will connect to it over the network through `localrouter`.

`localrouter` does not need to run on the host. The host only needs Ollama to
listen on a reachable address such as `0.0.0.0:11434`.

## Temporary Host Pipeline

This is the fastest way to test host exposure in one shell session:

```bash
OLLAMA_HOST=0.0.0.0:11434 ollama serve
```

In another shell on the host:

```bash
curl http://127.0.0.1:11434/api/tags
```

## Persistent Host Pipeline

If Ollama is running as a `systemd` service, persist the bind address with an
override:

```bash
sudo mkdir -p /etc/systemd/system/ollama.service.d
sudo tee /etc/systemd/system/ollama.service.d/localrouter.conf >/dev/null <<'EOF'
[Service]
Environment="OLLAMA_HOST=0.0.0.0:11434"
EOF
sudo systemctl daemon-reload
sudo systemctl restart ollama
sudo systemctl status ollama --no-pager
```

Check that Ollama is listening:

```bash
ss -ltn | grep ':11434'
```

## Open The Firewall

Pick the firewall your distro actually uses.

### `ufw`

```bash
sudo ufw allow 11434/tcp &&
sudo ufw status
```

### `firewalld`

```bash
sudo firewall-cmd --permanent --add-port=11434/tcp &&
sudo firewall-cmd --reload &&
sudo firewall-cmd --list-ports
```

If your distro uses another firewall stack, allow inbound TCP `11434` there.

## Find The Host IP

```bash
ip route get 1 | awk '{print $7; exit}'
```

Use the printed address as `HOST_IP` in the client-side `localrouter`
configuration.

## Verify From Another Machine

From the user or client machine:

```bash
curl http://HOST_IP:11434/api/tags
```

If that works, the host side is ready and you can point `localrouter` at:

```text
http://HOST_IP:11434
```

## Notes

- These Linux host steps were verified against the current Ollama Linux and
  FAQ docs.
- The `ufw` and `firewalld` examples are common Linux firewall recipes rather
  than Ollama-specific commands.

References:
- [Ollama Linux docs](https://docs.ollama.com/linux)
- [Ollama FAQ: configuring the server and exposing Ollama on your network](https://docs.ollama.com/faq)
