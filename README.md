# portkiller

A terminal UI port manager for Linux, built with Go, Bubble Tea, and Lip Gloss.
Data sources: `ss`, `/proc`, `docker ps`, and `podman ps`.

## Features

- **Container-aware**: Docker and Podman containers are stopped with `docker stop` / `podman stop`, not by killing `docker-proxy` or `rootlessport`. Under sudo, the invoking user's rootless Podman containers are listed too and stopped as that user
- **Multi-owner aware**: a socket shared by several processes is listed once per PID
- **Project-aware**: PROJECT column from the process cwd plus markers (`.git`, `go.mod`, `composer.json`, `package.json`)
- **Git branch**: BRANCH column (140+ columns wide) and detail line read straight from `.git/HEAD`, worktrees included; the search filter matches it too
- **Health check**: parallel TCP dial per port
- **Orphan detector**: TCP, no tty, parent PID 1/systemd, belongs to a project
- **Blast radius**: the kill dialog shows connections, clients, and child processes
- **Respawn warning**: a listener run by pm2, supervisord, nodemon, forever, runit or s6 is flagged in the kill dialog and by `--kill`/`--dry-run`, with the command that stops it for good (`pm2 stop 3`, `supervisorctl stop web`)
- **Detail panel**, **Stop project** (`P`), **Restart** (`R`)
- **Port Guard**: reserve ports in `~/.config/portkiller/guard.conf`
- **History** (`h`): ports appearing/disappearing, with the cause
- **Conflict check** (`c`): ports declared in `docker-compose.yml` or `.env` of the current directory vs. running listeners
- **Multi-select**: mark rows with `Space`, then stop them all in one confirmation
- **Open in browser** (`b`): opens `http://localhost:<port>` as the desktop user, never as root
- **Hide noisy processes** (`H`): persisted in `~/.config/portkiller/hidden`; `U` shows them dimmed
- **Responsive layout + mouse support**

## Install

    go install github.com/Madiennasaa/port-killer/cmd/portkiller@latest

Or from source:

    git clone https://github.com/Madiennasaa/port-killer.git
    cd port-killer
    make install

Run `portkiller`. It re-executes itself with `sudo` so it can see all processes.

## Keybindings

| Key | Action |
|---|---|
| `j`/`k`, arrows, `PgUp`/`PgDn`, `g`/`G` | Navigate |
| `F3` or `/` | Search (`Esc` clears) |
| `F4` or `t` | Protocol: TCP / UDP / ALL |
| `F5` or `r` | Refresh (auto every 2s) |
| `F6` or `s` | Sort |
| `F9` or `Enter` | Stop (SIGTERM / `docker stop` / `podman stop`) with impact preview; acts on all marked rows if any |
| `F8` or `K` | Force kill (SIGKILL) |
| `Space` | Mark / unmark row (`Esc` clears marks and search) |
| `b` | Open the port in the browser |
| `H` / `U` | Hide / unhide the process name / show hidden rows |
| `o` / `X` | Orphans only / clean up orphans |
| `i` or `Tab` | Detail panel |
| `h` | History |
| `c` | Conflict check vs `docker-compose.yml` / `.env` |
| `P` | Stop all processes of the project (skips root/system processes) |
| `R` | Restart last stopped |
| `?` or `F1` | Help |
| `F10`, `q` or `Ctrl+C` | Quit |

Mouse: click a row to select, click again (or right-click) to stop, scroll to move, click footer keys.

## Port Guard

Create `~/.config/portkiller/guard.conf`, one rule per line (`port=owner`; owner is a process, container, or project name):

    5432=postgres-dev

## Files

- `~/.config/portkiller/guard.conf`: guard rules
- `~/.local/state/portkiller/events.jsonl`: history log
- `~/.config/portkiller/hidden`: process names hidden from the table
- `docs/portkiller.1`: man page, installed by `make install` to `/usr/local/share/man/man1`

## Development

    make test    # go vet + go test -race
    make cover   # coverage summary
    make build   # stamps the version from `git describe`

## License

MIT

## CLI mode

    portkiller --help            # usage
    portkiller --version         # version stamp
    portkiller --list            # table output
    portkiller --list --json     # machine-readable
    portkiller --list --plain    # tab-separated, for cut/awk
    portkiller --kill 8765       # SIGTERM, then SIGKILL after 3s; docker/systemd aware
    portkiller --kill 8765 --force
    portkiller --kill 3000 8080  # several ports in one command
    portkiller --kill 3000 --dry-run   # preview the impact, stop nothing
    portkiller --kill-name vite  # every listener named vite (or container "vite")
    portkiller --kill 8000 --signal HUP   # one signal, no SIGKILL escalation
    portkiller --kill 3000 --wait && npm run dev   # block until 3000 is free (10s, or --wait N)
    portkiller --free 3000-3010  # first bindable port in the range
    portkiller --free 3000-3010 --json
    portkiller --history         # recent appear/vanished events, newest first
    portkiller --history --port 3000 --limit 50
    portkiller --history --json
    portkiller --completions bash > ~/.local/share/bash-completion/completions/portkiller
    portkiller --completions zsh > "${fpath[1]}/_portkiller"
    portkiller --completions fish > ~/.config/fish/completions/portkiller.fish

Host processes are stopped with SIGTERM and get 3 seconds to exit before
SIGKILL. A process is only signalled when its PID still belongs to the same
start time captured during the scan, and a systemd system service is stopped
or restarted through `systemctl` instead of signalling its PID.

Restarted processes keep their original environment, are launched from
`/proc/<pid>/exe`, and write stdout/stderr to
`~/.local/state/portkiller/restart-<name>.log`.

Unknown flags exit with status 2 instead of silently starting the TUI.
`--help`, `--version`, `--free` and `--history` run without a sudo prompt.
`--free` checks TCP bindability, so ranges below 1024 need root (`sudo
portkiller --free 80-90`).

## MCP server (AI agents)

`portkiller --mcp` speaks the [Model Context Protocol](https://modelcontextprotocol.io) on stdio, so Claude Code, Cursor and other MCP clients can ask "who is on port 3000?" or "give me a free port" directly:

    claude mcp add portkiller -- portkiller --mcp

Generic client config:

```json
{ "mcpServers": { "portkiller": { "command": "portkiller", "args": ["--mcp"] } } }
```

| Tool | What it does |
|------|--------------|
| `list_ports` | Listeners with process, PID, project, branch, container; optional `port` / `process` / `proto` filters |
| `free_port` | First bindable TCP port in a `range` such as `3000-3010` |
| `port_history` | Recent appear/vanished events (`port`, `limit`) |
| `stop_port` | Stop by `ports` and/or `names` (`force`, `signal`, `dry_run`); flags pm2/supervisord-managed listeners |

The server is read-only by default: `stop_port` only previews (`dry_run`) until you start it with `--mcp --allow-kill`. It never re-runs itself under sudo (stdio belongs to the client), so without root, listeners owned by other users show no PID.
