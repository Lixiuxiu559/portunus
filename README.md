<p align="center">
  <img src="web/src/renderer/public/logo.png" alt="Portunus Logo" width="180" />
</p>

<h1 align="center">Portunus</h1>

<p align="center">
  <strong>English</strong> |
  <a href="./README_CN.md">简体中文</a>
</p>

> One endpoint for every LLM — multi-channel routing, protocol translation, and usage logging.

Portunus is a lightweight LLM API aggregation service: connect multiple upstream providers, price by channel × model, expose a unified `/v1` endpoint for clients like Claude Code and Codex.

## ✨ Features

- **Multi-Channel Aggregation** — Connect multiple upstream providers through a single entry point. Supports OpenAI Chat / Responses, Anthropic, and Gemini.
- **Model Auto-Sync** — Automatically pull model lists when saving a channel, with one-click manual sync available.
- **Protocol Translation** — Seamless conversion between OpenAI Chat / Responses, Anthropic, and Gemini, with SSE streaming passthrough.
- **Automatic Failover** — Multi-model groups with priority-based failover — switch to the next upstream when one fails.
- **Per-Model Pricing** — Prices tied to channel × model, auto-filled with built-in defaults and manual override supported.
- **4-Dimensional Billing** — Input, output, cache-read, and cache-write tokens tracked and billed separately.
- **Lightweight** — Single binary + SQLite, zero external dependencies.

## 🚀 Quick Start

### Desktop App (Electron)

Portunus ships as a self-contained Electron desktop app: the Go backend is bundled as a sidecar process, so one installer is all a customer needs — no separate deployment.

Build installers locally:

```bash
cd web
pnpm install
pnpm run build:mac   # macOS DMG / ZIP (arm64)
pnpm run build:win   # Windows NSIS (x64)
```

Artifacts land in `web/dist-electron/`. Pushing a `v*` tag triggers GitHub Actions, which builds both platforms and uploads a **draft** GitHub Release — review it, then publish manually.

### Run from Source

**Requirements:** Go 1.26+

```bash
git clone https://github.com/Lixiuxiu559/portunus.git
cd portunus
GOPROXY=https://goproxy.cn,direct go mod tidy
go run .
```

Listens on `0.0.0.0:3061` by default. Database: `data/portunus.db` (SQLite).

### Development (Management UI)

The management UI lives in `web/` (Electron + React + Vite). The dev server proxies `/api` and `/v1` to the backend at `localhost:3061`, so start the backend first (`go run .`).

```bash
cd web
pnpm install        # install dependencies
pnpm run dev:web    # start Vite and open http://localhost:5173
pnpm run dev        # start Vite + Electron desktop window
```

## 📝 Configuration

A `config.json` is auto-generated on first run:

```json
{
  "server": { "host": "0.0.0.0", "port": 3061 },
  "database": { "type": "sqlite", "path": "data/portunus.db" }
}
```

| Option | Description | Default |
|---|---|---|
| `server.host` | Listen address | `0.0.0.0` |
| `server.port` | Server port | `3061` |
| `database.type` | Database type (`sqlite` / `mysql` / `postgres`) | `sqlite` |
| `database.path` | Database path | `data/portunus.db` |

Override via environment variables:

| Variable | Maps to |
|---|---|
| `PORTUNUS_SERVER_PORT` | `server.port` |
| `PORTUNUS_DATABASE_TYPE` | `database.type` |
| `PORTUNUS_DATABASE_PATH` | `database.path` |

## 🔌 API

### Gateway (`/v1` — API Key required)

| Method | Path | Description |
|---|---|---|
| GET | `/v1/models` | Model list (group names as model IDs) |
| POST | `/v1/chat/completions` | OpenAI Chat |
| POST | `/v1/responses` | OpenAI Responses |
| POST | `/v1/messages` | Anthropic Messages |

Auth: `Authorization: Bearer <key>` or `x-api-key`.

### Management (`/api`)

| Path | Description |
|---|---|
| `/api/ping` | Health check |
| `/api/channels` | Channel CRUD + model sync |
| `/api/models` | Model CRUD |
| `/api/groups` | Group & group item CRUD |
| `/api/apikeys` | API Key management |
| `/api/settings` | Global settings |
| `/api/logs` | Log query & statistics |

## 📖 More Docs

- [ARCHITECTURE.md](ARCHITECTURE.md) — Architecture, core concepts & design decisions
- [docs/research](docs/research) — Design research notes