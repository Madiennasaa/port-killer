# portkiller

A terminal UI port manager for Linux, built with Go, Bubble Tea, and Lip Gloss.
Data sources: `ss`, `/proc`, and `docker ps`.

## Features

- **Docker-aware**: containers are stopped with `docker stop`, not by killing `docker-proxy`
- **Project-aware**: PROJECT column from the process cwd plus markers (`.git`, `go.mod`, `composer.json`, `package.json`)
- **Health check**: parallel TCP dial per port
- **Orphan detector**: TCP, no tty, parent PID 1/systemd, belongs to a project
- **Blast radius**: the kill dialog shows connections, clients, and child processes
- **Detail panel**, **Stop project** (`P`), **Restart** (`R`)
- **Port Guard**: reserve ports in `~/.config/portkiller/guard.conf`
- **History** (`h`): ports appearing/disappearing, with the cause
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
| `F9` or `Enter` | Stop (SIGTERM / `docker stop`) with impact preview |
| `F8` or `K` | Force kill (SIGKILL) |
| `o` / `X` | Orphans only / clean up orphans |
| `i` or `Tab` | Detail panel |
| `h` | History |
| `P` | Stop all processes of the project |
| `R` | Restart last stopped |
| `?` or `F1` | Help |
| `F10` or `q` | Quit |

Mouse: click a row to select, click again (or right-click) to stop, scroll to move, click footer keys.

## Port Guard

Create `~/.config/portkiller/guard.conf`, one rule per line (`port=owner`; owner is a process, container, or project name):

    5432=postgres-dev

## Files

- `~/.config/portkiller/guard.conf`: guard rules
- `~/.local/state/portkiller/events.jsonl`: history log

## Development

    make test
    make build

## License

MIT
