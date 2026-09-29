# 服务端工具穿行设计：hosted web_search 的协议矩阵适配

## 研究范围

- 主题：Claude Code 的 WebSearch（Anthropic Messages API 的服务端工具）在 portunus 协议矩阵中的无损穿行
- 结论形态：设计决策记录（含两个参考项目的对照考证），实施完成于 2026-09-29，本文同时记录实施过程中发现的关键时序事实
- 参考项目考察：new-api（`QuantumNous/new-api` 的 `relaykit/relayconvert/internal/toolconv/`）、cc-switch（`farion1231/cc-switch` 的 `src-tauri/src/proxy/`，v3.20.0 PR #5681）
- git 基线：main @ 782dbd8 之上的工作区改动

## 问题

Claude Code 向 portunus 发的请求里，WebSearch 是**服务端工具声明**：`{"type":"web_search_20250305","name":"web_search"}`——`type` 是身份，**没有 `input_schema`**（有 allowed_domains / max_uses 等可选字段）。改造前的处理是错误的适配：

1. `AnthropicTool` DTO 无 `type` 字段——声明在 json 解析时被静默压扁（第一现场）
2. tools 循环一律硬编码转 function tool——hosted 声明变"名叫 web_search、无描述、空 schema"的空壳函数发往上游
3. 历史回传的 `server_tool_use` / `web_search_tool_result` 块在 `anthropicMessagesToChat` 的 switch 中无 case，静默丢弃——多轮对话上下文断裂

## 参考项目的行为基准（重要：两者都不代跑搜索）

**new-api**：工具 IR（`Definition{Kind, Execution: Client|Server}`）+ 四协议双向编解码；出口渲染"最接近等价物"（→Chat `web_search_options`、→Responses hosted `web_search`、→Gemini `googleSearch`）；语义丢失产出结构化诊断 + tool-loss-policy 可拒；历史 hosted 块跨协议重放。

**cc-switch**：Anthropic→Responses 单向桥（v3.20.0）；处处 fail-closed（不支持的新版本报错）；`max_uses` 在上游拒收限制字段时由桥本地强制（指令注入 + 掐流 + 合成 `max_uses_exceeded`）；响应侧把 `web_search_call` 合成回 `server_tool_use` + `web_search_tool_result`（含块排序状态机 `WebSearchResultOrderingState`）；**对拒收 hosted 工具的国产网关按供应商自动禁用**。

**关键判断**：两个参考项目都假设"上游实现了对应的 hosted tool"。对 DeepSeek/GLM 上游（openai chat 协议），new-api 的路线无效（上游不认识 `web_search_options`），cc-switch 的路线只在 OpenAI 官方端点有效、遇国产上游选择禁用。本项目采同一立场：**不代跑搜索、不发明新功能，做诚实的协议穿行**——DeepSeek/GLM 链路剥离 + 遥测（不再发空壳函数）；Gemini / Anthropic 兼容 / 官方 Responses 上游链路 WebSearch 真正可用。

## 设计决策记录

### D1 canonical 统一语义名（而非原字面直通）

各协议 hosted 字面不同（`web_search_20250305` / `googleSearch` / `web_search` / `web_search_preview_2025_03_11`）。若 canonical 承载原字面，每个 renderer 都得认识全部协议的字面全集，矩阵发散。裁决：**canonical `Tool.Type == "web_search"` 是唯一语义名**（判别 `isHostedToolType`：非空且非 `"function"`，openai.go）；各协议出入口做语义↔字面映射，出站字面化统一到各协议基线（anthropic → `web_search_20250305`，gemini → `googleSearch`，responses → `web_search`）。版本间差异字段 MVP 不承载。对齐 new-api 的 `KindWebSearch` 抽象与基线版本思路（`convmeta` options 的 baseline `web_search_20250305`）。

### D2 openai chat 出站剥离 + 遥测（不渲染 web_search_options）

不采纳 new-api 渲染 `web_search_options` 的路线：本项目的 openai 上游全是 DeepSeek/GLM 等兼容端点，不认识该字段（最好被忽略、最坏 400——cc-switch 对国产网关的实证）。剥离是诚实行为，但**剥离必须可见**：`ComposeStats.HostedToolsStripped` → `shared.Log.hosted_tools_stripped` 落库，>0 即说明该请求的搜索能力在上游侧静默降级。gemini 入站识别到的未知 hosted 键不计入该计数（已知不对称，注释声明）。

### D3 历史块的对称转换（与 tool_use / tool_result 同构）

`server_tool_use` → assistant `tool_calls`（id/name/input 同序）；`web_search_tool_result` → tool 消息，content 的 `web_search_result` 块归一为每行 `title: url` 的紧凑文本（确定性）。上游模型由此看到完整的多轮搜索历史。`toolNameByID` 映射同步扩展。

### D4 Gemini grounding → Anthropic 响应合成

canonical 新增 `GroundingMetadata` 中转载体（`ChatMessage` / `ChunkDelta` 的 `grounding_metadata`，openai.go——流式链两段之间传递，不出网）。Gemini 的 `groundingMetadata`（candidate 级，取 `webSearchQueries` + `groundingChunks[].web`）填充之；anthropic 出站经 `webSearchToolBlocks`（单一形状源，非流式与流式共用）在 text 前合成 `server_tool_use` + `web_search_tool_result` 两块，id 取块序数的确定性函数（`srvtoolu_gateway_<seq>`），保证缓存前缀稳定。

### D5 流式晚到时序（实施中发现的真实行为，重点考证）

**Gemini 真实流的 `groundingMetadata` 通常随末 chunk（带 finishReason）到达，晚于文本开始。** 若合成块的补发只挂在"首个 text 块打开"时机，此时序下合成块静默丢失——单测若只喂"grounding 先到"的有利时序（首 chunk 同携文本与接地）则测不出。处理（对齐 cc-switch 的排序思路）：

- grounding 先到：text 块打开前落地合成块（`textBlock` 开块分支）
- grounding 晚到：**关闭当前 text 块 → 插入合成块 → 重开续写**，块序变为 `text → server_tool_use → web_search_tool_result → text`——与 Anthropic 官方"先说话、后搜索、再作答"的块顺序同构，客户端按块序拼接 content，信息完整
- `Finish` 收尾兜底：纯工具/无文本增量的流式响应也要补发（与非流式行为对齐）
- 回归锁定：`TestGeminiGroundingLateArrivalStream`（gemini_grounding_test.go）喂真实时序，断言块序与确定性

### D6 出界（明确不做，留待需求证实）

- 网关代跑搜索（参考项目均无此做法）
- openai chat 出站渲染 `web_search_options`（canonical 语义已就位，加渲染是局部小事）
- Responses `web_search_call` 的响应侧合成（P1：官方 Responses 上游场景出现再做）
- hosted 声明的 allowed_domains / blocked_domains / max_uses / response_inclusion 等 optional 字段承载
- server tool 计次计费

## 验证锚点

- `hosted_tool_matrix_test.go`：三入站探针 × 四出站目标全积登记（to=openai 为 drop 格带理由，其余 keep）——与 `capability_matrix_test.go`（reasoning 维度）同构的能力维度登记
- `gateway/relay_hosted_tool_test.go`：端到端三用例（gemini 渠道声明抵达 + grounding 回流合成；openai 渠道剥离 + 日志遥测；流式真实时序）
- `prefix_stability_test.go` 追加 hosted 历史块用例；各方向测试文件（`*_hosted_tool_test.go` / `gemini_grounding_test.go`）
