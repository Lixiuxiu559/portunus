# 最新 new-api / cc-switch 输出预算字段兼容调查

调查日期：2026-10-20。按用户要求先实际 git fetch，再基于远程最新 main 源码重新调查；本轮不修改 Portunus 业务代码、不调用收费上游。

## 版本与工作区状态

- new-api：origin/main = 32e587406c6ebe891f18ab9aeb2f4a1f6b7b9e14，2026-10-19，feat(email): 添加 resend 邮件服务商接口 (#7880)。相对原 e36d191c 新增 911 个提交。
- cc-switch：main/HEAD/origin/main = f92ff0b03964188f1cd011d9302ab8869d7a8219，2026-10-19，docs(release): add v4.0.16 release notes。相对原 5ca9459d 新增 780 个提交，已快进更新，工作区干净。
- new-api 原工作区存在用户修改的 docker-compose.yml，远程也改动同一文件。为避免覆盖或强制合并，保留原分支及修改，最新 origin/main 单独检出到 /private/tmp/portunus-new-api-latest-20261020；该最新检出干净。主目录并未宣称已更新至最新 HEAD。

## new-api 最新事实

协议转换已经拆到 relaykit。Responses→Chat 的 to_oai_chat_req.go:65-70 直接将 req.MaxOutputTokens 赋给 MaxCompletionTokens。并不是所有跨协议输入都先选择 max_tokens，上一轮基于旧代码的概括不足以覆盖这条新路径。

OpenAI 适配器 relay/channel/openai/adaptor.go:422-429 查询 GetOpenAIChatCapabilities。当 UseMaxCompletionTokens 为真、原请求未提供非零 MaxCompletionTokens、却提供了非零 MaxTokens 时，将 MaxTokens 移至 MaxCompletionTokens 并清空旧字段。

relaykit/dto/openai_request.go:320-397 集中实现模型能力规则，包括已知 o 系列、GPT-5 家族、特定 GPT-6 型号/快照。未知型号不自动继承限制。这比旧版粗前缀判断细致，但仍依赖目标模型标识，不是上游接口自动协商。

DeepSeek 适配器 relay/channel/deepseek/adaptor.go:105-113 处理思考后缀后直接返回请求，没有将 MaxCompletionTokens 反向改成 MaxTokens。由默认源码路径推断，最新版本本身也不能保证解决本次 Responses→DeepSeek 的字段兼容现象；本轮未运行 new-api 的真实上游请求来验证该推断，部署端参数覆盖等可改变行为。

固定证据：

- https://github.com/QuantumNous/new-api/blob/32e587406c6ebe891f18ab9aeb2f4a1f6b7b9e14/relaykit/relayconvert/internal/oai_responses/to_oai_chat_req.go#L65-L70
- https://github.com/QuantumNous/new-api/blob/32e587406c6ebe891f18ab9aeb2f4a1f6b7b9e14/relay/channel/openai/adaptor.go#L422-L429
- https://github.com/QuantumNous/new-api/blob/32e587406c6ebe891f18ab9aeb2f4a1f6b7b9e14/relaykit/dto/openai_request.go#L320-L397
- https://github.com/QuantumNous/new-api/blob/32e587406c6ebe891f18ab9aeb2f4a1f6b7b9e14/relay/channel/deepseek/adaptor.go#L105-L113

## cc-switch 最新事实

Codex 实际生产调用 forwarder.rs:1841-1849，进入 transform_codex_chat.rs:316-327：max_output_tokens 默认变成 max_tokens；is_openai_o_series 判定真时改用 max_completion_tokens。判定为模型名以小写 o 加数字开头（transform.rs:57-63），不包含旁边 reasoning-effort 检查中的 GPT-5 条件。随后分别复制客户端原有两字段，非标准输入可能出现双字段，这不是主动双发兼容策略。

Claude→Chat 实际生产调用 claude.rs:424-430，核心 transform.rs:278-285 同样按 o 系列选择字段。与旧版本读取/diff 交叉核对，预算选择实质未变。

Claude→Responses 使用 max_output_tokens；特殊 Codex OAuth 路径会删除预算字段。这是消费级反代接口特例，不适用于 Portunus 保留预算的目标。

代理还有 thinking budget rectifier，但它处理 Anthropic thinking.budget_tokens 的 1024 下界约束，并可能抬高 max_tokens；不是 max_tokens/max_completion_tokens 的错误后切换机制。

固定证据：

- https://github.com/farion1231/cc-switch/blob/f92ff0b03964188f1cd011d9302ab8869d7a8219/src-tauri/src/proxy/forwarder.rs#L1841-L1849
- https://github.com/farion1231/cc-switch/blob/f92ff0b03964188f1cd011d9302ab8869d7a8219/src-tauri/src/proxy/providers/transform_codex_chat.rs#L316-L327
- https://github.com/farion1231/cc-switch/blob/f92ff0b03964188f1cd011d9302ab8869d7a8219/src-tauri/src/proxy/providers/transform.rs#L57-L63
- https://github.com/farion1231/cc-switch/blob/f92ff0b03964188f1cd011d9302ab8869d7a8219/src-tauri/src/proxy/providers/transform.rs#L278-L285

## 判断与建议

高置信度事实：最新两项目均存在按模型标识选择预算字段的规则；cc-switch 默认 max_tokens；new-api Responses→Chat 固定写 MaxCompletionTokens，不能拿它作为默认旧字段的完整先例。

在本次检索的两字段引用、转换与代理路径中，未发现针对预算字段的通用“明确不支持错误→切换另一个字段”或运行时能力学习机制。该结论限于核对范围，不对所有插件、部署侧改写作不存在证明。

原建议“跨协议转 Chat 默认 max_tokens，遇明确字段不兼容错误再改 max_completion_tokens 一次”仍是 Portunus 自己的可选设计，而不是从最新两项目发现的成熟通用机制。它借鉴 cc-switch 的默认值，使用实际错误替代模型名启发，不要求用户配置；代价是旧字段不兼容时多一次往返，仍不能解决静默忽略 max_tokens 的未知上游。

本轮未实施该建议、未运行两个项目的构建或集成测试、未追加收费调用。源码路径结论高置信度，上游真实执行与部署覆盖行为未确认。报告新增于 Portunus 仓库，尚未被 Git 跟踪。
