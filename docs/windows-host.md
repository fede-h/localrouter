# Windows Host Notes

`localrouter --setup-windows` automates most of this. This document describes the **one-time manual step** that must happen on the Windows host before that command can run, and details every change `--setup-windows` and `--setup-ssh-key` make.

## One-time manual step: install OpenSSH Server

On the Windows host, in an elevated PowerShell window:

```powershell
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Start-Service sshd
Set-Service -Name sshd -StartupType Automatic
New-NetFirewallRule -DisplayName "localrouter SSH inbound" `
    -Direction Inbound -Action Allow -Protocol TCP -LocalPort 22 -Profile Any -Enabled True
```

Once `ssh user@host` works at all from the Linux client, the rest can be re-asserted from the client side.

## Confirm Ollama works locally on Windows

```powershell
ollama list
ollama pull qwen2.5-coder:7b-instruct-q4_K_M
```

Verify from the Linux client:

```bash
ssh your-windows-user@192.168.1.50 "ollama list"
```

If `ollama` is not found over SSH, add its install directory (e.g. `C:\Users\<you>\AppData\Local\Programs\Ollama`) to the **System** PATH and reconnect.

## What `--setup-windows` does

Every step is idempotent — it checks state before changing anything.

| Step | Action | Reverts how |
| ---- | ------ | ----------- |
| 1 | `Set-Service sshd -StartupType Automatic` (only if currently not Automatic) | `Set-Service sshd -StartupType Manual` |
| 1 | `Start-Service sshd` (only if currently not Running) | `Stop-Service sshd` |
| 2 | `Set-NetConnectionProfile -NetworkCategory Private` on each interface whose profile is not already `Private` | Re-classify via Settings → Network |
| 3 | `New-NetFirewallRule -DisplayName 'localrouter SSH inbound' …` (only if a rule by that exact name does not exist) | `Remove-NetFirewallRule -DisplayName 'localrouter SSH inbound'` |
| 4 | Read-only: `Get-Command ollama` | n/a |
| 5 | Read-only: `Get-NetTCPConnection -State Listen -LocalPort $SSH_PORT` | n/a |

Steps 1–3 modify system state and require the SSH user to be an Administrator. The script prints what it changed and what it skipped; nothing happens silently.

## What `--setup-ssh-key` does

1. On the Linux client, generates `~/.ssh/localrouter_<host>` (ed25519, no passphrase, comment `localrouter@<host>`) if it does not already exist.
2. Over SSH, runs a PowerShell snippet that:
   - detects whether the SSH user is in the local `Administrators` group;
   - chooses the authorized-keys file accordingly (admins use `C:\ProgramData\ssh\administrators_authorized_keys`, regular users use `%USERPROFILE%\.ssh\authorized_keys`) — this matches the default Windows OpenSSH `Match Group administrators` config;
   - creates the directory and file if missing;
   - appends the public key only if the exact line is not already present;
   - runs `icacls` to set `inheritance:r` and grant `F` only to the appropriate principal (`Administrators` + `SYSTEM` for the admin path, the user for the per-user path).
3. Merges `-i ~/.ssh/localrouter_<host>` into `SSH_OPTS` in `~/.config/localrouter/config` (idempotent).

## Retries and fallbacks

Every remote operation (`--setup-windows`, `--setup-ssh-key`, `--model …`) runs inside a retry loop. The default is 3 retries with 2-second linear backoff (attempt 1 → 2 s → attempt 2 → 4 s → attempt 3). Override:

```bash
localrouter --retries 5 --setup-windows
LOCALROUTER_RETRY_BACKOFF=5 localrouter --setup-windows
```

After exhausting retries, `localrouter` exits non-zero and prints the last underlying exit code. SSH connection-level failures (auth, host-key) usually do not benefit from retry — fix the underlying problem rather than raising the count.
