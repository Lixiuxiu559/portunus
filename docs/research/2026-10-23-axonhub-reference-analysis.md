# AxonHub 对 Portunus 的借鉴分析

日期：2026-10-23。范围：协议转换、路由与负载均衡、重试与观测。仅调研，未修改业务代码。

## 结论

值得借鉴，但主要价值是补齐调度信号、协议保真与失败边界，而不是重新实现转换和负载均衡。Portunus 已有四协议转换、三种分组策略、模型级熔断、提交响应前重试与独立上游尝试记录。建议保留这些骨架，先完善能力筛选与重试治理，再增设可选的智能策略。

## 问题、方法与版本

- 问题：AxonHub 哪些机制能改善 Portunus？适配成本、优先级与边界是什么？
- 方法：读取官方仓库和文档；下载公开源码到 `/tmp/axonhub-portunus-20261023`；与本地 Portunus 源码交叉核查。独立调查本地实现和 AxonHub 调度，主调查抽查关键证据。
- AxonHub：默认 `unstable` 分支，提交 `c0e3374c3486b9a4cfc88955141dc6b462462e8e`。下列 AxonHub 源码链接固定到该版本。
- Portunus：本地提交 `4aa25279429254bfacf8c3278b22f90f0c37fa71`，开始调查时工作区干净。
- 验证：本地执行 `go test ./backend/router ./backend/protocol ./backend/gateway`，三个包通过，输出为 cached；没有执行 AxonHub 测试、真实上游生成调用或性能压测。

## 已确认事实：Portunus 当前能力

| 领域 | 当前实现 | 本地源码证据 |
| --- | --- | --- |
| 协议 | OpenAI Chat、Responses、Anthropic、Gemini 四类上游；客户端生成入口为前三类 | `backend/protocol/protocol.go:6`；`backend/gateway/gateway.go:20` |
| 转换 | 跨协议经扩展 OpenAI Chat canonical；同协议直通保留未知字段，仅覆盖指定字段 | `backend/protocol/convert.go:8,110,151` |
| 流式 | 请求级有状态转换；传输分帧与协议转换分离；同协议提取 usage | `backend/protocol/stream.go:9,44`；`stream_frame.go:9` |
| 能力验证 | 已有 reasoning 与 hosted tool 的协议矩阵 | `backend/protocol/capability_matrix_test.go:333,463`；`hosted_tool_matrix_test.go:63` |
| 路由 | manual、round_robin、failover；轮询返回完整环形候选，失败顺延 | `backend/router/router.go:25,47,87` |
| 可靠性 | 每目标重试后换家；模型 ID 级熔断跨分组共享；等头假死快速开路 | `backend/gateway/relay.go:101,110`；`circuit_breaker.go:20,34,52` |
| 重放边界 | 流式响应提交后禁止重新生成；用户取消与上游故障分类 | `backend/gateway/relay.go:305`；`fail.go:127` |
| 调用观测 | request_id、TTFT、总时长、token/cache usage、每次显式 HTTP Do 尝试与原因 | `backend/shared/log.go:10`；`upstream_attempt.go:7` |

当前缺口可以从调用接口直接判断：`Resolve(g)` 不接收请求能力或会话信息；GroupItem 只有模型和优先级，调度未使用权重、TTFT、inflight 或并发容量。Retry-After 目前保留给最终客户端错误响应，未形成跨请求的内部限流冷却。

## 协议转换：借鉴语义保真，而非重写中间格式

### 事实与证据

1. AxonHub 分离 Inbound、Outbound，统一覆盖请求、响应、流、错误与聚合接口：[interfaces.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/transformer/interfaces.go#L13)。Portunus 的 provider 注册表已经有相近的职责划分，不必为了外观一致整体重构。
2. 一个渠道可以解析出多个端点格式；在同一渠道内优先匹配入站格式，随后才回退到其他可处理的格式：[select_endpoints.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/select_endpoints.go#L13)。这不等于全局优先同协议供应商。
3. Responses 私有字段、原始工具与输入项使用 sidecar 保存：[provider_extensions.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/provider_extensions.go#L5)。HTTP/WS Responses 适配避免 generic-model 往返，保留原始 response/item ID 和未知字段：[protocol_codec.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/transformer/openai/responses/protocol_codec.go#L31)。
4. 对 `additional_tools` 输入不能转换到非 Responses 格式的情况，返回 UnsupportedConversion；pipeline 对此终止普通重试：[outbound.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/outbound.go#L794)、[pipeline.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/pipeline/pipeline.go#L311)。不能据此宣称其所有不可转换能力均已完整防护。
5. Responses continuation 有来源绑定：渠道、端点、格式、模型、认证身份与连接代次；缺失恢复上下文时明确要求完整历史，禁止普通故障转移：[responses_continuation.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/llm/transformer/shared/responses_continuation.go#L16)。

### 适配建议（推断）

- 先定义请求的必要能力：普通 function tool、托管搜索、原生 Responses item、reasoning/signature、续接等，并对候选执行兼容检查。
- 保留 Portunus 同协议透传；跨协议沿用现有 canonical，只对实际丢失的字段增加有来源约束的扩展。私有签名不能无条件投递其他供应商。
- 现有 hosted-tool 剥离已有测试及遥测，不是未发现的行为。建议新增明确的严格模式：先找支持该能力的成员，全部不支持则报出原因；允许降级时由明确策略控制，避免仅在日志里体现。
- 将能力矩阵从测试契约延伸到运行时选路，但仍区分协议可表达、供应商声明和实际调用验证；不能凭模型名称认定支持。
- 若以后支持 previous_response_id 或 WebSocket，必须把续接来源约束纳入路由；会话亲和不能替代完整历史或合法上游会话。

## 负载均衡：借鉴多维信号与原子准入

### 事实与证据

| 机制 | AxonHub 实现 | 对 Portunus 的价值 |
| --- | --- | --- |
| 硬优先级 | 普通候选先按 priority 分层，层内排序 | 主备与流量分配可以组合 |
| 多策略排序 | 策略接口、评分与决策明细分离 | 可以渐进加入信号，不改 relay 的执行职责 |
| 性能反馈 | 流式使用 TTFT 与 TPS 的 EWMA；非流式使用总时延 | 比统一 HTTP latency 更贴合生成体验 |
| 速率与容量 | RPM、TPM、并发和冷却参与排序 | 提前避开过载目标 |
| 原子准入 | 执行前 TryAcquireRequest，评分之外检查真实容量 | 并发请求不会仅凭旧快照同时抢满一个目标 |
| 选择计数 | 初选记账，备用候选不提前记；实际切换再记 | 减少突发扎堆与备用负载虚增 |
| 可解释决策 | 每策略得分、排名和耗时可调试 | 说明为何选中或跳过目标 |

源码：[load_balancer.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/load_balancer.go#L30)、[lb_strategy_latency.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/lb_strategy_latency.go#L110)、[rate_limit_admission.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/rate_limit_admission.go#L11)、[candidates.go](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/candidates.go#L928)。

### 不应直接照搬的边界

1. 文档将会话感知描述为 +1000 分，但当前候选流程直接 pin 历史渠道，发生在普通优先级排序前：[candidates.go:777](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/candidates.go#L777)。配额预筛仍可移除候选；选择 circuit-breaker 策略时还有执行前熔断检查，但不能把这套粘性理解为完全服从健康评分。adaptive 与 circuit-breaker 是不同策略组合，不应假定前者自动包含后者的全部行为。
2. 429 冷却/限额耗尽得到 -10000 分，保留为兜底，评分本身不是硬排除：[lb_strategy_rate_limit.go:11](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/lb_strategy_rate_limit.go#L11)。硬优先级分层下，高优先级负分目标仍可能排在低优先级健康目标前；实际发送还要经过准入等后续检查。
3. 流式 70% TTFT / 30% TPS，以及 3000ms 等常量，是其实现参数，不是 Portunus 已验证的最优配置。
4. 多模型混合分组需要谨慎解释性能：长推理任务总耗时长不代表目标差。建议性能键至少区分渠道、模型和流式类型，并设样本数与时效要求。渠道凭据共享的 RPM/并发约束则仍应按渠道或实际限额范围执行。
5. 文档关于未设置 MaxConcurrent 时回退默认连接容量的描述，与当前 `lb_strategy_rate_limit.go:208` 路径不一致；未配置上限时评分直接满分。加权轮询实际也是按窗口请求数归一化评分，并非经典 smooth WRR，叠加其他策略后不能保证严格权重流量比例。

### 推荐的 Portunus 调度顺序（设计建议，尚未实现）

```text
分组候选
→ 必要能力与续接来源检查
→ 排除熔断、有效冷却及硬容量不可用目标
→ 尊重用户主备优先级
→ 可用目标内的会话亲和
→ 权重、近期错误、延迟与在途负载排序
→ 原子获取准入名额
→ 执行尝试并释放名额、反馈指标
```

评分不能充当并发限制。候选全部不可用时明确失败或按有限预算等待，避免无限排队。保留手动、轮询、故障转移的既有含义；新增“智能”作为主动选择的策略，不默默把现有分组改成智能选路。

## 重试与熔断：近期回报更直接

AxonHub 显式区分同渠道重试与跨渠道切换，429 不执行同渠道重试：[outbound.go:1033](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/orchestrator/outbound.go#L1033)。Portunus 目前每个目标各自尝试 `RetryCount+1` 次，默认重试数为 2；没有整个候选链的总尝试数/总 deadline，普通重试循环也没有退避。

建议：

1. 添加整个客户端请求的最大尝试数与总耗时预算，覆盖普通重试、换渠道及预算字段兼容重试。总时限由剩余预算收紧单次时限；流式生成时限与首包选路预算应分别定义。
2. 429 优先换候选，解析 Retry-After 并保存限流作用域的 cooldown；无合法值时使用有上限的退避。其他瞬时故障按原因选择退避和抖动。
3. 保留现有模型级熔断；限流可能属于整条渠道凭据，不能全部混成模型故障。
4. 对 half-open 加有限探测租约。Portunus `circuit_breaker.go:61` 对 half-open 直接允许，虽有“一个探测”注释但无并发名额；AxonHub 有明确 TryBeginProbe/EndProbe，可参考：[model_circuit_breaker.go:337](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/internal/server/biz/model_circuit_breaker.go#L337)。
5. 继续禁止已交付流式内容后的自动重放；客户端取消不降低上游健康度。

## 观测与产品适配

Portunus 已有独立 HTTP attempt 记录，不需要另建一套重复日志。建议增加一次客户端请求的最终结果、终态原因和采集完整性；选路记录补充候选排除原因、亲和命中、评分依据、冷却与准入结果。

只有请求终态可靠后，才能用客户端请求成功率评估新策略。旧日志行数、关联归并数、显式 HTTP Do 次数不是完全等价的统计单位。对比指标应包括：首包延迟分位数、最终成功率、每请求尝试数、429 比例、切换次数；缓存收益需要真实 usage 支持，不能凭亲和命中推断。

适合沿用“分组就是对外 model”的现有对象，不增加一层重复维护的虚拟模型。智能调度不应顺带恢复产品已撤出的费用计算，也不需要引入 AxonHub 的组织/RBAC/GraphQL 平台结构。多模态新端点、Gemini 原生入口和 Responses WS 可独立排期，非本轮前置依赖。

## 建议排期与验收

| 阶段 | 工作 | 最小验收 |
| --- | --- | --- |
| P0 | 请求能力筛选与明确降级规则；429 冷却/跳同家重试；全局尝试预算；half-open 有限探测 | 不支持能力不静默丢失；冷却内不反复打同家；尝试数和总预算有界；恢复并发不穿透 |
| P1 | 加权调度、inflight/并发准入、可选会话亲和、解释选路 | 并发名额原子获取；取消释放；过期亲和自动清除；亲和不压过硬约束；保留旧策略行为 |
| P2 | TTFT/TPS EWMA 自适应、同渠道多端点优选、必要的 Responses 原生扩展 | 冷启动和少样本不极端倾斜；流式终态与工具生命周期回归；使用相同样本对比延迟/成功率 |

请求终态观测应随 P0/P1 同步补齐。实施时沿用已有协议矩阵与 gateway 测试，新增的关键测试应覆盖多候选预算、并发准入、冷却、亲和失效和协议不可转换情况；性能结论需另做受控负载验证。

## 复用方式与许可证事实

建议先参考接口、状态机和测试场景，再在现有模块内实现。官方 [NOTICE](https://github.com/looplj/axonhub/blob/c0e3374c3486b9a4cfc88955141dc6b462462e8e/NOTICE) 明确 `llm/` 使用 LGPL-3.0，其他部分通常为 Apache-2.0，文件级许可证优先；Bedrock 子目录另有 NOTICE。不能因仓库首页显示 Apache 就把整个转换模块当成单一 Apache 代码复制。本次未引入任何 AxonHub 代码或依赖；具体代码复用及分发方案尚未评估。

## 置信度与未确认项

- 高：现有协议骨架、策略、重试与源码级行为，均有直接源码证据；本地相关自动化测试通过。
- 中：分阶段投入顺序与适配方案，是结合本地桌面网关定位作出的工程判断。
- 未确认：真实并发规模、429/超时占比、缓存损失、策略能带来的改善幅度；未读用户生产日志，未做 A/B 或压测。
- AxonHub 文档存在部分行为描述滞后；本文对比以固定提交源码为准，不把 README 的性能宣传当作验证结果。

此报告创建时为未跟踪文件。无需入库时可删除；未修改 `.gitignore`。
