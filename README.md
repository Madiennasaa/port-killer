# portkiller

TUI manajer port untuk Linux (Go, Bubble Tea, Lip Gloss). Sumber data: ss, /proc, docker ps.

## Fitur

- Docker-aware: container dihentikan dengan `docker stop`, bukan kill docker-proxy
- Project-aware: kolom PROJECT dari cwd proses + penanda .git, go.mod, composer.json, package.json
- Health check: dial TCP paralel per port
- Orphan detector: TCP, tanpa tty, induk PID 1/systemd, punya proyek
- Blast radius: dialog kill menampilkan koneksi, klien, proses anak
- Detail panel, Stop proyek (P), Restart (R)
- Port Guard: ~/.config/portkiller/guard.conf (port=pemilik)
- Riwayat (h): port muncul/hilang + penyebab
- Responsive + mouse

## Keybind

j/k panah PgUp/PgDn g/G navigasi, F3 / cari, F4 t proto, F5 r refresh, F6 s urut,
F9 Enter stop, F8 K force, o orphan, X bersihkan orphan, i detail, h riwayat,
P stop proyek, R restart, ? bantuan, F10 q keluar.

## Pasang

    go install github.com/Madiennasaa/port-killer/cmd/portkiller@latest

    atau dari source:

    make install
    portkiller

## Port Guard

File ~/.config/portkiller/guard.conf, satu aturan per baris:

    5432=postgres-dev
