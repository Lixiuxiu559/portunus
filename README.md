<p align="center">
  <img src="web/src/renderer/public/logo.png" alt="Portunus Logo" width="180" />
</p>

<h1 align="center">Portunus</h1>

<p align="center">
  <strong>English</strong> |
  <a href="./README_CN.md">简体中文</a>
</p>

> One endpoint for every LLM — multi-channel routing, protocol translation, and usage logging.

Portunus is a lightweight LLM API aggregation service: connect multiple upstream providers, price by channel × model, and expose groups as a unified `/v1` endpoint for clients like Claude Code and Codex — all managed from a single desktop app.

## ✨ Features

- **Desktop App, Ready Out of the Box** — Electron shell with the Go backend bundled as a sidecar process: install one app and go, no separate deployment. In-app auto-update, listen-mode switch (localhost / LAN), light & dark themes.
- **Multi-Channel Aggregation** — Manage multiple upstream providers behind one entry point. Supports OpenAI Chat / Responses, Anthropic, and Gemini.
- **Protocol Translation** — Automatic conversion when client and upstream speak different protocols, with SSE streaming passthrough and streaming tool calls across all protocols. Append `-thinking` to a model name to toggle thinking mode (`reasoning_content` aligned across protocols).
- **Smart Routing & Resilience** — Per-group routing strategy: manual / round-robin / failover, with drag-to-sort priorities. Fast failover on stalled upstreams, automatic retries and circuit breaking.
- **One-Click Client Onboarding** — Manage Claude Code and Codex config files right inside the app: service URL and token written automatically, per-slot model mapping (including a 1M-context slot), auto backup before changes and one-click rollback.
- **Per-Model Pricing, 4-Dimensional Billing** — Prices tied to channel × model; input, output, cache-read, and cache-write tokens tracked and billed separately. Built-in default prices applied on model sync, manual override supported.
- **Call Logs & Statistics** — Every call logged with protocol, model, group, latency, tokens, and cost (plus stream flag and request ID); optional retention-based auto cleanup.
- **Model Auto-Sync** — Model lists pulled automatically when saving a channel, with scheduled auto-sync and one-click full sync.
- **Lightweight** — Single binary + SQLite, zero external dependencies.

## 🚀 Quick Start

### Desktop App (Electron)

Portunus ships as a self-contained Electron desktop app: the Go backend is bundled as a sidecar process, so one installer is all a customer needs — no separate deployment.

Grab an installer from [GitHub Releases](https://github.com/Lixiuxiu559/portunus/releases/latest), or build locally.

### Install

**macOS (Apple Silicon) — command-line install recommended**

Command-line download + unzip never sets the quarantine flag, so Gatekeeper is not involved at all:

```bash
curl -L -o /tmp/portunus.zip https://github.com/Lixiuxiu559/portunus/releases/latest/download/Portunus-mac-arm64.zip
unzip -o /tmp/portunus.zip -d /Applications
```

If you install from a browser-downloaded DMG instead, the first launch shows **"Portunus.app is damaged and can't be opened"** (macOS Gatekeeper policy for unsigned apps, not actual damage). Run the following and reopen, or click "Open Anyway" in System Settings → Privacy & Security (one time only):

```bash
xattr -rd com.apple.quarantine /Applications/Portunus.app
```

**Windows (x64)**

Double-click the NSIS installer to install; the ZIP is a portable build — just unzip and run. SmartScreen may warn about an unknown publisher — click **More info → Run anyway**.

**Linux (x64)**

Download the AppImage and run it:

```bash
chmod +x Portunus-*-x64.AppImage
./Portunus-*-x64.AppImage
```

### Build Locally

```bash
cd web
pnpm install
pnpm run build:mac   # macOS DMG / ZIP (arm64)
pnpm run build:win   # Windows NSIS / portable ZIP (x64)
```

Artifacts land in `web/dist-electron/`. Pushing a `v*` tag triggers GitHub Actions, which builds all three platforms (macOS / Windows / Linux) and uploads a **draft** GitHub Release — review it, then publish manually.

### Run from Source

**Requirements:** Go 1.26+

```bash
git clone https://github.com/Lixiuxiu559/portunus.git
cd portunus
GOPROXY=https://goproxy.cn,direct go mod tidy
go run .
```

The server listens on `0.0.0.0:3060` by default, with a SQLite database under `data/`. A `config.json` is auto-generated on first run; every option can be overridden via `PORTUNUS_*` environment variables (e.g. `PORTUNUS_SERVER_PORT`, `PORTUNUS_DATABASE_PATH`).

### Development (Management UI)

The management UI lives in `web/` (Electron + React + Vite). Start the backend first; the dev server proxies `/api` and `/v1` to the backend at `localhost:3060`.

**Requirements:** Node.js 20+ and pnpm

```bash
go run .            # terminal 1: start the Go backend

cd web              # terminal 2: start the frontend
pnpm install
pnpm run dev:web    # browser development: open http://localhost:5173
pnpm run dev        # or an Electron desktop window (waits for the backend automatically)
```

Backend tests: `go test ./...`.

## 📖 More Docs

- [ARCHITECTURE.md](ARCHITECTURE.md) — Architecture, core concepts & design decisions
- [docs/research](docs/research) — Design research notes
