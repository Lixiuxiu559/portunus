<p align="center">
  <img src="web/src/renderer/public/logo.png" alt="Portunus" width="112" />
</p>

<h1 align="center">Portunus</h1>

<p align="center">
  <strong>Your models. Your tools. One place to connect them.</strong>
</p>

<p align="center">
  A desktop LLM gateway for individual developers.<br />
  Bring your API providers together, connect Claude Code and Codex, and track usage and costs for every call.
</p>

<p align="center">
  <a href="https://github.com/Lixiuxiu559/portunus/releases/latest"><strong>Download Portunus</strong></a> ·
  <a href="#get-started-in-three-steps">Get started</a> ·
  <a href="https://github.com/Lixiuxiu559/portunus/issues">Report an issue</a>
</p>

<p align="center">
  macOS · Windows · Linux<br />
  <strong>English</strong> | <a href="./README_CN.md">简体中文</a>
</p>

---

## Give your models a home on your desktop

Working with multiple AI providers means juggling endpoints, API keys, and client configuration files. When a provider fails, you find a replacement by hand. When you want to understand your usage, you check several dashboards.

Portunus brings that work into one desktop app. Connect your providers, organize models into groups, and connect your favorite clients to those groups. Change providers or routing later while your clients keep using the same endpoint.

**Install one app and get started.** The backend ships with the installer. There is no separate server, Docker setup, or database to deploy.

<p align="center">
  <img src="docs/images/groups-light.png" alt="Portunus in light mode: model groups and failover priorities" width="1200" />
</p>
<p align="center"><sub>Organize models around your workflow and give them a fallback plan.</sub></p>

> Screenshots show the running product with a separate copy of local data. Channel names and service addresses have been anonymized. Prices and usage reflect local configuration and records, not service pricing.

## Built around your development workflow

### Connect once. Switch when you need to.

Configure Claude Code and Codex endpoints, tokens, and model mappings inside the app. Map Claude Code model slots, including 1M context markers. Configuration files are backed up before changes, with one-click rollback when needed.

### Multiple protocols. One entry point.

Connect OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini providers. Portunus translates between client and upstream protocols, with support for SSE streaming and tool calls, giving you more flexibility in choosing providers.

### Give your daily models a backup plan

Combine models from different providers into a group named for its purpose, such as `coding` or `daily`. The group name becomes the model name your client uses; Portunus handles the upstream selection.

| Routing strategy | How it works | When to use it |
| --- | --- | --- |
| Manual | Uses the model you select | Keep a preferred model and switch on demand |
| Round-robin | Distributes requests across group members in turn | Spread calls across multiple providers |
| Failover | Tries models in priority order, moving on after failure | Add backup providers to a daily workflow |

Retries, timeout handling, and circuit breaking help reduce disruption from upstream failures. Drag to reorder priorities.

### Understand the cost of each call

Call logs capture the model, channel, group, protocol, latency, tokens, cost, and request ID. Input, output, cache-read, and cache-write tokens are priced separately. The same model can have different prices on different channels.

Model sync can apply built-in default prices, and you can override them. **Costs are local estimates calculated from configured prices and reported usage.**

<p align="center">
  <img src="docs/images/models-dark.png" alt="Portunus in dark mode: per-channel model prices for input, output, and cache tokens" width="1200" />
</p>
<p align="center"><sub>Compare model prices across channels, including cache reads and writes.</sub></p>

<p align="center">
  <img src="docs/images/logs-dark.png" alt="Portunus in dark mode: call logs, cost totals, token usage, and time to first token" width="1200" />
</p>
<p align="center"><sub>Go from cost totals to individual calls, cache usage, and response timing.</sub></p>

### Keep everyday maintenance on your desktop

Sync model lists when saving a channel, on a schedule, or across all channels on demand. Automatically clean up logs by retention period. Switch between light and dark themes, update inside the app, and choose localhost or LAN access.

## Get started in three steps

### 1. Download and open

Choose the installer for your system from [GitHub Releases](https://github.com/Lixiuxiu559/portunus/releases/latest):

| Platform | Architecture | Package |
| --- | --- | --- |
| macOS | Apple Silicon (arm64) | DMG or ZIP |
| Windows | x64 | EXE installer or portable ZIP |
| Linux | x64 | AppImage |

If your system blocks the first launch, see [Installation help](#installation-help) below.

### 2. Add a channel and create a group

In Channels, enter your provider's endpoint, API key, and protocol type, then save to sync its model list. In Groups, select models and choose a routing strategy. For automatic fallback, choose Failover and set the priority order.

### 3. Connect your development tools

Open Clients, choose the groups for Claude Code or Codex, and apply the connection configuration. Restart your client to begin sending requests through Portunus, then open Logs to inspect usage and costs.

Other compatible clients can connect using the gateway address, a Portunus token, and a group name. The packaged desktop app defaults to `http://127.0.0.1:13060`; the OpenAI-compatible base URL is `http://127.0.0.1:13060/v1`.

## Installation help

<details>
<summary><strong>macOS: installation and first launch</strong></summary>

Move the app to Applications. Alternatively, download and extract the latest ZIP from a terminal:

```bash
curl -fL -o /tmp/portunus.zip https://github.com/Lixiuxiu559/portunus/releases/latest/download/Portunus-mac-arm64.zip
unzip -o /tmp/portunus.zip -d /Applications
```

Current builds are unsigned. If macOS blocks launch, check System Settings → Privacy & Security for an Open Anyway option. If it reports that the app “is damaged and can't be opened,” confirm the package came from this repository's release, then remove its quarantine attribute and reopen:

```bash
xattr -rd com.apple.quarantine /Applications/Portunus.app
```

</details>

<details>
<summary><strong>Windows: installer and portable edition</strong></summary>

Run the EXE installer and select an installation directory, or extract the ZIP and launch the app directly. If SmartScreen reports an unknown publisher, confirm the package source, then choose More info → Run anyway.

</details>

<details>
<summary><strong>Linux: run the AppImage</strong></summary>

Download `Portunus-linux-x64.AppImage` from the release, make it executable, and launch it:

```bash
chmod +x Portunus-linux-x64.AppImage
./Portunus-linux-x64.AppImage
```

</details>

## Frequently asked questions

**Does Portunus include models or API credits?**

Portunus connects providers, routes requests, and records usage. Bring your own provider endpoints and API keys. Model availability, quotas, and actual billing are determined by your upstream providers.

**Where is my data stored?**

The app stores configuration and call logs locally in SQLite. Requests are forwarded to the upstream services you configure. The desktop app listens on localhost by default, with optional LAN access in settings.

**Does protocol translation make every model capability interchangeable?**

Translation supports interactions such as messages, streaming, and tool calls. Context limits, reasoning capabilities, and upstream-hosted tools still depend on the provider. Setting a 1M context marker does not extend a model's actual context limit.

## Development and builds

<details>
<summary><strong>Run from source</strong></summary>

Requirements: Go 1.26.6+, Node.js 24 (matching the release workflow), and pnpm 10.

```bash
git clone https://github.com/Lixiuxiu559/portunus.git
cd portunus
go mod download
go run .
```

The backend defaults to `0.0.0.0:3060`, with SQLite data files under `data/`. Configure it through `config.json` or `PORTUNUS_*` environment variables, including `PORTUNUS_SERVER_HOST`, `PORTUNUS_SERVER_PORT`, and `PORTUNUS_DATABASE_PATH`.

Keep the backend running and start the management UI in a second terminal:

```bash
cd web
pnpm install
pnpm run dev:web    # Browser: http://localhost:5173
# Or use the desktop development window:
pnpm run dev
```

The development server proxies `/api` and `/v1` to `localhost:3060`. Reading and writing client configuration files requires the Electron desktop environment.

</details>

<details>
<summary><strong>Build installers and run tests</strong></summary>

Run backend tests from the repository root:

```bash
go test ./...
```

Install dependencies, run frontend tests, or build installers from `web/`:

```bash
cd web
pnpm install
pnpm test
pnpm run build:mac   # macOS DMG / ZIP (arm64)
pnpm run build:win   # Windows EXE / ZIP (x64)
```

Artifacts are written to `web/dist-electron/`. Pushing a `v*` tag triggers GitHub Actions to build macOS, Windows, and Linux packages and create a draft release for manual review and publication.

</details>

---

[Update distribution](./docs/updates.md) · [Architecture and design](./ARCHITECTURE.md) · [Technical research](./docs/research) · [Release notes](https://github.com/Lixiuxiu559/portunus/releases) · [Issues and suggestions](https://github.com/Lixiuxiu559/portunus/issues)
