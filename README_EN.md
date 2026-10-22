<p align="center">
  <img src="web/src/renderer/public/logo.png" alt="Portunus" width="112" />
</p>

<h1 align="center">Portunus</h1>

<p align="center">
  <strong>Aggregate upstream APIs locally. Serve them through model groups.</strong>
</p>

<p align="center">
  A locally running API aggregation gateway.<br />
  Connect official APIs and relay services, organize models into groups, and give clients and applications a stable endpoint.
</p>

<p align="center">
  <a href="https://github.com/Lixiuxiu559/portunus/releases/latest"><strong>Download Portunus</strong></a> ·
  <a href="#get-started-in-three-steps">Get started</a> ·
  <a href="https://github.com/Lixiuxiu559/portunus/issues">Report an issue</a>
</p>

<p align="center">
  macOS · Windows · Linux<br />
  <strong>English</strong> | <a href="./README.md">简体中文</a>
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

> Screenshots show an earlier running version with a separate copy of local data. Channel names and service addresses have been anonymized. The current source retains top capsule navigation with later page-layout improvements; the screenshot illustrates model groups and routing configuration.

## Serve clients through model groups

### Connect once. Switch when you need to.

Choose a group in Service Access and copy the gateway address, Portunus token, and generic API example, or follow a guide for Claude Code, Codex, or another client. Portunus does not read or modify client configuration files; upstream channels and routing stay in the gateway.

### Multiple protocols. One entry point.

Connect OpenAI Chat Completions, OpenAI Responses, Anthropic Messages, and Gemini upstreams. Client generation endpoints support Chat Completions, Responses, and Messages, alongside a model list. Cross-protocol translation supports SSE streaming and tool calls within the capabilities of the target protocol and upstream. Gemini is currently an upstream protocol only.

### Give your daily models a backup plan

Combine models from different providers into a group named for its purpose, such as `coding` or `daily`. The group name becomes the model name your client uses; Portunus handles the upstream selection.

| Routing strategy | How it works | When to use it |
| --- | --- | --- |
| Manual | Uses the model you select | Keep a preferred model and switch on demand |
| Round-robin | Distributes requests across group members in turn | Spread calls across multiple providers |
| Failover | Tries models in priority order, moving on after failure | Add backup providers to a daily workflow |

Retries, timeout handling, and circuit breaking help reduce disruption from upstream failures. Drag to reorder priorities. Once a streaming response has started reaching the client, an interruption cannot be replayed or switched seamlessly. Renaming a group changes its client model name; changing members preserves that group endpoint.

### Inspect usage and diagnose calls

Call records capture the selected model, channel, group, protocol, latency, request ID, and upstream-reported input, output, cache-read, and cache-write tokens. Use them to understand where a request went, how many tokens it used, and why it failed.

Record counts refer to log rows; retries or target switches can produce several records for one client request. Portunus does not configure model prices or calculate call costs. Refer to your upstream provider's invoice for actual charges.

Filter by channel, group, or exact request ID; open a record to copy its request ID or error summary. The request association view groups persisted logs by request ID and shows ordinary retries, token-budget compatibility retries, and target switches. Historical logs without an ID stay separate. The latest persisted result is not proof of a final client outcome, and a continuous attempt sequence does not prove completeness. See the [call-record guide (Chinese)](docs/call-records.md).

### Check upstream balances and quotas

Add query sources from an upstream channel card. Built-in adapters cover DeepSeek, SiliconFlow China, OpenRouter, and NewAPI accounts; custom sources use GET/POST and JSON field mappings. Query credentials are configured separately from generation credentials, and accounts and currencies are displayed separately.

Cards show the last queried balance, its timestamp, and a refresh action. Successful values remain visible as they age; failed updates and invalid credentials have separate states. Automatic refresh is off by default. These values do not affect routing, circuit breakers, or call-cost calculation. See the [balance and quota guide (Chinese)](docs/channel-quota.md). This describes the current source tree; packaged availability depends on the Release.

### Keep everyday maintenance on your desktop

When adding or editing a channel, fetch and select models or enter model names manually. Saving imports only the selected models; it does not trigger a full sync. Scheduled and manual sync remain available. Sync adds newly discovered models while preserving existing models; it does not delete missing upstream models or run paid generation checks. Automatically clean up logs by retention period, switch themes, and update inside the app.

Service Access manages the existing single token and localhost/LAN access mode. In LAN mode, other devices can call `/v1` with the token. Management routes under `/api` accept only real loopback connections and do not trust forwarding headers. Do not expose `/api` through a local reverse proxy; local processes remain inside the management trust boundary.

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

In Upstream Channels, choose a provider preset and protocol, enter the endpoint and API key, then fetch and select models or enter their names manually before saving. See the [upstream-channel guide (Chinese)](docs/upstream-channels.md). On the Model Groups home page, select upstream models and choose a routing strategy. For automatic fallback, choose Failover and set the priority order.

### 3. Connect clients and applications

Open Service Access from a group. Use a generic API example or manually apply a client guide for Claude Code, Codex, or another application. Send requests using the group name as model, then inspect Call Records for the selected upstream, model, usage, and results.

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

The app stores configuration and call logs locally in SQLite. Requests are forwarded to the upstream services you configure. The desktop app listens on localhost by default, with optional LAN service access in Service Access. Management routes remain restricted to loopback connections.

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

Desktop development uses an isolated **Portunus Dev** instance; do not also start `go run .` for this path:

```bash
cd web
pnpm install
pnpm run dev        # Builds and manages the Dev backend; Vite: http://127.0.0.1:5173
# Or build and launch a separate desktop preview:
pnpm run build:dev
pnpm run preview:dev
```

The Dev backend uses port `13061`; the installed production app keeps `13060`. Configuration, databases, caches, and updates are isolated. Dev starts with fresh configuration and does not copy production credentials. Running `dev:web` alone requires an already running Dev backend; its `/api` and `/v1` proxy targets `127.0.0.1:13061`, independently of the standalone backend's default port 3060. The management UI and proxy remain local-only. See the [development guide](docs/product/DEVELOPMENT.md).

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

Branch pushes, pull requests, and manual CI runs execute Go tests, vet, build, frontend tests, and the renderer build. Dependencies are installed with `pnpm install --frozen-lockfile`; no upstream credentials or paid generation calls are required.

Artifacts are written to `web/dist-electron/`. Tag and manual release workflows reuse these checks before packaging. Pushing a `v*` tag builds macOS, Windows, and Linux packages and creates a draft release for manual review and publication; a manual release run only builds artifacts. Local validation has passed, but the first remote CI run and required status checks remain unverified/unconfigured. See the [CI and development guide](docs/product/DEVELOPMENT.md#持续集成与发布检查).

</details>

---

[Product definition and roadmap](./docs/product/PRODUCT.md) · [Update distribution](./docs/updates.md) · [Architecture and design](./ARCHITECTURE.md) · [Technical research](./docs/research) · [Release notes](https://github.com/Lixiuxiu559/portunus/releases) · [Issues and suggestions](https://github.com/Lixiuxiu559/portunus/issues)
