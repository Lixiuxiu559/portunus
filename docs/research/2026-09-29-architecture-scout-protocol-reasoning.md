# 架构机会侦察：protocol 的 reasoning/thinking 跨协议处理

## 研究范围

- 目标模块 / 主题：`backend/protocol` 的 reasoning/thinking 跨协议处理（含 gateway / channel 交界处）
- 调查范围：`backend/protocol`（15 个非测试文件 4001 行 + 14 个测试文件 4062 行，106 个测试函数）、`backend/gateway`（非测试 1242 行）、`backend/channel`、`docs/research/`
- git 基线：main @ 911533d（v0.3.4）；历史追溯到 2026-08-25 初始提交
- 调查方式：3 个并行 researcher（结构调用链 / 历史变更热点 / 测试与约束反证），主 Agent 对关键事实做了逐行抽查核验

## 调查结论

**protocol 的宏观结构是健康的，不建议动转换骨架。** canonical 枢纽（N² → N+N）、出向 `blockSink` 骨架（e17f020）、SSE 帧器、usage 编译期穷尽（provider_registry.go:18-20）、`ComposeUpstreamRequest` 单入口（a175bfd）五轮收敛都经受住了考验：12 次 reasoning 主题提交中"同一规则跨文件同构改写"只发生过 1 次（d8924ea），多数修复由单文件消化。

**真正值得进一步设计的是：转换能力的覆盖状态（哪个协议 × 哪个方向 × 流式与否保留思考内容）是散落在 6 个文件里的隐式知识，没有任何接口面陈述它。** 近一个月 8 次"思考内容在某条路径丢失"的线上事故全是同一模式——某个格子没填，直到真实流量撞上。当前矩阵中仍有 3 格"流式有、非流式无"的疑似无意遗漏。

---

## 候选卡片

### 候选 1：能力覆盖契约显式化——「协议 × 方向 × 模式」矩阵从隐式变显式（Strong）

**涉及文件 / 模块**

- `backend/protocol/provider_registry.go:11-22` —— `providerImpl` 的 8 个能力槽位，无任何 reasoning 能力声明
- `backend/protocol/anthropic.go:643-687` —— 非流式响应入向（缺 thinking 块 case）
- `backend/protocol/responses.go:431-462` —— 非流式响应出向（不读 `ReasoningContent`）
- `backend/protocol/gemini.go:398-421` —— 非流式响应出向（不产 thought part）
- `backend/protocol/reasoning_align_test.go` —— 手工对齐测试（非穷尽）
- `backend/gateway/relay.go:234` —— gateway 对 protocol 的全部依赖是入口级调用

**当前矩阵（主 Agent 逐行核验）**

入向（native → canonical，生产 `ReasoningContent`）：

| 协议 | 请求入向 | 响应入向 · 非流式 | 响应入向 · 流式 |
|---|---|---|---|
| anthropic | ✅ 桥/明文/占位（anthropic.go:298-315） | ❌ **无 thinking case**（:651-667） | ✅（anthropic_stream.go:119-120） |
| responses | ✅ pendingReasoning 归属（responses.go:200-239） | ✅（:401-416） | ✅（:491-499） |
| gemini | ✅ geminiContentToChat（:162-165） | ✅ 共享同 helper（:367） | ✅（:479-482） |
| openai | ✅ canonical 原生字段 | ✅ | ✅ |

出向（canonical → native，消费 `ReasoningContent`）：

| 协议 | 请求出向 · 渲染 | 响应出向 · 非流式 | 响应出向 · 流式 sink |
|---|---|---|---|
| anthropic | ❌ 只渲染 text+tool_use（:526-539） | ✅ thinking 块+桥签名（:720-728） | ✅（anthropic_stream.go:247-252） |
| responses | ❌ 只渲染 message/function_call（:318-334） | ❌ **不产 reasoning item** | ✅（:595-597） |
| gemini | ❌ 只渲染 text/functionCall（:221-229） | ❌ **不产 thought part** | ✅（:523-525） |
| openai | ✅ canonical 原生字段序列化 | ✅ | ✅ |

流式列全 ✅ 正是因为出向流式有 `blockSink` 编译期强制（`ReasoningDelta` 是接口方法，漏实现编译失败）；**非流式和请求渲染方向没有任何契约**——`providerImpl` 的 8 个槽位里没有 reasoning 声明，格子的填与不填全靠实现者记得。

**真实摩擦**

- 近一个月 8 次线上事故驱动的修复，全部是"补格子"：e467195（请求入向丢弃）、9703824（响应流式+非流式丢弃，同一 400 症状分两次修）、d8924ea（gemini/responses 入向）、e17f020（出向流式 gemini/responses 丢弃）、d314984（Responses input item）、340159a（redacted 转空串）等。提交信息含线上 request id（如 dd74c857、"64 失败/100 成功"）。
- `reasoning_align_test.go` 的存在本身说明"全协议对齐曾经漏过"，但它是手工抽查：测哪些协议对、哪些方向，取决于谁记得写。新协议接入时没有任何机制提醒补一行。
- 今日矩阵中仍有 3 格"流式有、非流式无"的疑似无意遗漏（上表加粗）——同一协议内流式与非流式行为不一致，是有意取舍的可能性低（若是取舍，无任何地方登记理由）。

**当前问题**

调用方与维护者无法从 protocol 的任何接口得知"这条转换路径是否保留思考内容"。这个契约知识物理上散在 6 个文件的 switch/case 里。深模块应当"接口简单稳定、内部承载业务知识"——现在承载是对的，但**没有任何一处陈述它**。

**删除测试**

- 删除/合并这层后：**复杂度集中**。
- 证据：这层不是运行时包装，而是契约的陈述点。两种实现粒度——(a) 纯测试矩阵：参数化生成 from × to × 流式/非流式 × 请求/响应 的用例，每格断言登记的期望（保留/丢弃/占位）；(b) registry 加能力声明字段 + 编译期强制新协议表态（同 `newUsageExtractor` 先例，provider_registry.go:18-20 注释"编译期穷尽：新增协议必须实现，否则直通流式计费丢 token"——同样的理由适用于 reasoning：漏了静默丢内容）。调用方（gateway）零变化，协调逻辑无处可转移。

**反证**

- 最小整理（逐个补齐 3 个丢失格子 + 给 reasoning_align_test 加用例）确实能解决 reasoning 这一个能力的当前缺口——但解决不了"下一个能力再来一遍"。这个项目的能力维度不只 reasoning：图片（tool_result 里的图片已多次踩坑，anthropic.go:336-339 的注释就是事故记录）、finish_reason 语义、多模态 content，都是潜在的同构矩阵。
- 若产品决定那 3 格"故意不填"（如非流式出向前端不展示思考），矩阵的价值下降为登记 intentional-skip——但即便如此，显式登记也比考古代码强。
- 全部是测试基建，不产出用户可见的新功能——优先级取决于后续还要接多少协议/能力。

**三个选项**

| 选项 | 内容 | 成本与风险 |
|---|---|---|
| 维持现状 | 3 个疑似遗漏格子继续静默丢内容；下一个能力接入时靠人肉记得 | 修复学费继续按"线上事故"计价（已有 8 次） |
| 最小整理 | 补 3 个格子 + reasoning_align_test 加用例 + 修正注释矛盾 | 一天内可完成；防复发能力为零 |
| 结构性深化 | registry 能力声明（编译期强制表态）+ 生成式矩阵测试，把每个格子的期望收敛到一张表 | 约 2-4 天；零运行时行为变化，回滚 = 删测试；初期需人工标定每格期望 |

**为什么最小整理不够**

最小整理修的是 reasoning 这一次的洞；矩阵契约防的是"能力 × 协议 × 方向"这个类的洞。8 次事故的共性不是"reasoning 特别难"，而是"没有任何机制暴露格子没填"。`usage` 槽位（编译期穷尽）与 `blockSink`（接口方法强制）两个先例在本仓库已被验证有效——reasoning 只是第三个撞上同一结构缺口的能力。

**预期收益**

- locality：14 格契约知识收敛到一张表；新协议接入时的 checklist 从"读 6 个文件"变为"填一列登记"。
- leverage：下一个能力（图片跨协议、cache_control、tool_choice 语义）直接复用矩阵框架。
- 测试：矩阵测试天然防回归；现有 106 个黑盒测试可完整保护迁移（零行为变化）。

**迁移风险**

- 公共调用方：无（gateway 不感知此层）。
- 数据/错误语义：无（纯测试 + 登记字段，不改变运行时分发）。
- 测试保护：强（若想顺带补齐 3 个丢失格子，每个格子补齐是对外行为变化，现有对应方向测试会兜底）。
- 分阶段迁移与回滚：先 (a) 矩阵测试标定现状（含 3 个 ❌ 作为登记的当前行为），再逐格决定补齐或标 intentional-skip；任何阶段删测试即回滚。

**需要进一步确认**

- 3 个疑似遗漏格子是否已有真实用户流量撞上（拉 `shared.Log` 里 46fae96 新落的 `bridge_decoded` / `shim_rewrites` 分布可部分回答）。
- 请求出向 3 格（canonical → anthropic/responses/gemini 上游请求不回传思考）是产品取舍还是遗漏——各上游其实都有承载形状（anthropic thinking 块、responses reasoning item、gemini thought part），需要产品判断。

---

## 待调查区（Speculative，未过删除测试，不占推荐名额）

### 入向流式（X → openai）三份手写 switch 是否值得骨架化

`anthropicToOpenAIStream.Convert`（anthropic_stream.go:82，64 行）、`responsesToOpenAIStream.Convert`（responses.go:474，69 行）、`geminiToOpenAIStream.Convert`（gemini.go:454，41 行）各自手写事件 switch，只共享薄状态工具 `openAIStreamState`（stream.go:186-251）。d8924ea 曾需在三处同构落笔"入向流式 reasoning"。

**为何只是 Speculative**：删除测试无法确认通过。from 方向（openai→X）骨架成立的前提是输入已统一为 openai chunk；to 方向三家的事件模型差异大（anthropic 的 content_block 生命周期、responses 的 output_item 序列、gemini 的整包 parts 数组——后者甚至不是增量事件流），强行共享可能制造错误抽象。需要逐行比对三份控制流、量化"可统一 / 协议独有"比例后才可判定。若要调查，gemini 大概率应排除在外（模型不同）。

## 最小整理清单（不构成结构候选，可随手做）

1. **thinking 意图构造下沉 protocol**：`gateway/relay.go:74-90` 注释自称"gateway 不再触碰协议知识"，实际硬编码 `Type: "enabled"` 并解析 anthropic 的 `thinking.budget_tokens` 形状（:85-89）。收拢为 protocol 侧一个解析函数即可，注释随之对齐。
2. **gateway 测试解耦桥编码**：`relay_test.go:173` 直接调用 `protocol.EncodeReasoningSignature` 拼请求体——桥载荷格式已是事实上的跨包契约，至少应为 protocol 提供导出的测试构造 helper。
3. **responses.go 两份提取规则已漂移**：`responsesReasoningText`（:247-258，`TrimSpace`、`\n\n` 分隔）vs `responsesResponseToOpenAI` 内联块（:401-416，不 trim、`+=` 直拼、兜底条件略异）。合并为一个 helper。
4. **两层占位注入粒度不一致**：anthropic.go:313-314 对 `redacted_thinking` 解码失败**无条件**注入 `ReasoningPlaceholder`，而装配末端垫片 thinking_compat.go:55-62 **只认带非空 tool_calls 的 assistant**。两者服务不同场景（"有密文读不出" vs "压根没带来"），但粒度差异未在任何地方说明理由——对齐或在两处写明因果。
5. **注释与实证矛盾**：thinking_compat.go:31-35 称收窄垫片粒度的理由是"减少前缀漂移面积……缓存命中率低的成因之一"，而同一提交新建的 prefix_stability_test.go:100-103 实证结论相反（"收窄垫片范围对缓存命中没有收益——收益是保真度"）。测试一方是对的（注入是历史的确定性函数），注释宜修正。
6. **疑似不可达分支**：`reasoningSignatureFallback`（anthropic_stream.go:306-308）在当前骨架下不可达（骨架只在 `ReasoningContent != ""` 时回调 `ReasoningDelta`，累积器恒非空）。动态验证后删除或注明保留理由。

## 风险警报（非架构候选，建议单独立任务核实）

**同协议直通会把网关产的桥签名透传给真 anthropic 上游。** 混协议分组 failover 切换场景：anthropic 客户端第一轮打到 openai 上游 → 网关返回的 thinking 块带 `portunus-reasoning-v1:` 桥签名 → 第二轮 failover 到 anthropic 上游 → `rewriteSameProtocol`（convert.go:132-154）原样透传 → 真 Anthropic 上游对 signature 有校验，可能 400（"Invalid 'signature' in 'thinking' block"）。`docs/research/cc-switch-retry.md:162` 记录了 cc-switch 为同类问题实现了签名整流器，portunus 未实现对应能力。未在真实渠道验证过，置信度中。

## 推荐顺序

1. **候选 1（矩阵契约显式化）** —— 证据强度最高（8 次同模式事故 + 3 个现存疑似遗漏 + 两个已验证的本仓库先例），且第一步（矩阵测试标定现状）零行为风险。时间敏感性在于：3 个丢失格子此刻正在静默丢内容，而每一轮新的线上排查都在重复支付同一类学费。
2. 最小整理清单可随日常改动顺手完成，不建议为此立项。
3. 入向流式骨架化（Speculative）只在做完候选 1、矩阵全绿之后再考虑——届时才有数据回答"三份 switch 的共性比例"。

## 下一步

```text
你想深入哪一个候选？也可以选择暂不改造。
```
