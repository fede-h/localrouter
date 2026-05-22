# Windows Host Setup

Use this guide when the machine running Ollama is Windows and the user or
client machine will connect to it over the network through `localrouter`.

Per the current Ollama docs, Windows host support targets Windows 10 22H2 or
newer. The normal installer runs Ollama as a user application in the
background and provides the `ollama` CLI in `cmd` and PowerShell.

## Temporary Host Pipeline

For a quick test in the current PowerShell session:

```powershell
$env:OLLAMA_HOST = "0.0.0.0:11434"
ollama serve
```

In a second PowerShell window on the host:

```powershell
Invoke-WebRequest http://127.0.0.1:11434/api/tags
```

## Persistent Host Pipeline

Persist the bind address for the current Windows user:

```powershell
[Environment]::SetEnvironmentVariable("OLLAMA_HOST", "0.0.0.0:11434", "User")
```

Then quit the Ollama tray app and launch it again from the Start menu so it
inherits the updated environment.

## Open The Firewall

Run this in an elevated PowerShell:

```powershell
New-NetFirewallRule -DisplayName "Ollama API 11434" -Direction Inbound -Action Allow -Protocol TCP -LocalPort 11434
```

Check the listener:

```powershell
Get-NetTCPConnection -LocalPort 11434 -State Listen
```

## Find The Host IP

```powershell
Get-NetIPAddress -AddressFamily IPv4 |
  Where-Object {
    $_.IPAddress -notlike '127.*' -and
    $_.IPAddress -notlike '169.254*' -and
    $_.InterfaceAlias -notmatch 'Loopback'
  } |
  Select-Object InterfaceAlias, IPAddress
```

Use the correct LAN address from that list as `HOST_IP` in the client-side
`localrouter` configuration.

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

- These Windows host steps were verified against the current Ollama Windows
  docs and FAQ.
- The persistent `OLLAMA_HOST` recommendation uses the user environment scope
  because that is the path Ollama documents for the standard Windows app flow.
- If you run Ollama via a separate service wrapper, set `OLLAMA_HOST` inside
  that service environment instead.

References:
- [Ollama Windows docs](https://docs.ollama.com/windows)
- [Ollama FAQ: configuring the server and exposing Ollama on your network](https://docs.ollama.com/faq)
- [Microsoft Learn: New-NetFirewallRule](https://learn.microsoft.com/en-us/powershell/module/netsecurity/new-netfirewallrule?view=windowsserver2025-ps)
