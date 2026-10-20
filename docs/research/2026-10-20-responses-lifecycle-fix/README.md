# Responses 正文生命周期与缓存明细修复验收

日期：2026-10-20。承接[上一轮实机报告](../2026-10-20-responses-completion-live/README.md)。用户已授权修复，并明确要求真实上游和真机验证。

后续：[真实工具调用及同会话双轮验证](../2026-10-20-responses-tools-multiturn/README.md)已通过。本报告仍保留本轮自身的覆盖范围与限制。

## 结论

**本次修复及所列真实场景验证通过。** 本机 Codex CLI 0.157.0 经两条真实上游均实际显示 `OK`，不再出现 `OutputTextDelta without active item`；预算截断仍能被准确识别；真实缓存命中明细已传到 Responses 和 Codex。

这不是对所有供应商、所有工具或所有客户端的兼容性承诺。被测对象是当前源码启动的临时网关和本机实际 Codex CLI，没有替换正在运行的正式版或 Dev 应用。

## 修改内容与依据

- 为正文和思考建立 output_item / content_part 或 summary_part 的 added、delta、done 生命周期，保留必需的 ID、索引、递增 sequence_number、正文 annotations 空数组。
- 工具的 OpenAI index 独立映射到连续 Responses output_index，避免正文、思考与工具混排时索引冲突；空工具参数仍保留必需的 arguments 字段。
- 终态携带完整 output；output_item.done 等结束原因明确后发送，保持此前预算截断和内容过滤映射。
- `toResponsesUsage` 将缓存读写中的缓存读数填入 `input_tokens_details.cached_tokens`，保留总输入和缓存写统计口径。

协议依据：[OpenAI Responses streaming events](https://developers.openai.com/api/reference/resources/responses/streaming-events)。根因和修复范围在上一轮报告中已给出，本轮沿用用户批准继续实施。

## 本地与离线验证

新增生命周期测试修复前因缺少事件序号、输出项和空数组失败；新增缓存测试修复前因 cached_tokens 为 0 失败。修复后通过：

- 正文正常完成/预算截断：added → part added → delta → text done → part done → item done → 终态；内容、ID、索引和 annotations 一致。
- 思考、并行工具、正文混排：独立连续索引、参数累计、输出项与终态一致。
- 空流、仅 role 流、空工具参数、同帧和独立尾帧的截断、内容过滤、未知结束原因、usage 缺失。
- 流式与非流式的缓存读、缓存写、零缓存与总输入口径。

实际命令：

```bash
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test ./... -count=1
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go vet ./...
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go build -buildvcs=false ./...
git diff --check
```

均通过。Go mock 测试使用获准的本机临时端口，不调用收费服务。

实际 Codex CLI 的零收费离线回放：当前源码生成的 `OK` 正文可显示；`first → think → OK` 混排能显示两条 agent_message（first、OK），退出码 0、无 active item 错误。合成标准事件对照同样通过。离线回放在最后补齐 annotations 和空 arguments 字段前执行；最终代码随后由下面的真实调用、全量回归和独立评审验证。

## 真实上游和本机 Codex 验证

| 场景 | 渠道 / 模型 | 预算 | 实际输出 token | 验证结果 |
|---|---|---:|---:|---|
| Responses 流式截断 | 乐聚-newapi / deepseek/deepseek-v4.1-flash | 128 | 128 | length → incomplete / max_output_tokens，部分正文完整保留 |
| Responses 非流式截断 | 同上 | 128 | 128 | JSON 状态、部分正文和用量正确 |
| Responses 流式截断 | 个人-deepseek / deepseek-flash | 128 | 128 | 同上，SSE 序号与终态 output 一致 |
| Responses 非流式截断 | 同上 | 128 | 128 | JSON 状态、部分正文和用量正确 |
| 实际 Codex 正常完成 | 乐聚-newapi / deepseek/deepseek-v4.1-flash | 256 | 1 | 上游 stop，CLI 显示 OK，turn.completed、exit 0 |
| 实际 Codex 正常完成 | 个人-deepseek / deepseek-flash | 256 | 1 | 同上，真实缓存读 10880，CLI 明细一致 |
| 实际 Codex 思考开启后预算截断 | 个人-deepseek / deepseek-flash，effort=high | 128 | 128 | 保留 563 字符思考；CLI 明确识别 max_output_tokens，turn.failed、exit 1；无 active item 错误 |

7 个场景均独立复核通过。每场景只有 1 次真实上游调用，没有额外重试；总输出 642 token，总输入 33874 token（含缓存读 10880）。真实费用以供应商账单为准。

重点交叉核对：

- 正常场景不只要求 exit 0，必须存在 agent_message=OK，且与实际保存的上游正文相同。
- 截断场景要求上游 length、网关 incomplete/max_output_tokens，CLI 截断场景须出现明确原因；exit 1 是该场景的预期结果。
- 所有场景的终态正文/思考内容与实际收到的上游增量一致；流式 item.done 与终态 output 相同，sequence_number 连续。
- 上游、Responses、网关日志的输入/输出/缓存明细逐项一致；真实命中场景的 Codex `cached_input_tokens=10880`，不是用零缓存样本代替命中验证。
- 全部实际 CLI 场景均没有 active item 错误。

## 实验隔离与限制

只读正式配置库中的两条既有渠道，使用必要连接信息在内存 SQLite 中构建最小测试路由。密钥只在进程内用于对应上游鉴权；未复制数据库、未写 Dev 配置、未修改客户端配置。前后正式版 13060 / Dev 13061 的监听进程一致。

Codex 使用独立临时空目录、临时命令行 provider，开启 read-only 和 ephemeral，不读取业务代码、不执行工具。入口只注入本轮已批准输出预算，其余请求字段保留。网关和 CLI 重试关闭；出站 transport 硬限制每场景一次、指定模型和预算。实际上游发送 max_tokens，不发送 max_completion_tokens。结果文件使用 O_EXCL 防止误重复收费运行。

上游结束原因和用量在字节交给网关前采集；结果不受上一轮旧探针尾部采集竞态影响。采集结果包含合成任务产生的正文/思考，不保存请求正文或密钥。CLI 仍提示测试分组没有模型 metadata，正常正文和截断验证均在该条件下通过，不把此警告隐去。

本轮没有验证图形界面、真实工具执行、内容过滤真实触发、其他上游协议或供应商。缓存写无真实上游样本，由回归测试覆盖。临时服务器随探针结束已停止。

## 评审与产物

独立三轴评审：Correctness 曾发现终态 output_text 缺 annotations，已修复并复核关闭；最终新增问题 0。Standards、Spec 均 0 项。未配置可用突变工具，本轮没有安装；未运行独立复杂度工具或计算 CRAP，协议状态分支通过场景测试和人工评审检查。

- `assessment.json`：独立复核结果、用量汇总、被测源码 SHA-256。
- `results.jsonl` / `summary.json`：7 次实际脱敏观测与 CLI 事件。
- `*.response.txt`：真实网关向客户端发出的 SSE/JSON（仅合成任务结果）。
- `*codex*.jsonl` / `*.stderr.txt`：实际 Codex CLI 日志。
- `offline_*`：离线正文与混排对照结果。
- `probe.go.txt`、`assess.py.txt` 及离线源码文本：复现与复核方法；源码保留为 .txt，避免常规测试误触发收费调用。

脱敏报告与复核材料随本轮修复一并入库；未修改忽略规则。历史报告保留原始失败事实，本报告记录修复后的新结果。
