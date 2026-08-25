# Portunus 架构

LLM API 聚合服务：多渠道接入、按渠道模型定价、模型分组对外提供、全协议互转、调用日志。

## 技术栈

- **语言**：Go 1.26
- **Web 框架**：Gin
- **ORM**：GORM + SQLite（起步，保留换 MySQL / PostgreSQL 的能力）
- **前端**：暂缓，管理 API 设计成前端将来可直接接入

## 目录结构（backend/）

| 模块 | 职责 |
|---|---|
| `channel` | 渠道：上游连接配置（type / base_url / key），自动从 `/models` 拉模型 |
| `model` | 模型：管理渠道中的模型，**每个模型带价格**（内置价格表填默认 + 手动覆盖） |
| `group` | 分组：跨渠道聚合模型，**每个分组必配一个路由策略** |
| `router` | 路由解析：按分组策略决定调用哪个/哪些上游，返回「模型 + 渠道」 |
| `protocol` | 协议转换：OpenAI Chat / Responses ↔ Anthropic ↔ Gemini |
| `api` | 管理 API：供前端调用（渠道 / 模型 / 分组的 CRUD） |
| `gateway` | 对外 `/v1` LLM 接口：供 claude code / codex 等客户端调用，按分组策略路由 |
| `cron` | 定时任务：定时同步模型、日志落库 |
| `shared` | 跨模块公共：配置、数据库初始化、APIKey / 日志实体 |

依赖方向：`api` / `gateway` / `cron` → `router` / `channel` / `model` / `group` / `protocol` → `shared`。模块之间用外键 ID 关联，避免包级循环依赖。

## 核心概念

- **渠道 Channel**：连接一个上游供应商的配置单元。协议类型 ∈ `openai` / `openai_responses` / `anthropic` / `gemini`。`渠道 1:N 模型`。
- **模型 Model**：从渠道自动拉回、归属某渠道的模型，是本项目的「原料库」。价格是模型的属性（`输入价 / 输出价`），拉回时用内置价格表填默认值，可手动覆盖。
- **分组 Group**：自定义分组名即对外暴露的模型名。从模型库跨渠道挑模型入组（可混协议），按策略路由。
- **路由策略 Strategy**：每个分组必配其一
  - `manual`：手动指定激活项，只打它
  - `round_robin`：每次请求在组内模型间轮流
  - `failover`：按 priority 顺序，失败自动换下一个
- **鉴权**：简单 API Key，调 `/v1` 必带，用于日志 / 费用归属（无多用户）。

## 一次调用数据流

```
客户端(任意协议) → /v1 → APIKey 鉴权
  → 命中分组 → 按策略选出一个模型项（渠道 + 模型）
  → 判断客户端协议 vs 上游协议 → 不一致则转换
  → 上游调用 → 转回客户端协议 → 返回
  → 异步写日志 / 费用
```

## 关键决策

1. **从零重写**，仅参考 octopus 的功能与交互；许可证自选（octopus 为 AGPL-3.0，不 fork）。
2. **价格挂在 (渠道, 模型)** 上，而非 octopus 的「全局按模型名定价」。
3. **对外全协议互转**，上游渠道支持 openai / openai_responses / anthropic / gemini 四种。
4. **分组可混协议**，relay 时按需动态转换。
5. **模型自动拉取**：启动同步 + 定时同步 + 手动触发；同步为全量覆盖，价格独立留存不丢。
6. **框架沿用 Gin + GORM**（从零是摆脱 octopus 的业务负担，不必连成熟框架一起弃用）。
