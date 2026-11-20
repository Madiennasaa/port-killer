# Changelog

All notable changes to portkiller are listed here. Versions follow
[Semantic Versioning](https://semver.org).

## v0.2.0 - 2026-11-20

### Features

- CLI mode: `--list` (`--json`, `--plain`), `--kill` with several ports,
  `--kill-name`, `--signal`, `--wait`, `--dry-run` impact preview and `--force`
- `--restart`: stop a listener and relaunch the same command once its port is
  free, with framework detection (Vite, Next.js, Django, Rails, `go run`, ...)
- `--free START-END` prints the first bindable port; `--history` prints
  appear/vanish events; `--watch` tails events with optional `--notify`
- Built-in MCP server (`portkiller --mcp`, stop/restart behind `--allow-kill`)
- Pre-launch port conflict check from compose files and `.env`
- Guard rules with desktop notifications and a terminal bell on violations
- Podman support, multi-select stop, open in browser, hide processes
- Per-process CPU and memory usage, project git branch, and a warning when a
  process manager (pm2, supervisord, systemd) may restart a stopped listener
- Shell completions (bash, zsh, fish) and a man page
- English UI, README and code comments

### Fixes

- Verify `ss` socket owners against `/proc/<pid>/comm`, so a process name
  crafted to look like another pid cannot redirect a stop
- Never signal pid 0, pid 1, negative pids or portkiller itself
- Parse `ss` process names containing spaces and skip non-numeric ports
- Expand published container port ranges (`8000-8010`) into individual ports
- Probe IPv6-only wildcards on `::1` and handle interface suffixes in health
  checks
- List the processes connected to a port as its clients, not the server
- Harden state files against FIFOs, symlinks, hard links, partial writes and
  unbounded growth; history and restart logs are private (0600)
- Keep the original `argv[0]` when restarting through the captured executable
- Sequence TUI scans so a slow scan cannot overwrite a newer one, and send
  guard notifications off the update loop
- Stop the sudo re-exec from looping when sudo does not elevate
- Validate CLI arguments and option values up front: unknown flags,
  conflicting modes and bad ports, signals or wait times exit 2 before any
  sudo prompt
- MCP: correct JSON-RPC errors for malformed requests, recover handler panics,
  reject unknown tool arguments (a mistyped `dry_run` no longer stops for real),
  clamp the history limit and normalize ports
- Show marks hidden by the filter in the info bar
- Keep the TUI usable under `NO_COLOR` and on very small terminals; cache
  uptime and root status instead of reading them on every render
- `--watch` ignores failed scans and prints each guard violation once
- Safer stop escalation, systemd unit handling and restart bookkeeping

### Docs

- README, man page and `--help` describe restart/stop hardening, state-file
  handling, `--watch`, keys, `NO_COLOR`, exit codes and MCP argument checks

### Internal

- Test suite at 100% statement coverage, isolated from the developer's real
  state; the CI coverage gate now fails on any uncovered block
- CI actions bumped to `actions/checkout@v7` and `actions/setup-go@v7`

## v0.1.0

- Initial release: interactive TUI to list listening ports and stop their
  processes.
