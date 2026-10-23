# 响应侧原生扩展保留（S5）

## 当前行为

新增 `protocol.ResponseEnvelope` 保存源协议、完整原始响应和带 owner/position 的片段；非流式入口为 `CaptureResponse` / `ConvertResponseWithReport`。旧 `ConvertResponse` 保持签名，内部委托新入口并忽略报告。同协议返回原始字节的独立副本，保留空白、对象字段顺序、数组顺序和大整数；未知原生工具、签名、密文、annotation 及供应商扩展不会经过 canonical JSON 重建。

跨协议仍使用原有 parse/render 映射，原生信封与 core 分开保存，不把来源格式的未知字段直接塞进目标 body。信封 `Render` 可对不同目标重新生成独立结果，不修改源快照。这是请求内原始信息保留和诊断能力，不是跨协议任意原生扩展恢复，更不提供跨请求可信续接存储。

流式每个 `StreamEvent.Native` 附独立源快照，包含完整 SSE event/data/id/retry/Raw；为解析补入 type 时不改原始快照。decoder 没有产生语义事件时，`StreamSession.NativeEvent()` 仍可查看当前源帧；会话只保存当前帧，不累积整条流的 raw 历史。合成 End/目标终态不冒用上一源帧的 ownership。同协议仍返回原始 SSE，不经过 typed encoder 重建。

`Native()`、`Original()`、`Wire()` 和会话当前帧访问都隔离可变引用。信封内部字段不参与 JSON，`StreamEvent.Native` 使用 `json:"-"`，原生原文不进入诊断或日志。片段保存数组的原始位置；对象字段按稳定键顺序登记，完整对象原始排版仍由 Original 保留。该 owner 模型用于当前已识别的结构层级，不是跨协议重放时可直接使用的完整实体身份映射。

## 覆盖与兼容诊断

| 来源 | 当前保留和检查的结构层级 |
| --- | --- |
| Chat | 顶层、choices、message/delta、工具调用、未知 message 扩展等 |
| Messages | 顶层、content block、thinking signature/redacted data、工具/引用及流事件 wrapper |
| Responses | 顶层、output item、content/summary、加密 reasoning、annotations 及逐项流事件 |
| Gemini | 顶层、candidate/content/part、thoughtSignature、工具/多模态与 grounding 扩展 |

完整原文始终保留；诊断只扫描具名、有限层级，不递归解释用户工具参数或任意 opaque 载荷。不是所有嵌套字段都有单独 fragment 或诊断。例如 usage 的任意内部扩展不在本轮全面覆盖范围内，多 choice/candidate 的原有跨协议消费行为也未扩大。

`Compatibility(to)` 返回已有 `ConversionReport` 形状，scope 分别为 `response_native_v1` 和 `stream_native_v1`。同协议的 compatible=true 仅表示原始格式能够保留，不证明响应语义成功、供应商签名可信或模型支持该能力。跨协议遇到所检查层级的未知/opaque 构造、未保留的原生数据或 SSE 元信息时，给出静态 reason/path；scope 内无问题不等于全字段无损。

不支持的原生数据只诊断，不拒绝请求、不切换目标、不重建 strict。对未能解析的同协议非流式 body 仍按原样返回，以兼容旧接口；跨协议解析错误和流式协议错误继续走原有错误路径。

流会话按静态 reason/path 去重汇总，不记录供应商动态字段名、原生类型字面值、响应 ID、签名/密文或内容。路径集合受已定义 schema 限定，避免事件数量增长导致诊断状态无界。零事件时 checked=false；处理过 SSE 后才更新检查摘要。

## 安全与资源边界

本阶段不改变原来的有意义内容判定、ReplayUnsafe、源终态识别、预提交门或 Finish 条件。未知事件不能因加入信封而变成安全重放或成功完成；错误帧继续按原协议处理。已有单帧/预提交缓存上限、交付后禁止重放、原生操作发送后限制重放、请求总 HTTP 预算、累计首内容期限、idle 超时和取消继续执行。

流原生快照仍受已有 1 MiB SSE 单事件上限约束，每帧会产生原文/片段及隔离副本的额外分配；不保存跨事件 raw 历史，不代表整条转换链常量内存。非流式仍沿用已有 ReadAll 行为，本阶段没有新增响应体总大小限制。未添加网络调用、后台任务或原生持久化。

## 日志与迁移

响应摘要与请求侧 `Conversion*` 分开，新增：

- `response_conversion_checked`
- `response_conversion_scope`
- `response_conversion_compatible`
- `response_conversion_issue_count`

只保存去重问题数量及范围，不保存原生载荷。流式失败尝试和成功尝试分别记录其摘要，不跨尝试继承；同协议收到未知扩展仍可能 compatible=true、但 outcome=truncated，两个状态不能相互代替。历史日志新增字段为零值，不据此推断过去已检查。旧策略列依旧退役，不新增开关或默认拒绝行为。

## 验证设计

- 四协议非流式 raw/大数/顺序与快照隔离；所有跨协议组合与原 parse/render 结果保持一致。
- 原生工具/未知 block/item、签名/密文、annotations 等只在对应原生格式保留；诊断无动态键值和敏感原文。
- SSE metadata、type 补入、当前帧替换、语义事件独立快照、合成终态无旧帧 ownership，以及空 batch 的源帧保留。
- 真实网关原生 body/SSE 精确返回、请求/响应摘要独立、usage 不变、失败尝试诊断不污染下一次、已交付未知事件 EOF 禁止重放。
- 历史日志自动迁移、新字段零值，以及现有完整终态/交付/缓存/预算/deadline 回归。

## 验证结果与审查

已执行 `go test ./...`、`go vet ./...`、`go test -race ./backend/gateway ./backend/protocol ./backend/shared`，全部通过；`git diff --check` 通过。新增协议用例对比四协议共 12 个跨协议组合的既有 wire；网关原生/跨协议、失败尝试隔离及交付后禁止重放用例通过，旧日志 DDL 自动迁移也通过。未连接真实供应商或真实 SDK，未重启正式服务。

Correctness 独立审查无可确认缺陷；Standards/Spec 审查无剩余问题，网关新增测试由评审者参与实现，其测试独立性降级，其余实现独立复核。仅以 SSE event 推断 type 的诊断会保守报告 type/event 元信息，诊断精细程度有限；不改变已成功转换的事件。

项目未配置复杂度/突变工具，本阶段未新增安装或运行；采用结构差异审查、功能回归和竞态检测验证，不提供 CRAP 或突变分数。
