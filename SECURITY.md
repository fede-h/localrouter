# Security Policy

## Supported Versions

Security fixes target the latest `main` branch until the project starts publishing tagged releases.

## Reporting a Vulnerability

Open a private security advisory on GitHub if the repository has advisories enabled. Otherwise, contact the maintainer privately before opening a public issue.

## Security Notes for Contributors

This project coordinates local shell commands, SSH, and remote commands. Treat every new command path as security-sensitive.

Rules for changes:

- Do not add hardcoded private hostnames, IP addresses, usernames, or keys.
- Do not pass unvalidated user input to `ssh`, `sudo`, `systemctl`, PowerShell, `cmd.exe`, or shell interpreters.
- Prefer narrow commands over generic remote command execution.
- Keep SSH host-key verification enabled.
- Avoid storing credentials. Use the platform SSH agent or password prompt.
- Document every new privileged action in `README.md`.

Remote Windows administration commands are not shipped yet because they need explicit review. Any future setup automation should be idempotent, narrowly scoped, and easy to audit before execution.
