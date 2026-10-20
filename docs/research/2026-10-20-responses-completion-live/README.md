# Responses 结束语义实机验证

日期：2026-10-20。用户在结束语义修复后明确要求“进行实机验证”。

后续：本文保留本轮原始失败事实。正文生命周期与缓存明细已在[下一轮修复及真实复验](../2026-10-20-responses-lifecycle-fix/README.md)中修复并通过所列场景。

## 结论

**结束语义修复已通过真实上游和 Codex CLI 验证，但完整 Codex 接入不能判为通过。**

- 两条上游各验证流式、非流式预算截断，4 次均保留 `length → incomplete / max_output_tokens`，输出用量与日志一致。
- 实际 Codex CLI 0.157.0 收到预算截断后明确报告 `Incomplete response returned, reason: max_output_tokens`，退出码 1。这是预期的截断识别结果，不是正常任务成功。
- 正常对照得到上游 `stop`、网关 `response.completed`、Codex `turn.completed` 和退出码 0，但没有正文 `agent_message`；stderr 报 `OutputTextDelta without active item`。因此“退出码 0”不能作为完整接入成功的依据。
- 另发现 Responses 返回的缓存明细丢失：上游缓存读 10880 token、网关日志正确保留，客户端 `cached_tokens` 却是 0。

本轮只验证并记录上述问题，未改动生产转换代码来修复新问题。

## 真实调用结果

| 场景 | 上游 | 输出预算 | 实际输出 token | 上游结束 | 网关终态 | Codex 结果 |
|---|---|---:|---:|---|---|---|
| 流式截断 | 乐聚-newapi / deepseek/deepseek-v4.1-flash | 128 | 128 | length | incomplete / max_output_tokens | 协议探针 |
| 非流式截断 | 同上 | 128 | 128 | length | incomplete / max_output_tokens | 协议探针 |
| 流式截断 | 个人-deepseek / deepseek-flash | 128 | 128 | length | incomplete / max_output_tokens | 协议探针 |
| 非流式截断 | 同上 | 128 | 128 | length | incomplete / max_output_tokens | 协议探针 |
| 正常完成 | 个人-deepseek / deepseek-flash | 256 | 1 | stop | completed | exit 0、turn.completed，但正文缺失 |
| 预算截断 | 同上 | 128 | 128 | length | incomplete / max_output_tokens | exit 1、turn.failed，明确识别原因 |

共 6 次真实上游请求，每个场景均只调用一次，未发生额外重试。总输出 641 token；总输入 22600 token（包含缓存命中 10880 token）。实际费用以供应商账单为准。

非流式结果表中的 incomplete 指 JSON 响应状态，不是 SSE 事件；原始探针统一将它表示为一条终态观测。

## 方法与边界

1. 使用本轮工作区源码编译临时程序，注册真实 gateway `/v1/responses` 路由，使用本机随机端口监听。
2. 只读正式桌面配置库，读取上述两条既有渠道的必要连接信息；在内存 SQLite 中创建最小测试渠道、模型和分组。密钥仅在进程内用于对应上游鉴权，没有复制数据库或写入 Dev 配置。
3. 保持已运行的正式版 13060、Dev 13061 和各自数据库不变；前后端口对应进程一致。本轮没有替换安装包，也不代表这两个旧实例已经加载修复。
4. 普通探针使用合成数数任务。Codex 在独立临时空目录运行，`--ignore-user-config --ignore-rules --ephemeral --sandbox read-only`；仅命令行配置本地 Responses provider，没有修改用户 Codex 配置。没有运行工具或读取业务代码。CLI 的自带说明与工具定义会计入输入 token。
5. 测试入口只注入已批准的 `max_output_tokens`，Codex 其余请求字段保留；实际上游均发送相同数值的 `max_tokens`，未发送 `max_completion_tokens`。Codex 预算由测试边界施加，并非声称 CLI 自身支持指定该字段。
6. 网关和 CLI 重试均关闭；出站 transport 硬限制每场景最多一次真实调用和指定模型/预算。结果文件使用 O_EXCL 创建，防止意外重复收费运行。
7. 上游 `finish_reason`、usage、`[DONE]` 在字节交给网关前解析，避免上一版探针“网关先结束，尾部观测尚未采集”的竞态。结果记录不包含密钥或真实请求正文。

## 新问题取证与离线对照

### 1. 正文输出项生命周期缺失（已验证，高置信度）

当前 `responsesBlockSink.TextDelta` 只发 `response.output_text.delta`，没有先创建 message 输出项；最终响应也没有正文 output 项。真实 CLI 报 `OutputTextDelta without active item`，正常回合没有 `agent_message`。

零上游调用的离线 A/B 对照使用相同 Codex 版本、相同模型名和文本 `OK`：

| 回放 | CLI 退出码 | agent_message | active item 错误 |
|---|---:|---|---|
| 当前源码生成的 SSE | 0 | 无 | 有 |
| 按官方结构补齐输出项及内容生命周期的合成 SSE | 0 | OK | 无 |

对照仍有相同的未知模型 metadata 警告，因此该警告不足以解释正文丢失。对照补齐了多个相邻生命周期字段，不主张已经证明其中某一个字段是唯一必要条件。

建议下一步补齐 message 的 output_item/content_part 生命周期、索引与 ID、文本 done 和终态 output；同时覆盖 reasoning、工具混排与截断场景，避免只补一个开场事件。

官方契约：[Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events)。

### 2. 缓存命中明细未传出（已验证，高置信度）

真实截断场景中，上游 `prompt_tokens_details.cached_tokens=10880`，日志 `cache_read_token=10880`，Responses `input_tokens_details.cached_tokens=0`。总输入、输出仍正确。

离线构造输入 100、缓存读 80、输出 1，canonical usage 正确保留非缓存输入 20 和缓存读 80，而最终 Responses 的 cached_tokens 为 0。`toResponsesUsage` 只填总输入、输出、总量，没有填缓存明细。

建议单独补齐缓存明细映射与回归测试，不改当前 token 统计口径。

## 产物与复核

- `results.jsonl`、`summary.json`：原始脱敏观测；其中 protocol_verified 只判断终态，**不是完整客户端兼容性结论**。
- `assessment.json`：独立复核终态、用量、缓存及 CLI 正文，并记录被测源码 SHA-256。
- `codex_normal.jsonl` / `codex_truncated.jsonl` 与各自 stderr：实际 CLI 输出。
- `offline_lifecycle_summary.json`、`offline_current.sse`、`offline_control.sse` 及 offline CLI 日志：零收费的生命周期对照。
- `cache-offline.json`：缓存明细离线复现。
- `probe.go.txt`、`offline_probe.go.txt`、`offline_replay.py.txt`：实际探针源码，使用 .txt 后缀，避免常规测试自动执行收费调用。

真实探针编译和执行成功不意味着所有验收项通过；以本报告和 assessment.json 为准。离线对照不是生产修复。本轮未验证内容过滤真实触发、真实工具执行、图形界面显示或其他供应商。

临时服务器随程序退出已停止。脱敏报告与复核材料随本轮修复一并入库；未修改忽略规则。
