# Portunus

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

### Docker Compose (Recommended)

Create a directory with a `docker-compose.yml`:

```yaml
services:
  # Backend: Go service (internal network only, accessed via web reverse proxy)
  backend:
    image: lijx559/portunus-backend:latest
    container_name: portunus-backend
    restart: unless-stopped
    environment:
      PORTUNUS_SERVER_HOST: 0.0.0.0
      PORTUNUS_SERVER_PORT: "3060"
      PORTUNUS_DATABASE_PATH: /app/data/portunus.db
    volumes:
      - ./data:/app/data

  # Frontend: nginx static hosting + reverse proxy for /api and /v1
  web:
    image: lijx559/portunus-web:latest
    container_name: portunus-web
    restart: unless-stopped
    depends_on:
      - backend
    ports:
      - "3060:80"
```

```bash
docker compose up -d
```

Then open `http://localhost:3060`:

| Path | Description |
|---|---|
| `http://localhost:3060/` | Web management UI |
| `http://localhost:3060/api/*` | Management API (same origin as the UI, no CORS needed) |
| `http://localhost:3060/v1/*` | LLM gateway (set base_url to `http://localhost:3060` for Claude Code / Codex) |

**Custom data directory**: the SQLite database lands in `./data` next to the compose file by default. Change the host-side path in volumes to move it anywhere:

```yaml
    volumes:
      - /your/custom/path:/app/data
```

> ⚠️ The image sets `PORTUNUS_DATABASE_PATH=/app/data/portunus.db`, so only change the host-side path (left side of the colon); keep the container-side path (`/app/data`) unchanged.

### Docker Run

Prefer plain Docker? Two commands (create a network first; the backend gets no host port — web proxies it):

```bash
docker network create portunus

docker run -d --name portunus-backend \
  --network portunus \
  -v /path/to/data:/app/data \
  lijx559/portunus-backend:latest

docker run -d --name portunus-web \
  --network portunus \
  -p 3060:80 \
  lijx559/portunus-web:latest
```

Images are multi-arch (linux/amd64 + linux/arm64).

### Run from Source

**Requirements:** Go 1.26+

```bash
git clone https://github.com/Lixiuxiu559/portunus.git
cd portunus
GOPROXY=https://goproxy.cn,direct go mod tidy
go run .
```

Listens on `0.0.0.0:3060` by default. Database: `data/portunus.db` (SQLite).

### Frontend (Management UI)

The management UI lives in `web/` (Electron + React + Vite). The dev server proxies `/api` and `/v1` to the backend at `localhost:3060`, so start the backend first (`go run .`).

```bash
cd web
pnpm install        # install dependencies
pnpm run dev:web    # start Vite and open http://localhost:5173
pnpm run dev        # start Vite + Electron desktop window
pnpm run build      # build renderer and package the desktop app
```

## 📝 Configuration

A `config.json` is auto-generated on first run:

```json
{
  "server": { "host": "0.0.0.0", "port": 3060 },
  "database": { "type": "sqlite", "path": "data/portunus.db" }
}
```

| Option | Description | Default |
|---|---|---|
| `server.host` | Listen address | `0.0.0.0` |
| `server.port` | Server port | `3060` |
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