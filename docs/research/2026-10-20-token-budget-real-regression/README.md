# 输出预算修复后的真实上游验证

2026-10-20，用户授权小规模真实调用。复用当前桌面配置的乐聚-newapi（渠道 1，`deepseek/deepseek-v4.1-flash`）和个人-deepseek（渠道 3，`deepseek-flash`），共 4 次真实请求，没有追加调用。

## 结果

| 上游 | 客户端协议 / 强度 | 客户端预算 | 实际发送 | HTTP | 客户端收到的输出 token | 客户端结束信号 | 耗时 |
|---|---|---|---|---|---|---|---|
| 乐聚 | Codex Responses / high | max_output_tokens=256 | max_tokens=256 | 200 | 256 | response.completed | 2.060s |
| 乐聚 | Claude Code Messages / max | max_tokens=256 | max_tokens=256 | 200 | 256 | max_tokens + message_stop | 2.046s |
| DeepSeek 官方 | Codex Responses / high | max_output_tokens=256 | max_tokens=256 | 200 | 256 | response.completed | 1.753s |
| DeepSeek 官方 | Claude Code Messages / max | max_tokens=256 | max_tokens=256 | 200 | 256 | max_tokens + message_stop | 1.870s |

四次请求都只调用上游一次，无字段拒绝或兼容切换，实际发送的 `reasoning_effort` 分别保持 high/max。总输出用量为 1024 token（不含输入计费）。结合修复前同一排程任务在 `max_completion_tokens=256` 下分别生成 5089/6404 token 的[单变量对照](../2026-10-20-token-limit-ab-comparison/README.md)，本轮证据支持：当前两条上游的 Codex 和 Claude Code 转换路径均已使 256 token 输出上限生效。单次样本不能推广到所有供应商、模型或预算档位。

## 路径和边界

- 使用当前工作区代码的真实 gateway 路由，从 `/v1/responses`、`/v1/messages` 入站，经鉴权、路由、协议转换和流式响应转换。不是启动实际 Codex / Claude Code 客户端；验证的是它们使用的协议载荷。
- 隔离测试库映射真实上游模型，本地转发器记录发送预算、强度与状态，再使用配置中的地址和密钥请求真实上游。使用合成排程任务，未读取或外发用户业务代码。
- 桌面配置数据库只读，密钥仅在内存用于对应渠道鉴权；未保存密钥、未修改现有运行服务或持久路由配置。
- 256 是上游输出预算，可能全部被思考 token 消耗，不保证还有正文答案；输出用量来自上游 usage 经转换后的客户端事件。

## 探针限制与另一个发现

原始临时探针的严格测试判定失败，不能报告为测试命令通过。原因是探针在转发完整流后才解析所缓冲的上游尾部：网关已读取终止事件并返回，报告序列化与转发器尾部解析存在时序竞争，原报告没有保存完整上游 usage/finish_reason/[DONE]，两条还记录了结束时的 read_error。不能据此认定上游发生异常断流，也不能声称本轮已独立验证原始 Chat `finish_reason=length`。

保留原始 `results.jsonl` 和原样 `probe.go.txt`（含上述限制）。离线复核发送预算、HTTP 状态、客户端 usage 和结束事件，得到 `summary.json`；没有为了纠正探针追加收费请求。四条均收到完整客户端结束事件且 usage.output_tokens=256；Claude 两条另有明确的 `stop_reason=max_tokens`。

Codex 路径另有现存结束语义问题：`backend/protocol/responses.go` 的 `responsesBlockSink.Finish` 忽略 finish reason，固定输出 `response.completed/status=completed`。因此输出已经被限制，客户端却不能从这个状态识别预算截断。该发现独立于预算字段修复，本轮仅记录，未修改生产代码。

## 文件

- `case.json`：实际使用的合成输入。
- `results.jsonl`：原始脱敏观测，保留严格探针的 false 结果。
- `summary.json`：根据已保存客户端证据离线复核的结论。
- `probe.go.txt`：原始临时 Go 测试，运行后已从 gateway 测试目录移除，避免常规测试触发收费调用。其上游尾部采集存在上述限制；任何复跑都会新增真实费用，必须先修正采集时序并另行明确授权。
