# 完整 SSE 与交付控制实施记录（S3）

> 后续决策：strict/legacy 转换策略配置及请求侧拒绝分支已移除。本文涉及策略开关的内容是阶段历史；当前统一保留诊断、沿用正常调用行为，安全边界始终执行，见[当前诊断模式](2026-10-23-protocol-diagnostics-only.md)。

本阶段继续 S2 的类型化流事件改造，完成完整 SSE 解帧、来源终态观察、预提交缓存和交付后的禁止重放。`proxy.conversion_policy` 仍默认 `legacy`，不自动开启 strict；非流式调用和普通请求的转换规则沿用现有行为。

## 完整事件与终态

`protocol.SSEReader` 保留 `event`、`id`、`retry`、多行 `data`、注释和原始帧，支持 LF/CRLF/CR 与分片读取。裸 CR 立即结束行，不等待下一字节；若 CRLF 恰跨读取且 LF 尚未到达，LF 会随下一帧保留，连续转发的字节顺序不变。读取时限制单事件原始字节为 1 MiB，避免超长无换行数据先无限分配。同协议按原始帧转发，旁路转换器仅采集 usage；未知原生数据保留，保守标记禁止重放。已开始交付后的同协议原生 error 帧保留原来的类型和 SSE 字段。

`protocol.StreamSession` 独立观察源协议终态：

| 来源 | 终态证据 |
| --- | --- |
| Chat | 已观察 choice 均具有有效 finish_reason，随后允许 usage 和 [DONE] |
| Messages | 有效 stop_reason 加 message_stop |
| Responses | 有效 response.completed / response.incomplete；response.failed 为失败 |
| Gemini | candidate finishReason 或 promptFeedback.blockReason |

预算截断、过滤、拒答、工具完成及合法无正文结束按实际终态传播，不因没有文本重试。仅 EOF 或仅 [DONE] 不构成成功；缺终态返回 truncated，不能调用旧 Finish 伪造成功。

跨协议在源终态后最多等待 1 秒收集允许的 usage 尾帧，再统一编码目标终态。尾部读取错误或超时不撤销已确认的语义结果，记录 `stream_tail_limited`；这不证明 usage 已完整采集。同协议不重建上游终态，不追加成功 [DONE]。终态后的矛盾内容属于协议错误。

## 提交门和重放

`gateway.deliveryGate` 是流式客户端写入口。role、created、空块、usage、注释和心跳先留在本次尝试中；正文、思考、拒答、工具标识/参数、未知原生事件或有效空终态触发提交。缓存随失败尝试丢弃，转换器、ID、序号与 usage 不跨尝试复用。

默认预提交上限为 256 KiB、128 个事件，任一先到即停止整个请求，不为了释放缓存提前提交，也不轮换目标。字节预算按每事件原始帧和编码结果的较大值累计，包含没有目标输出的源元数据；实际保存的是目标帧。单源帧还受 1 MiB 读取上限约束。该限制不是整条转换链的总内存承诺。

首内容等待沿用 `FirstByteTimeoutSeconds`，元数据和心跳不能续期；提交后沿用 `StreamIdleTimeoutSeconds`。源终态后的尾部窗口也不能由心跳延长。

首次尝试写头/帧前即标记交付开始；部分写入、短写、Flush 失败和客户端取消都禁止重试及换家。Gin 的 Flush 没有错误返回，因此解开 wrapper 后观察底层 FlushError。客户端写失败和取消不计渠道熔断。

普通生成在未交付时仍沿用现有有限重试。原生 computer、MCP、代码执行、状态续接及未知原生操作可能在输出前产生作用，发送后保守禁止重放；网络错误、HTTP 5xx/429、SSE 错误及缺终态都受此限制。普通客户端执行的 function 声明和明确允许的只读搜索声明保留未交付重试能力。这里不承诺供应商只执行一次，也不证明客户端实际收到写出的内容。

本地缓存/帧大小上限和解析错误停止整个请求，不喂上游熔断；缺终态和供应商可重试错误沿统一 `failSpec` 分类。交付后的上游中断只记失败并结束当前流。

## 观测与范围

每条 relay 日志新增来源 outcome/reason、源终态证据、交付开始、终态写出、写失败、尾部窗口受限摘要；原因仅保存静态分类，不保存原生正文。失败尝试已采集的 usage 也独立记录。它们描述一次 relay 尝试，不是客户端完整请求凭证，也不改变独立 HTTP attempt 和旧日志聚合口径。

本阶段没有引入请求级统一 HTTP 总预算、跨目标累计首内容 deadline、退避调度、可信续接存储或完整响应扩展恢复；继续沿用现有有限候选与 RetryCount。多 choice/多 candidate 的跨协议完整表示能力也未在本阶段扩大。不能把本阶段的事件大小限制当作全文或工具参数累积器的总内存上限。

## 验证

新增协议测试覆盖完整帧、多行 data、不同换行与分片、资源上限、四协议终态及跨协议组合、合法空终态、失败/缺终态不伪造成功、未知原生数据、尾部 usage 和重放资格。

新增网关测试覆盖尝试缓存隔离、绝对首内容期限、缓存限额、部分写/短写/FlushError、交付后 EOF、取消、原生操作失败禁止重放、尾部窗口、真实网关日志及完整原生 SSE。历史成功 fixture 补齐供应商语义终态，保留原有断言。

最终执行 `go test ./...`、`go vet ./...`、`go test -race ./backend/gateway ./backend/protocol` 均通过，`git diff --check` 无错误。未调用真实供应商或真实 SDK/CLI，未重启正式服务。

质量审查：Correctness 独立轴发现的原生操作 5xx 重放、opaque SSE 拒绝、原生错误帧丢失三项已修并复核关闭；Standards 独立轴无剩余有依据问题；Spec 复核的问题已修，协议实现者参与该轴，协议部分独立性降级。项目没有已配置的复杂度或突变工具，本轮未安装或运行这两类工具，不提供 CRAP 或突变分数。
