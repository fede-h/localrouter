# Initial Audit Notes

These notes capture the migration from the original `/usr/local/bin/win-ai` script.

## Issues Found

- The script hardcoded a private Windows username and IP address.
- Remote model names were interpolated into an SSH command without validation.
- The script could stop local Ollama with `sudo systemctl stop ollama`, but that privileged behavior was not documented.
- Runtime files lived directly in `$HOME` instead of XDG config/state paths.
- There was no install path, README, license, security policy, or contribution guidance.
- The Windows host setup process was not curated enough to automate safely.

## Migration Changes

- Moved the command into `bin/localrouter`.
- Added user config in `~/.config/localrouter/config`.
- Added model list in `~/.config/localrouter/models.list`.
- Added tracked model state in `~/.local/state/localrouter/current_model`.
- Added strict Ollama model/tag validation before remote execution.
- Added an installer that can also create a `win-ai` compatibility symlink.
- Documented local sudo and remote SSH behavior.
