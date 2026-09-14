# reasoning_content 回传机制对照研究：cc-switch vs new-API vs portunus

> 研究时间：2026-09-14。对象：① `/Users/lixiuxiu/development_tool/projects/cc-switch/`（Claude Code / Codex 本地代理转换层，Rust + serde_json）；② `/Users/lixiuxiu/development_tool/projects/new-API/`（Go LLM 聚合网关）；③ portunus 当前实现。
> 起因：线上持续报上游 400 "The `reasoning_content` in the thinking mode must be passed back to the API"（2026-09-14 当天 64 失败 / 100 成功，全部集中在乐聚-newapi 渠道的 `deepseek/deepseek-v4-flash`）。此前 v0.3.1 的渠道级垫片（`ThinkingCompat`，7e24b64）已就位但从未启用；本次调研找同类项目的成熟解法，定位剩余根因。
> 结论先行：**cc-switch 的四层体系完整命中我们的全部场景，new-API 字段面齐全但请求方向丢字段、无任何兜底，参考价值有限**。portunus 存在两个真实 bug：`redacted_thinking` 块转出空串 reasoning_content（密文在 `data` 字段而非 `thinking`）、Responses input 的 reasoning item 落 default 分支生成 `role:""` 畸形空消息。

---

## 1. 背景机制（为何要回传）

DeepSeek V4 / Kimi K2 等严格 thinking 上游要求多轮对话把 assistant 上一轮的 `reasoning_content` 原样回传，缺失即 400。客户端侧的「思考内容来源」有三种形态，各有丢失风险：

| 客户端 | 思考内容形态 | 丢失场景 |
|---|---|---|
| Claude Code（Anthropic 协议） | `thinking` 块（明文 + signature）或 **`redacted_thinking` 块（密文在 `data` 字段，无 `thinking` 字段）** | 辅助请求（标题/摘要）不带；上下文压缩后的历史不带；带的是 redacted 块时没有明文可取 |
| Codex（Responses 协议） | input 数组里的 `reasoning` item（`summary[]` / `content[]` / `encrypted_content`） | 本机实测（`~/.codex/sessions/`）：直连官方时 `summary:[]`、`content:null`、只有 `encrypted_content` 密文 |
| OpenAI Chat 客户端 | assistant 消息的 `reasoning_content` 扩展字段 | 客户端自行管理，可能不带 |

「间歇性 400」的成因：乐聚-newapi 这类中转对同名模型按参数路由多个上游，严格上游间歇命中——同请求在严格渠道 400、宽容渠道 200（2026-09-10 线上排查，request dd74c857）。

---

## 2. cc-switch：四层体系（几乎全是现成答案）

cc-switch 是 Claude Code / Codex 的本地代理转换层，与 portunus 网关角色同构。核心文件：`src-tauri/src/proxy/providers/transform_codex_chat.rs`（4867 行）、`transform.rs`、`codex_chat_common.rs`、`reasoning_bridge.rs`、`thinking_rectifier.rs`。

### 2.1 第一层：占位注入（粒度比 portunus 垫片细）

`transform_codex_chat.rs:1049-1062` `ensure_tool_call_reasoning_content`：

```rust
/// kimi/Moonshot、DeepSeek 等 thinking 模型要求每条带 `tool_calls` 的 assistant
/// 消息都必须携带非空 `reasoning_content`。跨轮历史恢复 miss（如代理重启丢失内存缓存、
/// call_id 歧义无法恢复、上游某轮未产出思考）时，这里补一个占位，避免上游返回
/// `reasoning_content is missing in assistant tool call message`。
```

要点：
- **只对带 `tool_calls` 的 assistant 消息注入**，占位字符串是 `"tool call"`；纯文本 assistant 不添字段——比 portunus 垫片（对所有有内容的 assistant 注入）克制
- **在转换管线末端注入**（`backfill_tool_call_reasoning_placeholders`，:1030）：先等真实 reasoning 经 pending/attach 回填，过早注入会被 `append_reasoning_content` 追加而污染真实思考

### 2.2 第二层：redacted_thinking 占位（直击 portunus 的空串 bug）

`transform.rs:458-463`（`convert_message_to_openai` 的 redacted 分支），注释原话：

> Claude Code encrypts historical thinking into redacted_thinking blocks. MiMo/DeepSeek require non-empty reasoning_content on assistant tool-call messages, so inject a minimal placeholder when the real content is unavailable.

做法：`redacted_thinking` 且 preserve 开关开启 → `reasoning_parts.push("[redacted thinking]")`，与明文 `thinking` 块提取的内容一起 join 进 `reasoning_content`。

### 2.3 第三层：Responses input 的 reasoning item 归属算法（portunus 完全缺失的那块）

`transform_codex_chat.rs:599-790` `append_responses_input_as_chat_messages`：

- **`pending_reasoning` 缓冲**：reasoning item 的文本提取后挂起，**前向附挂到其后的 function_call / message**（`flush_pending_tool_calls` / assistant message 分支消费）
- **绝不跨 user 回合泄漏**：user 等回合边界消息到达时，pending 回溯附挂到上一条 assistant（`attach_pending_reasoning_to_previous_assistant`）——注释记录了一个历史 bug：早期实现在 pending_tool_calls 为空时直接回溯附挂，「把新一轮的思考错拼进旧消息，导致紧跟的纯文本 assistant 丢失 reasoning_content，思考型模型（kimi 等）多轮对话因此中途断片」
- **尾部剩余**：整个 input 处理完毕后仍有 pending → 回溯附挂最后一条 assistant；目标已有 `reasoning_content` 时以 `\n\n` 追加（`append_reasoning_content`），保留同 turn 的 embedded + trailing 思考
- **文本提取穷举**（`codex_chat_common.rs:8-110`）：消息级字段优先级 `reasoning_content > reasoning(字符串/对象) > reasoning_details`；reasoning item 级取 `summary[]` 数组各 part 的 `text`/`content`，兼容字符串形态

### 2.4 第四层：reasoning 桥接（encrypted_content 无损旅行，最重武器）

`reasoning_bridge.rs`（约 110 行，带环测）：Anthropic Messages 协议没有字段承载 Responses 的 reasoning item，为了 **stateless tool loop 无损**，把完整 item base64 封装成带版本前缀的 opaque 载荷塞进协议自带字段：

- `encrypted_content` 有 + summary 有 → `thinking` 块，**密文封装进 `signature`**（前缀 `ccswitch-openai-reasoning-v1:`）
- `encrypted_content` 有 + summary 空 → `redacted_thinking` 块，密文封装进 `data`
- 客户端下一轮回传时从 signature/data 解码还原完整 reasoning item（`openai_reasoning_item_from_anthropic_block`）

适用场景：Codex 客户端 + Anthropic 上游（或反向）。若上游是 openai chat 兼容（本问题主场景），没有 opaque 字段可借，占位是唯一选项。

### 2.5 附：thinking 整流器（错误驱动的自愈）

`thinking_rectifier.rs`：上游报签名类错误时（"Invalid 'signature' in 'thinking' block"、"must start with a thinking block" 等，按错误文案模式匹配）自动剥掉有问题的 signature/thinking 块重试。属于响应式自愈，与本问题的请求侧回传互补。

---

## 3. new-API：字段齐全但有同款洞，无兜底

核心文件：`dto/openai_request.go`、`dto/openai_response.go`、`relay/channel/claude/relay-claude.go`。

**做对的部分**（响应方向）：
- dto 双字段兼容：请求 `ReasoningContent` + `Reasoning`（DeepSeek / OpenRouter 两种形状，`dto/openai_request.go:282-283`）；流式 delta 同样双字段 + `GetReasoningContent()` 取值器
- claude 上游的 thinking/thinking_delta → `reasoning_content`（relay-claude.go:494-496, 570-575）；gemini thought、ollama、dify、coze 各上游均有映射
- `ThinkingToContent` 渠道开关：把思考内容以 `<think>` 标签并入正文（面向不支持 reasoning_content 的下游展示，与回传无关）

**同款洞**（请求方向）：
- `RequestOpenAI2ClaudeMessage`（relay-claude.go:47 起）构造 `fmtMessage` 时只拷 `Role/Content/ToolCalls/ToolCallId`，**`Message.ReasoningContent` 被丢弃**——openai 客户端 → claude 上游方向不回传思考内容
- `dto/openai_request.go` 的 `Message` 有字段但 claude 转换不读

**没有的机制**：全仓库无占位注入、无 400 整流、无 reasoning item 处理（Responses input 里直接无此类型）。它对这个 400 的解法是「没有解法」。

---

## 4. portunus 现状与差距

| 场景 | cc-switch 做法 | portunus 现状（2026-09-14） | 结论 |
|---|---|---|---|
| Claude Code 历史缺 thinking 块 | 仅 tool-call assistant 补占位 `"tool call"`，管线末端注入 | 渠道级 `ThinkingCompat` 垫片（`InjectReasoningPlaceholders`）对所有有内容 assistant 注入「（历史思考内容未保留）」——**但开关从未启用** | 功能可用，需打开开关；粒度粗但宽容上游无害，维持现状 |
| **`redacted_thinking` 块** | 占位 `"[redacted thinking]"` | **bug**：`anthropicMessagesToChat`（anthropic.go:277-282）对 `thinking`/`redacted_thinking` 同分支拼 `block.Thinking`——redacted 块无 `thinking` 字段（密文在 `data`），拼出**空串**，等于没处理 | **修复**：redacted 块注入占位 |
| Codex 回传 reasoning item | pending 缓冲 + 前向附挂 + 尾部回溯 + 边界不跨 user | **bug**：`InputItem`（responses.go:34-42）缺 `summary`/`content` 字段，`inputItemToChat` 不识别 `reasoning` 类型 → 掉 default 生成 `{"role":"","content":""}` 畸形空消息 | **修复**：加字段 + pending/attach 算法（简化版） |
| encrypted_content 无明文 | reasoning 桥接（signature/data 封装） | 未处理 | **暂不做**：本问题主场景上游是 openai chat 兼容，占位即够；等 Codex + Anthropic 上游组合真实出现再评估 |

### 4.1 修复方案

1. **redacted_thinking**（anthropic.go）：`case "thinking", "redacted_thinking"` 拆开——thinking 块拼 `block.Thinking`；redacted 块（无明文）追加占位常量 `ReasoningPlaceholder`（与垫片同款文案，复用 `thinking_compat.go` 的定义）。
2. **Responses reasoning item**（responses.go）：`InputItem` 加 `Summary []ResponsesContent`、`Content` 已有（`ResponsesContent` 数组，注意 input item 与 OutputItem 的 content 形状差异）；`inputItemToChat` 增加 `case "reasoning"`——提取 summary/content 文本进 pending 缓冲，前向附挂到紧随的 assistant（function_call 或 message），尾部剩余回溯附挂，无任何可附挂对象时丢弃（不生成空消息）。
3. **运维动作**：打开乐聚-newapi 渠道 `thinking_compat` 开关。

### 4.2 与垫片的关系

垫片（`InjectReasoningPlaceholders`）在 `ComposeUpstreamRequest` 末端对**最终上游请求体**兜底，上述修复在**协议转换层**消解大部分缺失场景。两者叠加而非互斥：转换层修复让「真实思考内容」能回传（保真），垫片兜底「无从回传」的场景（保命）。打开垫片后，转换层修复的价值在于：真实内容优先于占位——垫片只对缺 `reasoning_content` 的消息注入，已有内容（含修复后回传的真实思考）不会被覆盖（`TestInjectReasoningExistingKept` 锁定）。
