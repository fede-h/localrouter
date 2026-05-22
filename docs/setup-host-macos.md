# macOS Host Setup

Use this guide when the machine running Ollama is macOS and the user or client
machine will connect to it over the network through `localrouter`.

Per the current Ollama docs, macOS host support requires macOS Sonoma (14) or
newer. Apple Silicon supports CPU and GPU execution; Intel macOS is CPU-only.

## Host Pipeline

Set the bind address in the current user launchd session, restart the Ollama
app, and verify that it is listening:

```bash
launchctl setenv OLLAMA_HOST "0.0.0.0:11434"
osascript -e 'quit app "Ollama"' || true
open -a Ollama
lsof -nP -iTCP:11434 -sTCP:LISTEN
curl http://127.0.0.1:11434/api/tags
```

`launchctl setenv` is the supported way to persist Ollama app environment
variables for the logged-in macOS user session.

## Find The Host IP

```bash
IFACE="$(route get default | awk '/interface:/{print $2; exit}')" &&
ipconfig getifaddr "$IFACE"
```

Use that address as `HOST_IP` in the client-side `localrouter` configuration.

## Firewall Check

If the macOS firewall is enabled, allow `Ollama.app` to receive incoming
connections:

1. Open `System Settings`.
2. Go to `Network > Firewall`.
3. Click `Options`.
4. Add `Ollama.app` and set it to `Allow incoming connections`.

If the firewall is off, there is nothing extra to change here.

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

- These macOS host steps were verified against the current Ollama FAQ and
  macOS docs plus Apple's current firewall guidance.
- `localrouter` itself does not need any special macOS service integration on
  the host because the host is only serving Ollama.

References:
- [Ollama FAQ: configuring the server and exposing Ollama on your network](https://docs.ollama.com/faq)
- [Ollama macOS docs](https://docs.ollama.com/macos)
- [Apple: Block connections to your Mac with a firewall](https://support.apple.com/en-euro/guide/mac-help/mh34041/mac)
