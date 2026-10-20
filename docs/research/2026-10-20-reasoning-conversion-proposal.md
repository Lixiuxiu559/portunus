# 思考参数转换方案与本地验证

更新：2026-10-20。以本次用户确认的简化边界为准，替代此前需要模型能力配置的方案。

## 已批准边界

- 同协议保留原生参数和未知扩展，仅执行既有模型替换及显式兼容行为。
- 跨协议转换对应字段，保留原始强度，不根据模型名、URL 或供应商猜合法档位。
- 不新增渠道/模型思考能力配置，不让用户重复设置客户端思考强度。
- 无法直接表达的显式设置返回明确错误，不用固定预算或最高档位猜测兜底。
- 上游最终是否接受并执行参数仍由上游负责；本地测试验证实际发送的请求。

## 转换规则

| 意图 | Chat | Responses | Anthropic | Gemini |
|---|---|---|---|---|
| 强度 | reasoning_effort | reasoning.effort | output_config.effort | generationConfig.thinkingConfig.thinkingLevel |
| 显式关闭 | reasoning_effort=none | reasoning.effort=none | thinking.type=disabled | thinkingBudget=0 |
| token预算 | 无标准对应字段 | 无标准对应字段 | thinking.budget_tokens | thinkingBudget |

Gemini thinkingLevel 使用协议枚举大小写转换，档位本身不改写，不将 max 改成 high/xhigh。未指定设置不自动开启。adaptive 搭配明确 effort 时由目标强度字段表达控制；只有 adaptive、没有 effort 时，不猜目标档位。仅在 Anthropic/Gemini 有直接预算字段时转预算；Gemini 动态预算不能转换成 Anthropic 固定预算。目标无法同时表达关闭与非 none 强度或档位与预算时明确报错。

旧 -thinking 后缀保留为显式选择 Chat thinking 兼容扩展的入口，不通过模型名字猜能力；客户端显式关闭/原生 thinking 优先，不能被后缀覆盖。该兼容扩展原有预算行为保留，不自动扩散到没有预算字段的协议。

## 实现结构

protocol/reasoning.go 统一解析跨协议意图并渲染目标字段；ChatCompletionRequest 仅持有不序列化的内部元数据；ConvertRequest 与 ComposeUpstreamRequest 共用解析/渲染入口。同协议继续走 map 覆盖，不经有损结构体往返。

ReasoningConversionError 区分不可表达/类型非法的思考设置，网关返回 400 invalid_request_error 与具体字段原因；不计入上游故障熔断。现有分组仍可顺序尝试其他目标，但不重试同一目标。此次不改变中途断流后禁止重放的行为。

不改变历史 reasoning_content、签名桥或 ThinkingCompat；它们与本次生成的强度控制独立。

## 对照项目与取舍

核对本地版本 new-api e36d191c、cc-switch 5ca9459d，不能视作最新主分支。

new-api 将规则放在各渠道适配器，包含模型后缀、预算比例及特定模型分支；普通 Claude→Chat 路径在该版本仍未转换 output_config.effort。cc-switch 的 Claude 路径将 max→xhigh、adaptive→xhigh，Codex→Chat 路径还支持字段名称、档位集合和供应商映射配置。

本方案只借鉴共用转换入口与原生字段编码，不引入模型名单、预算阈值推导、能力表或重复用户配置。

参考源码：

- https://github.com/QuantumNous/new-api/blob/e36d191c2e31d31d0a7b8da51da1dcb93f63f681/service/convert.go
- https://github.com/QuantumNous/new-api/blob/e36d191c2e31d31d0a7b8da51da1dcb93f63f681/relay/channel/claude/relay-claude.go
- https://github.com/farion1231/cc-switch/blob/5ca9459d50ea4beea6a81bbc509de6ec5b6b09ca/src-tauri/src/proxy/providers/transform.rs
- https://github.com/farion1231/cc-switch/blob/5ca9459d50ea4beea6a81bbc509de6ec5b6b09ca/src-tauri/src/proxy/providers/transform_codex_chat.rs

## 验证设计与结果

使用项目现有 Go testing 和 HTTP mock，不增加测试框架。

- 四协议跨协议矩阵；ConvertRequest 与 ComposeUpstreamRequest 两个入口都检查目标参数及档位。
- 同协议原生字段与未知扩展保留、未指定设置不注入、显式关闭及后缀优先级。
- 预算直接转换、动态预算、无对应字段、冲突和非法类型的明确错误。
- 网关 HTTP mock 捕获 Claude/Codex 跨协议及同协议的实际请求体，模型名均为未知品牌别名。
- 原实现的红测试已确认：跨协议 effort 丢失、后缀覆盖显式 disabled；同协议扩展原本保留。

本地执行结果：

- 使用独立 GOCACHE=/tmp/portunus-reasoning-cache，避免旧缓存将存在的 httptest 标准库误判为缺失。
- GOTOOLCHAIN=local go test ./... -count=1：全部通过。HTTP mock 测试使用本机临时回环端口，在允许监听的执行环境运行。
- GOTOOLCHAIN=local go vet ./...：通过。
- GOTOOLCHAIN=local go build ./...：退出码 0；沙箱对模块下载统计缓存写入给出警告，但构建成功。
- gofmt 检查与 git diff --check：通过。
- 独立三轴 diff-review：Correctness、Standards、Spec 均无 finding。评审子 agent 的 protocol 测试通过；其回环端口受限，gateway 结果采用主 agent 的全量实测。
- 未执行专项复杂度与突变审计：仓库未配置相应工具，使用完整测试、静态检查与独立评审，不临时安装工具。

这些结果验证本地 mock 收到的参数与正常响应；未调用收费上游，未验证任意中转站是否继续保留参数或模型内部实际使用的计算量。

## 真实上游验证（2026-10-20）

用户明确授权后，使用当前源码的完整 gateway.Register 链路测试。只读读取桌面应用当前 deepseek-v4-flash 分组的首选渠道，复制所需记录至隔离内存数据库；不修改运行服务、持久配置或生产调用日志。关闭重试且只放一个目标，避免备用渠道掩盖参数拒绝。使用与 Claude Code/Codex 对应的 API 请求体，并未启动两款客户端 CLI。

目标：乐聚-newapi，OpenAI Chat，https://aiapi.lejurobot.com/v1/chat/completions，模型 deepseek/deepseek-v4.1-flash。两次均 stream=true，问题为“What is 1+1? Reply only with the answer.”，输出上限 2048。凭据仅在内存读取和发送，没有打印或写入探针文件。

| 客户端请求形状 | 实际上游参数 | 上游/网关状态 | 答案 | 总耗时 | 输入/输出 token | 网关请求 ID |
|---|---|---|---|---|---|---|
| /v1/messages，thinking.type=adaptive，output_config.effort=max | reasoning_effort=max；max_tokens=2048；未注入 thinking | 200 / 200 | 2 | 1008 ms | 43 / 14 | adf23a368d1f1655 |
| /v1/responses，reasoning.effort=high | reasoning_effort=high；max_completion_tokens=2048；未注入 thinking | 200 / 200 | 2 | 1110 ms | 43 / 22 | 93804ea4e6395f86 |

每次仅一次真实上游调用。Claude 入口收到 21 个 SSE 事件并正常 message_stop；Codex 入口收到 23 个 SSE 事件并正常 response.completed；两次网关日志记录均 success=true、无 err_kind。首包分别 883 ms、941 ms。

默认沙箱首次尝试在 DNS 阶段失败，未到达上游；获得网络执行权限后上述两次调用成功。DNS 失败不作为参数被拒绝的证据。

结论：当前首选乐聚渠道接受这两个转换后的参数，并能完成流式调用。没有测试 saver 备用模型或个人 DeepSeek 官方备用渠道；也不能仅凭 200 和返回 token 数证明中转站继续透传了参数，或实际模型采用了相应计算强度。测试探针已删除，脱敏输入和结果保留于本节。
