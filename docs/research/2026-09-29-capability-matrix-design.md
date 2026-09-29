# 候选 1 详细设计：协议转换能力覆盖矩阵（reasoning 试点）

> 前置报告：`docs/research/2026-09-29-architecture-scout-protocol-reasoning.md`
> 本文是深度研究 + 设计产物，未动任何代码。实施与否待决策。

## 1. 深度研究结论

### 1.1 现实流量（data/portunus-log.db，17780 条，2026-09-06 ~ 09-29）

- 三个疑似遗漏格子（anthropic 非流式响应入向、responses/gemini 非流式响应出向）**现实流量均为 0**：当前 3 个渠道 type 全为 openai，客户端只有 anthropic（227 条，全部流式）与遥测前的存量。
- 桥与垫片**实证生效**：`bridge_decoded>0` 占 anthropic 客户端请求 88.5%（201/227，累计还原 10408 次）；`shim_rewrites>0` 192 条。
- 同族事故真实发生过：**151 次** `reasoning_content must be passed back` 400（09-07~09-14，DeepSeek 上游），09-14 垫片默认开启后归零。
- 桥签名直通透传风险：零现实触发（无 anthropic 上游渠道），维持"假设级风险"。
- 结论修正：矩阵契约**不是救火，是低成本防复发基建**——3 个格子无人正在受苦，但事故模式（某条路径静默丢内容，直到真实流量撞上）已发生 8 次。

### 1.2 各协议官方回传语义（决定每个格子的登记值）

| 格子 | 官方语义 | 登记 |
|---|---|---|
| 请求出向 → anthropic | signature 必须原样回传（`thinking_block_param.py`），默认 `prefix_mismatch_behavior="error"` → 400；canonical 只有明文无合法签名，填格只能伪造——cc-switch 宁可报错、axonhub 宁可丢块，都**不伪造** | **skip**（登记理由：伪造签名必被 400，丢弃更安全） |
| 请求出向 → responses | 普通多轮不需要回传；含工具轮"需要"（质量口径，未见 400）；官方推荐形态是 `encrypted_content` 加密回传，明文 item 会被无害丢弃（cookbook reasoning_items.ipynb） | **skip**（登记理由：非加密明文回传无收益；工具轮加密回传属独立功能） |
| 请求出向 → gemini | 官方强制的是 `thoughtSignature`（Gemini 3 Pro + 工具调用 "mandatory to return that part"），不是 thought 明文；生态通行做法是填默认魔法签名（new-API / axonhub 同款 `context_engineering_is_the_way_to_go`） | **skip**（登记理由：缺的是签名不是明文；补签名属独立功能，见 §5 衍生发现） |
| 响应入向 ← anthropic（非流式） | 上游确实返回 `content[].type=thinking`（官方形状，portunus 的 `ContentBlock` 已定义该类型 anthropic.go:63，解析器却无 case） | **Phase 2 补齐** |
| 响应出向 → responses/gemini（非流式） | 流式方向已在产出（`reasoning_summary_text.delta` / `thought:true` part），非流式纯属漏写 | **Phase 2 补齐** |

### 1.3 测试基建机制（设计约束）

- `newUsageExtractor` 的"编译期穷尽"真相：注册表槽位 + 漏填运行期 panic 的显眼度强制，**并非语言级**。由此**否决** registry 加 `bool` 能力槽的方案——bool 零值静默，反而失去可发现性。
- 采用**测试期穷尽**：矩阵测试遍历 `providerImpls`（provider_registry.go:36）的全部 key，新协议未在期望表登记即红。与 usage 的 panic 显眼度同属"漏了就炸"，且更早（CI 而非线上）。
- **canonical 枢纽的红利**：期望表按「协议 × 位置」声明（24 个标量），而非 from×to 全积（48 格）。矩阵 runner 再以双循环展开全积，顺带验证降维假设成立（中间不引入额外损耗）。
- 出向断言用**各协议原生探测器**（查 `thinking` 块 / `reasoning` item / `thought` part / `reasoning_content` 字段），不走 parse 回环，避免"两端能力互为 confounder"。

## 2. 目标与非目标

**目标**：把"哪条转换路径保留思考内容"从散在 6 个文件的隐式知识，变成一张带出处的显式登记表 + 机械验证的测试。

**非目标**：不动运行时分发结构；不做 gemini `thoughtSignature` 注入 / responses `encrypted_content` 回传（见 §5 衍生发现）；不扩展到 reasoning 之外的能力维度（框架留好扩展位即可）。

## 3. 设计

### 3.1 新测试文件 `backend/protocol/capability_matrix_test.go`

四个组件，估算 ~350 行：

**(1) 期望登记表**（每协议两段，值域 `keep` / `drop` / `placeholder`，每条 skip 带理由注释）：

```
入向（from 协议 → canonical）：  { reqParse, respParse, respStreamParse }
  openai     keep / keep / keep
  anthropic  keep / drop(→P2: keep) / keep
  responses  keep / keep / keep
  gemini     keep / keep / keep

出向（canonical → to 协议）：    { reqRender, respRender, respStreamRender }
  openai     keep / keep / keep
  anthropic  skip（伪造签名 400）/ keep / keep
  responses  skip（明文回传无收益）/ drop(→P2: keep) / keep
  gemini     skip（缺的是签名）/ drop(→P2: keep) / keep
```

placeholder 当前无格（anthropic 请求入向 redacted 解码失败的兜底走 `ReasoningPlaceholder`，属 keep 的降级形态，先不单列，需要时再加枚举值）。

**(2) 探针 fixture**（每协议一份，共 8 份：请求、非流式响应；流式事件序列复用 stream_test.go 现成样本改探针文本）。统一含探针字符串（如 `思考探针`）。

**(3) 原生探测器**（每协议 ~10 行，检测输出体里思考内容存在/取值）。

**(4) runner + 穷尽检查**：
- 一致性测试：`providerImpls` 每个 key 必须出现在两张登记表——新协议忘登记即红。
- 位置测试（12 个）：探针 → 该协议的 parse/render 入口 → openai 搭档/探测器，钉死每个协议自身能力。
- 全积测试（双循环 16 组合 × 3 面）：`ConvertRequest` / `ConvertResponse` / `NewStreamConverter` 三个公开入口，期望 = 入向[from] ∧ 出向[to]（直通格天然 keep，一并验证）。

**与现有测试的关系**：矩阵测"在不在"，`reasoning_align_test.go` / `convert_test.go` 测"形状对不对"，互补不替代；矩阵 runner 只用三个公开入口（黑盒），与现有 106 个测试的驱动方式一致。

### 3.2 Phase 2 补齐三个响应格子（~30 行生产代码）

1. `anthropicResponseToOpenAI`（anthropic.go:651-667）加 `thinking` / `redacted_thinking` case：thinking 先试 `DecodeReasoningSignature(block.Signature)` 失败退 `block.Thinking` 明文——与请求入向（anthropic.go:298-302）行为一致；redacted_thinking 只在 `Data` 恰为桥载荷时还原，非桥密文跳过（不注占位）——对齐流式入向 `anthropicToOpenAIStream` 对 redacted 事件整体忽略的现状，保同协议内流式/非流式一致（占位是请求方向喂严格上游的手段，响应方向无此约束）。
2. `responsesResponseFromOpenAI`（responses.go:438-457）：`ReasoningContent` 非空时在 message item 之前插 `OutputItem{Type:"reasoning", Summary:[{Type:"summary_text", Text:rc}]}`——与流式 sink（responses.go:595-597）对称。
3. `geminiResponseFromOpenAI`（gemini.go:398-421）：`ReasoningContent` 非空时在 text part 之前插 `{Text:rc, Thought:true}`——与流式 sink（gemini.go:523-525）对称。

各自补一条形状断言进 `convert_test.go`（顺序、块类型、json 字段名），矩阵只翻期望值。

### 3.3 CONTEXT.md 词条（本仓库惯例）

> **能力覆盖矩阵 capability matrix** | `protocol.providerImpls` × 期望登记表 | 每协议 × 6 位置（请求/响应 × 入向/出向 × 流式与否）对能力（当前为思考内容）的处置声明：keep / drop / skip（带理由）。新协议接入必须在登记表表态，矩阵测试（capability_matrix_test.go）穷尽 `providerImpls` 全 key，未登记即红。skip 的理由引用官方语义考证。

## 4. 分期与成本

| 阶段 | 内容 | 成本 | 风险 |
|---|---|---|---|
| P1 | 矩阵测试标定现状（3 格登记 drop、请求出向 3 格登记 skip+出处） | ~350 行测试，半天 | 零（无运行时变化） |
| P2 | 补齐 3 个响应格子 + 期望翻转 + 形状测试 | ~30 行生产 + ~90 行测试，半天 | 低（与流式行为对齐；三格当前零流量） |
| P3（可选） | 扩展能力维度（图片、finish_reason 语义等） | 每维度复用 runner，探针+登记 | — |

回滚：P1 删测试即回滚；P2 每格独立提交，单格 revert。

## 5. 衍生发现（记录，不在本次范围）

1. **gemini `thoughtSignature` 缺失**：Gemini 3 Pro + 工具调用场景官方 mandatory，缺失报 `MISSING_THOUGHT_SIGNATURE`；生态做法是注入默认签名（new-API / axonhub 同款魔法串 `context_engineering_is_the_way_to_go`）。portunus 的 gemini 上游请求出向完全没有签名注入——接入 gemini 渠道跑多轮工具调用时预期踩雷。**独立功能候选**。
2. **responses 工具轮 reasoning item 加密回传**（`encrypted_content` + `store:false`）：Codex 型客户端经网关中转时的连续性优化。独立功能候选。
3. 桥签名直通透传（anthropic 客户端 × failover 到 anthropic 上游 → 真 Anthropic 拒收伪造签名）：假设级风险（当前无此类渠道）。若实施，参照 axonhub 的"签名像不像 Anthropic 签发"校验 + 丢块策略，比 cc-switch 的整流器（400 后剥离重发）更前置。

## 6. go/no-go 建议

**建议 GO（P1+P2 合计约一天）**，但以"防复发基建"立项而非救火：

- 支持：事故模式有 151 次线上实证的同族前科 + 8 次 reasoning 修复中 5 次是"格子漏填"；本仓库 usage/blockSink 两个先例证明这类收敛在本团队有效；成本极低、零运行时风险；且语义考证意外地给登记表提供了现成的官方出处。
- 不紧迫的证据（诚实记录）：3 个休眠格子零流量；当前主力形态（anthropic 客户端 + openai 上游）已被桥+垫片良好服务。
- 若 NO-GO：代价是下次接入 anthropic/gemini/responses 上游渠道或新协议时，重新靠线上事故付学费——而这个项目 v0.3.x 一个月内新增了两类协议处理场景，演进速度说明那一天不会太远。
