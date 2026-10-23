# 协议转换 S2：兼容的类型化流内部改造

> 后续决策：strict/legacy 转换策略配置及请求侧拒绝分支已移除。本文涉及策略开关的内容是阶段历史；当前统一保留诊断、沿用正常调用行为，安全边界始终执行，见[当前诊断模式](2026-10-23-protocol-diagnostics-only.md)。

日期：2026-10-23。范围：类型化流事件；延续 S1 请求保留与检查，保持默认 legacy。

## 本轮决策

将跨协议流的内部链路从“源载荷 → OpenAI chunk JSON → 目标载荷”改为“源载荷 → StreamEvent Go 对象 → 目标载荷”。公网入口和 StreamConverter 的 Convert/Finish/Usage 接口保持兼容。

本轮按用户要求优先保持行为，因此采用具名语义增量帧，而非立即拆成 TextDelta/ToolStart/Terminal 等互斥事件。保留每个源帧的混合字段、choice 索引、工具参数碎片和结束原因；目标状态机继续按既有顺序消费。细粒度事件分类、严格终态证明与交付门属于后续阶段，不把内部类型化解释为它们已经完成。

## 结构

```text
同协议：原生 payload → protocolAwarePassthrough → 原生 payload
                         └→ 旁路提取 usage

跨协议：源 JSON → streamDecoder.Decode / End
                    ↓
                []StreamEvent
                    ↓
                streamEncoder.Encode / Finish → 目标 JSON
```

- StreamEvent：ID、Model、Choices、Usage。
- StreamChoice：候选索引、StreamEventDelta、FinishReason。
- StreamEventDelta：角色、正文、思考、具名工具增量、grounding。
- 工具参数仍是原始字符串片段，不要求每一段是有效 JSON；需要整包参数的目标由原状态机累积。
- decoder/encoder 工厂统一登记在 providerImpl；生产跨协议路径不调用旧 wire→wire 中转。
- 源协议旧构造器/Convert 等内部兼容入口保留薄包装，最终输出 OpenAI 时才编码 JSON，不维护第二套语义映射。
- 同步 batch 推进，编码/写回沿既有链路背压，无新增 goroutine、队列或全量事件缓存。

## 兼容边界

1. 同协议仍保留 payload 字节（包括未知字段、未知类型、大整数及非标准载荷），仅旁路读取 usage。网关的 SSE reader/writer 本轮不变；这不是 event/id/retry、多行 data 或原始 SSE 帧逐字节保真的新承诺。
2. 跨协议继续只消费当前支持的首 choice，同帧按工具 > 思考 > 正文的现有互斥优先级处理。role 不吞同帧工具，结束原因在 role 守卫前保存。
3. usage-only 事件仍不提前开启目标消息；真实零 usage 与缺失不同。输入估算仅在 Anthropic 目标需要且真实 usage 缺失时惰性调用。
4. 保留 Responses output 生命周期、截断终态映射、reasoning 桥签名、并行工具及晚到 grounding 规则。各尝试实例拥有独立 decoder/encoder 状态。
5. 不新增 response.failed 的拒绝规则，不改变未知跨协议事件处理，不以本轮改造名义改变 EOF/Finish、提交时机、重试/熔断或 HTTP attempt 数量。
6. strict 仍需显式配置；本轮不修改 S1 模式选择、运行配置或应用进程。

## 特别记录：旧中间用量的兼容投影

基线差分发现非 Chat→非 Chat 的旧中间 JSON 会丢掉 flat cache_read_tokens/cache_write_tokens：Usage 的编码与 OpenAI 解码规则并不对称。类型化直接传值原本会改变客户端最终 usage，例如 Gemini→Responses 的缓存输入细项。

本轮通过具名 legacyIntermediateUsage 在目标编码前对副本执行旧有效语义投影：仅清除中间路径过去未能传递的缓存细项，不修改 PromptTokens/TotalTokens、不改 decoder 源用量。日志读取的 Converter.Usage() 保持完整。Chat 源、Chat 目标和同协议不应用该投影。

这是显式保留既有客户端输出，不代表认可该历史口径。用量统一应单独修改契约和验证，不能在结构重构中悄然改变结果，也不能恢复中间 JSON 往返来模拟兼容。

## 测试设计与验证方法

复用现有 Go testing、协议矩阵与 gateway httptest。新增测试强调以下可观察行为：

| 场景 | 断言 |
| --- | --- |
| 内部链路 | decoder 产出的类型事件直达 encoder，不经 JSON 中转 |
| 工厂注册 | 四协议具有 typed decoder/encoder；跨协议走 typed，同协议走 passthrough |
| 同帧组合 | 角色、工具、思考、正文和 finish 的字段/索引保留；目标优先级不变 |
| 元数据与用量 | usage-only 不开消息；真实零 usage 不触发估算；估算只调用一次 |
| 工具与思考 | 复用并行工具、参数首段、reasoning 桥、Responses 生命周期回归 |
| 原生透传 | 四协议 payload 含未知字段/非 JSON 不被重建；网关三种客户端入口保持未知 payload 和原生 usage |
| 状态隔离 | 分别创建的尝试不共享 decoder/encoder；交错推进用量互不污染 |
| 用量兼容 | 客户端保留旧中间用量效果，源用量与日志不受副本投影影响 |
| 调用边界 | 复用提交后不重试、首包前重试、错误帧与默认 legacy 网关测试 |

改造前源码快照保存在 `/tmp/portunus-stream-s2-baseline/protocol`。使用相同外部调用 harness，比较 7 组源流 × 4 个目标的 28 组结果，覆盖混合工具/思考、多个 choices、usage-only、未知原生载荷、Responses incomplete/failed、晚到 grounding。比较每次 Convert 的 batch、Finish、错误及 Usage；仅将生成的 id/item_id 统一为占位符，不能据此替代既有 ID 配对测试。同协议 payload 字节保留另有直接断言。

## 验证记录

实际验证：

- `go test ./backend/protocol ./backend/gateway`：通过。
- 最终 `go test ./...`：全部通过，包含 S1 配置/日志、协议矩阵和网关调用边界回归。
- `go vet ./...`、`git diff --check`：通过。
- 基线与当前实现各执行相同 harness，生成 before.json/after.json，`cmp`：28 组结果一致；生成 ID 做前述归一。同协议 payload 原字节另有单元与网关集成断言。
- diff-review：Correctness、Standards、Spec 三轴均无 finding；审查是独立只读检查，执行测试和差分由主代理完成。

复杂度审计降级 unavailable：环境无既有复杂度工具/配置，不安装依赖或生成伪指标。无配置好的突变测试链，跳过突变测试。未执行真实供应商/客户端图形界面调用或吞吐基准，不声称性能提升倍数。差分验证限定上述样本，不是任意输入的等价证明。

## 后续范围

完整 SSE envelope 保留、细粒度事件生命周期/终态证据、预提交缓存和交付确认、请求级尝试预算、响应侧原生扩展及资源上限继续单独推进。本轮不默认开启 strict，也不修正历史用量口径或扩大协议能力。

此文档创建时未被跟踪；本轮不改 `.gitignore`。
