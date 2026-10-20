# cc-switch、new-API 与 Portunus 的思考强度实现对比

## 问题与范围

调查三份本地源码如何设置模型思考强度，区分客户端可选档位、请求时的强度参数、是否开启思考、以及响应思考内容回传。只读调查，不改业务代码。版本：cc-switch `5ca9459d`、new-API `e36d191c`、Portunus `99f75e3`（2026-09-30）。

## 事实与证据

### cc-switch

- Claude Code 的设置编辑器提供“最大强度思考”开关，开时写 `env.CLAUDE_CODE_EFFORT_LEVEL=max`，关时删除该键。`src/components/providers/forms/CommonConfigEditor.tsx:72-85,131-138,229-236`。
- Codex 模型目录表单允许逐模型选择 `none/minimal/low/medium/high/xhigh/max/ultra` 和默认档位；保存为 `reasoningLevels/defaultReasoningLevel`，后端生成 `supported_reasoning_levels/default_reasoning_level`，经 `model_catalog_json` 供 Codex 菜单使用。`src/components/providers/forms/CodexFormFields.tsx:206-253,1319-1331`；`src-tauri/src/codex_config.rs:1100-1181,1928-1953`。这些字段定义可选档位，不等同于一次请求的强度。
- Codex 代理转换会读取 Responses `reasoning.effort`，依目标渠道能力写 `reasoning_effort`、`reasoning.effort` 或 `thinking`、`enable_thinking`、`reasoning_split`，并对部分渠道钳制合法档位。`src-tauri/src/proxy/providers/transform_codex_chat.rs:355-520`。
- Gemini 配置按原始环境变量和配置 JSON 保存；本次未发现专门的思考强度表单或网关适配。`src/components/providers/forms/ProviderForm.tsx:1548-1557`；`src-tauri/src/services/provider/live.rs:1866-1933`。

### new-API

- OpenAI Chat 模型后缀解析后写 `reasoning_effort`，Responses 后缀写 `reasoning.effort`，同时剥离上游模型名后缀；后缀优先于原请求强度。`relay/channel/openai/adaptor.go:313-338,574-590`；`setting/reasoning/suffix.go:9-35`。
- Claude Opus 4.6/4.7 的强度后缀转 `thinking.type=adaptive` 与 `output_config.effort`；旧 `-thinking` 路径按预算开启，Chat `reasoning_effort` 的 low/medium/high 还可映射固定 `budget_tokens`。`relay/channel/claude/relay-claude.go:156-239`。
- Gemini 对 `-thinking-N`、`-thinking`、`-nothinking` 和强度后缀分别映射 `thinkingBudget` 或 `thinkingLevel`，按模型范围钳制预算。`relay/channel/gemini/relay-gemini.go:55-197`。全局设置控制适配开关与比例，渠道通用参数覆盖可修改最终请求；未见渠道专属强度下拉。`setting/model_setting/claude.go:17-39`；`setting/model_setting/gemini.go:7-45`；`relay/compatible_handler.go:158-175`。

### Portunus

- Codex 默认配置写 `model_reasoning_effort="high"`；外部模型目录把所有模型固定声明为 `high/none`，默认 `high`。UI 的映射行只编辑模型、显示名、上下文，生成器不保存每模型强度。`web/src/main/client-config.js:19-32`；`web/src/main/codex-catalog.js:11-15,47-63,71-94`；`web/src/renderer/pages/Clients.jsx:647-680`。
- 网关识别请求模型名 `-thinking` 后缀，去掉后查分组，构造 `thinking.type=enabled`，Anthropic 入站时可复用 `budget_tokens`。仅 OpenAI Chat 兼容上游消费该意图，其他协议不注入。`backend/gateway/relay.go:74-84,223-228`；`backend/protocol/openai.go:41-63`；`backend/protocol/convert.go:79-86,125-130,149-175`。
- 同协议请求只改模型名和必要的 `thinking`，未知字段保留；跨协议先解析到 Chat canonical。该 canonical 未定义 `reasoning_effort`；Responses 请求结构未定义 `reasoning`，转换时未转出强度；Anthropic 请求结构未定义 `output_config`；Gemini 的 `GenerationConfig` 未定义 `thinkingConfig`。因此客户端传入的强度在这些跨协议路径被丢弃。`backend/protocol/convert.go:110-146`；`backend/protocol/openai.go:8-26`；`backend/protocol/responses.go:11-22,100-113`；`backend/protocol/anthropic.go:13-26,207-221`；`backend/protocol/gemini.go:53-60`。
- `ThinkingCompat` 与 `reasoning_content` 桥接解决历史思考内容回传，和“本次生成使用什么强度”是不同功能。`backend/protocol/thinking_compat.go:22-47`；`backend/protocol/reasoning_bridge.go`。
- `/v1/models` 只返回分组原名；自动获取模型不会列出虚拟的 `-thinking` 名称。`backend/gateway/gateway.go:69-89`。

## 推断与建议

1. **最高优先级：保证客户端明确请求的强度不被静默丢弃。** 给跨协议 canonical 增加独立的强度意图，区分 `enabled`、档位、预算和未指定；按目标上游协议/模型能力映射。先覆盖 Codex Responses → OpenAI Chat 的 `reasoning.effort`，再扩到 Anthropic/Gemini。对无法映射的目标显式报告或按明文策略降级，避免界面显示高强度而请求落成默认值。
2. **第二优先级：目录按模型声明可选档位。** 模型映射行保存合法档位和默认档位，校验默认值属于候选集合；不要对全部模型硬编码 `none/high`。官方模型的支持值依模型而异，`none` 对部分模型会返回 400。
3. **第三优先级：重新定义 `-thinking` 的定位。** 保留旧开关兼容，但新强度优先使用客户端原生参数；同一请求既有原生强度又有后缀时制定覆盖优先级。为确实需要后缀的客户端提供可发现的模型别名或界面入口，并避免与真实分组名冲突。
4. 加入跨协议矩阵测试：同协议保留、Responses→Chat 档位映射、Anthropic/Gemini 原生字段、禁用/未知档位、混合路由不同上游能力，以及 `-thinking` 与原生强度共存。

## 置信度与未确认项

源码路径结论：高。cc-switch/new-API 的真实上游兼容与客户端版本行为未实测；new-API 本次检索未找到直接覆盖后缀强度映射的测试。各模型实际允许的档位随版本变化，需按渠道模型能力维护，不应以通用列表推定。

官方协议依据：[OpenAI reasoning guide](https://developers.openai.com/api/docs/guides/reasoning)、[Anthropic docs](https://docs.anthropic.com/en/docs/build-with-claude/prompt-engineering/prompt-templates-and-variables)、[Gemini API reference](https://ai.google.dev/api/generate-content)。
