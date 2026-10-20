# Responses 真实工具调用与双轮会话验证

日期：2026-10-20。被测提交：`98f8d8a866fcc48d9039caa1d50f84000dc2c063`。用户明确要求完成“真实工具调用 → 回传结果 → 继续回答”的 Codex 实机闭环。

## 结论

**两条真实上游、本机 Codex 的只读函数工具闭环与同会话双轮验证均通过。**

- 每条上游在首次模型输出中产生两个 `read_probe_value` 调用，分别读取 alpha、beta。
- 本机 Codex App Server 0.157.0 原生分发 `item/tool/call`；测试客户端实际读取临时文件，将随机值通过原生工具结果接口交回 Codex。
- Codex 自动发起后续生成请求，网关将两条 function_call_output 转成与原 call_id 配对的 tool 消息，上游接收后返回正确答案。
- 同一内存会话的第二个用户回合不再执行工具，仍准确返回先前两个工具值与首轮用户标记。
- SSE 输出项、索引、序号、用量与日志逐项复核通过，无 active item 或工具中断错误。

本轮未发现需要修改业务代码的问题，只新增验证材料及更新产品状态。

## 场景与实际结果

工具为 Codex 原生动态函数 `read_probe_value(key)`，key 只允许 alpha / beta。每个会话在临时文件中生成两个随机值；它们不出现在用户提示词中，模型只能通过工具读取。工具只读，不执行 shell、不联网、不操作业务文件。

| 上游 | 第一次生成 | 工具结果后的生成 | 第二个用户回合 | 结果 |
|---|---|---|---|---|
| 乐聚-newapi / deepseek/deepseek-v4.1-flash | 同一次输出产生两个工具调用，finish_reason=tool_calls | 两个真实随机值均正确，finish_reason=stop | 无新工具调用，随机值及初始 tag 均正确 | 通过 |
| 个人-deepseek / deepseek-flash | 同上 | 同上 | 同上 | 通过 |

共 6 次真实模型请求、4 次实际工具执行、4 个已完成用户回合。每条上游恰好 3 次模型请求，无额外重试。输出预算为每次 384 token；实际总输出 269 token，总输入 50746 token（包含缓存读 25856 token）。实际收费以供应商账单为准。

这里的“双工具同轮”指上游在同一次生成中返回两个调用；测试客户端按收到的工具请求逐条读取文件，不声称测量了两次磁盘读取的时间并发。

## 验证方法与独立复核

先运行零收费的本机合成 Responses 服务，确认测试驱动能完成工具执行和同会话双轮操作，随后才连接真实上游。

真实链路为：

```text
本机 Codex App Server
  → 临时 Portunus /v1/responses
  → 真实上游 Chat Completions
  → Responses function_call 输出项
  → Codex 原生动态工具请求
  → 实际读取临时文件并回传工具结果
  → Codex 自动追加 function_call_output 并再次请求
  → Portunus 转换为配对的 assistant/tool 历史
  → 真实上游继续回答
  → 同一 thread 再次 turn/start，验证上下文延续
```

`assess.py.txt` 独立读取原始结果和 SSE，实际执行并通过以下断言：

1. 上游两个调用的 call_id 与 Codex 实际执行请求完全对应，工具参数分别为 alpha / beta。
2. 后续两次请求的 assistant/tool 历史无缺失、无孤立结果，tool 消息保留工具名、原 call_id 和真实返回值；所有配对检查通过。
3. 每个用户回合均为 completed。首回合 JSON 与实际文件随机值一致；第二回合还必须保留最初 tag，且没有再次调用工具。
4. 每次 SSE sequence_number 连续，output_item.done 与终态 output 相同，function_call 的参数与上游实际增量一致。
5. 所有 HTTP 200 均同时具备有效上游结束原因、终态与 `[DONE]`，不以 HTTP 状态单独判通过。
6. 上游用量、Responses 用量与网关日志的输出和缓存读一致；日志通过 request_id 逐条对应。

复核命令（离线、不新增费用）：将 `assess.py.txt` 内容保存为临时 Python 文件，在仓库根执行即可。本次实际执行的复核结果见 `assessment.json`；其中保存被测提交与源码 SHA-256。

官方接口依据：[Codex App Server 动态工具及会话 API](https://learn.chatgpt.com/docs/app-server)。同时使用本机 `codex app-server generate-json-schema --experimental` 生成的 0.157.0 schema 核对参数和返回结构。

## 隔离边界与覆盖限制

- 两条渠道配置只读加载；用内存 SQLite 创建最小测试路由，密钥仅在内存用于对应上游鉴权。没有复制数据库、改 Dev 渠道、改正式应用或用户 Codex 配置。
- Codex 使用独立临时目录，thread/start 的 ephemeral=true；同一个 thread 连续执行两个 turn/start。后续回合由真实 Codex 维护历史，不由脚本拼装工具回传或替模型回答。
- 实机客户端是本机 `codex app-server`，不是桌面图形界面。工具是原生动态函数，不是内置 shell、MCP 或命名空间工具。
- 测试入口只保留这一个函数声明，并注入输出预算；**input 会话历史原样经过网关**。其他内置/插件工具声明不在本次覆盖范围内，没有将它们的兼容性计为通过。
- 基础指令限制只调用此工具；任何其他服务器请求均被测试客户端拒绝。本轮无意外工具或审批请求。
- 网关与客户端重试关闭；每条上游硬限制最多 4 次真实请求，实际各 3 次。工具定义、模型和 max_tokens=384 均有出站检查；结果使用 O_EXCL 创建，避免误重复收费运行。
- 两条上游均使用 effort=none。本轮不覆盖工具与思考同时开启、工具超时/失败、更多用户回合、会话恢复、其他协议或供应商。
- 临时文件、服务和子进程已随实验退出清理；原正式版 13060 / Dev 13061 的监听进程保持不变。CLI 中已知的测试模型 metadata、插件清单警告保留在 stderr 中，没有隐藏。

## 产物

- `assessment.json`：独立验收结论、用量及源码指纹。
- `results.jsonl` / `summary.json`：脱敏上游调用、工具历史和客户端观测。
- `probe1.*` / `probe3.*`：实际 App Server 事件、双轮结果及 stderr。
- `probe*-request-*.response.sse`：6 次真实网关输出，保留协议要求的末尾空行。
- `probe.go.txt`、`client_driver.py.txt`：本次真实探针和客户端驱动。
- `assess.py.txt`、`offline_mock.py.txt`、`offline-client-summary.json`：离线复核与驱动预演材料。

未保存原始模型请求、上游密钥或用户业务文件。记录中的随机值只用于本次合成任务，不是凭据。报告目录随验证文档提交纳入 Git；没有修改忽略规则。由于未修改业务代码，本轮没有重复运行全仓测试，上一轮已通过的测试记录见前序报告。
