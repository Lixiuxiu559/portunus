# 流式转发的超时与中断处理：new-API 与 cc-switch 的做法

> 研究时间：2026-09-06。对象：`/Users/lixiuxiu/development_tool/projects/new-API`（QuantumNous/new-api，Go + Gin）、`/Users/lixiuxiu/development_tool/projects/cc-switch`（Tauri，Rust + axum/reqwest）。
> 背景：Portunus 诊断发现上游 aiapi.lejurobot.com（new-api 站）在流开始后整 300s 发 RST_STREAM(INTERNAL_ERROR) 掐断长流，Portunus 侧流已提交无法 failover，只能向 Anthropic 客户端发流内 error 事件。本文调查同类项目如何应对「流式超时与中途断流」，只补这个切面——gateway 编排见 `new-api-gateway-flow.md`，failover/熔断/超时框架见 `cc-switch-retry.md`，协议转换见 `new-api-format-conversion.md`，流式协议本身见 `anthropic-messages-streaming.md` / `openai-api-streaming.md`。

---

## 1. 研究问题

1. 流式转发有没有**总时长限制**？（对照我们遇到的 300s RST）
2. 上游**中途断流/超时**时，给客户端发什么？（错误事件 / 伪正常收尾 / 静默截断）
3. 有没有**心跳/ping** 之类的流保护？
4. **客户端断开**如何感知、如何传导到上游？
5. 流已开始写回客户端后还能不能 failover？

---

## 2. new-API 的做法

### 2.1 超时体系：两层，默认都偏「不管」

| 层 | 配置 | 默认 | 语义 | 出处 |
|---|---|---|---|---|
| 客户端总时长 | env `RELAY_TIMEOUT` | **0 = 关闭** | >0 时设到 `http.Client.Timeout`——对**流式同样是总时长限制**（Go 语义：覆盖连接到读完 body 全程），会把长流掐断 | `common/init.go:104`、`service/http_client.go:47-58` |
| 流内空闲 | env `STREAMING_TIMEOUT` | **300 秒** | 主 goroutine 的 ticker，scanner **每收到一行就 `ticker.Reset`**（stream_scanner.go:238）——是「两次数据之间的静默」超时，**不是总时长** | `common/init.go:132`、`relay/helper/stream_scanner.go:53,238,286-287` |

- 无首包专用超时、无响应头超时兜底（`RelayTimeout=0` 时 Transport 也不设 `ResponseHeaderTimeout`，`service/http_client.go:37-58`）。
- **与 Portunus 300s RST 的关系（重要辨析）**：lejurobot 站跑的就是 new-api，`STREAMING_TIMEOUT` 默认 300 与症状数值巧合，但**机制对不上**：它是空闲型超时，要求掐断前静默 ≥300s；而 Portunus 侧看门狗（idle=120s，`backend/gateway/stream.go:46`）未触发，说明掐断前最后一段静默 <120s——上游一直有数据，300s 整被 RST 是**总时长限制**（接入层反代/LB），不是 new-api 的 STREAMING_TIMEOUT。此段推断基于两侧机制比对，标注为推断。

### 2.2 流式转发实现：三 goroutine 管线

`StreamScannerHandler`（`relay/helper/stream_scanner.go:37-299`）是所有 adaptor 共用的流处理核心：

```
scanner goroutine（读上游 body，逐行筛 data:）
  → dataChan (buffer 10)
  → dataHandler goroutine（协议转换 + 写客户端，writeMutex 串行）
主 goroutine select { ticker.C（空闲超时）| stopChan | c.Request.Context().Done() }
ping goroutine（可选，下游保活）
```

- scanner buffer 初始 64KB、上限默认 **64MB**（stream_scanner.go:25-26），Portunus 是 1MB 上限（`backend/gateway/stream.go:142`）——new-api 为超长 SSE 行（如整段 base64）留了更大余量。
- 各 adaptor 复用：`relay/channel/openai/relay-openai.go:129`、`relay/channel/claude/relay-claude.go:878`、gemini/xai/baidu/dify 等同。

### 2.3 断流/超时后给客户端发什么：**静默截断 + 伪正常收尾（不发错误事件）**

这是 new-API 与 Portunus 分歧最大的设计。`StreamScannerHandler` 把结束原因记进 `StreamStatus`（九类：done / timeout / client_gone / scanner_error / handler_stop / eof / panic / ping_fail，`relay/common/stream_status.go:10-22`），**但异常原因只进日志**（stream_scanner.go:294-298 `LogError("stream ended: ...")`），不外传错误。上层 handler 无视 StreamStatus 一律走正常收尾：

- OpenAI 客户端：`OaiStreamHandler` 在 scanner 返回后补发最后一个 chunk（含 finish_reason）→ 补 usage chunk → `Done()`（发 `[DONE]`），然后 **`return usage, nil`**——对外是成功（`relay/channel/openai/relay-openai.go:106-196`，尤其 195 行）。
- Claude 客户端：`ClaudeStreamHandler` 同构——只有**转换错误**（`sr.Stop(err)`）才返回错误；上游断流/超时/客户端断开都 `HandleStreamFinalResponse` 补收尾后 `return usage, nil`（`relay/channel/claude/relay-claude.go:869-893`）。对 RelayFormatClaude 客户端，`HandleFinalResponse` 会把上游最后一个 openai chunk 转成 claude 的 message_delta + message_stop 事件写回（`relay/channel/openai/helper.go:197-222`，`info.ClaudeConvertInfo.Done = true`），客户端**以为流正常完成**。
- usage 缺失时用 `ResponseText2Usage` 按已收文本估算兜底，debug 日志明说 `maybe upstream error`（`relay-claude.go:818-841`）。

**后果（推断，基于上述代码）**：上游 300s RST 掐断 new-api↔真上游的流时，new-api 会给客户端伪完整收尾，截断的内容客户端不知情、也不会重试——这比 Portunus「发流内 error 事件让客户端可重试」更不诚实，但换来客户端 SDK 永远正常退出、不会出现「卡住 → 重试横幅」。

### 2.4 心跳 ping：独有的下游保活（两层）

ping 内容是 SSE 注释行 `: PING\n\n`（`relay/helper/common.go:94-107`）——SSE 规范里 `:` 开头是注释，客户端 SDK 忽略，但能刷新中间层反代的空闲计时。开关 `PingIntervalEnabled` 默认 **false**、间隔默认 60s（`setting/operation_setting/general_setting.go:28-29`）；代码内 fallback 10s（`stream_scanner.go:27`）。

两层部署点：
1. **发上游请求的当场**：`doRequest` 里流式请求先 `SetEventStreamHeaders(c)` 提交响应头、再起 ping goroutine，然后才 `client.Do`——**等上游首包期间就在向客户端发 ping**，防止「上游思考太久、客户端/中间层等不到任何字节」被掐。硬顶 120 分钟（`relay/channel/api_request.go:505-530, 385-449`）。
2. **流转发期间**：`StreamScannerHandler` 内的 pingTicker（stream_scanner.go:64-73, 120-183），同样带 10s 写超时保护与 30 分钟硬顶。

注意 ping 是**下游保活**：防的是「客户端 ↔ new-api ↔ 中间反代」这段被空闲超时掐；对「上游 ↔ 真渠道」无作用，更防不了总时长型掐断（总时长不看流量）。

### 2.5 客户端断开：循环内检测，不取消上游请求

- 感知点：scanner 循环每行 select `c.Request.Context().Done()`（stream_scanner.go:232-234）、主循环（290-291）、ping goroutine（174-176）；写路径三函数也先查 `c.Request.Context().Err()`（`helper/common.go:28-30, 86-88, 99-101`）。
- 命中后 `StreamEndReasonClientGone` 停止转发。但**上游 HTTP 请求没有绑客户端 ctx**：`http.NewRequest`（非 `NewRequestWithContext`，`relay/channel/api_request.go:298`）——客户端断开不会取消上游请求，靠 scanner 停读 + `defer resp.Body.Close()` 释放连接。
- 对比 Portunus：看门狗 ctx 派生自请求 ctx（`backend/gateway/stream.go:53-56`），客户端断开会沿 ctx 链取消整条读取链路，更及时。

### 2.6 failover 与流提交：**没有 committed 概念**

重试循环在 helper 外层（`controller/relay.go:189-235`），`shouldRetry` 按状态码/错误类型判定（relay.go:318-347）。但：
- 断流/超时类失败因 handler `return usage, nil` **根本不进重试**（见 2.3）。
- 唯一能触发重试的是**转换错误**（如上游响应解析失败，`sr.Stop(err)` → `return nil, err`）——此时流可能已经写了一半，new-api 没有「已提交不能换家」的标记，重试会让客户端收到两段拼接的流（第二段的 message_start 会再来一遍）。**这是隐患，推断**：转换错误通常发生在流早期（首事件解析失败），实际暴露概率低。

### 2.7 小结：new-API 有没有流总时长限制？

- **默认没有**（RELAY_TIMEOUT=0 → client 无 Timeout；STREAMING_TIMEOUT 是空闲型）。
- 管理员若设 `RELAY_TIMEOUT>0`，`http.Client.Timeout` 就是流式总时长限制，长流必被掐（`service/http_client.go:53-58`）——这个配置对长流是危险项。
- 站点看到的「整 300s 掐流」若出自 new-api 部署，只能是它前面的反代/LB 总时长限制，或它设了 RELAY_TIMEOUT=300（后者错误文案是 Go 的 Client.Timeout 提示，与我们捕获的 h2 RST 文案不同，已排除）。

---

## 3. cc-switch 的做法

定位确认：cc-switch 内置本地 HTTP 代理做转发（axum，`src-tauri/src/proxy/`），failover 框架、三个应用级超时、熔断器已在 `cc-switch-retry.md` §2/§6/§7/§8 详述，此处只补流中断切面。

### 3.1 流式转发：纯透传，不注入任何帧

`create_logged_passthrough_stream`（`src-tauri/src/proxy/response_processor.rs:683-737+`）：字节级透传，只**旁路**解析 SSE 事件（为 usage 收集与调试日志，`inspect_sse_events` 分支），不修改、不注入、不补帧。与 new-api 的「解析-转换-重发」和 Portunus 的「逐事件转换」相比最简单，也因此不支持跨协议。

### 3.2 中断行为：超时/断流 = 直接断，无错误帧

- 首字节超时与静默期超时在同一个透传流里：`is_first_chunk` 前用 first_byte_timeout，之后用 idle_timeout（response_processor.rs:706-719）；到点 `yield Err(io::Error::other("流式响应{首字节|静默期}超时"))` 后 `break`（response_processor.rs:722-734）——向 axum 响应流投递错误即**中止连接**，客户端只看到断流，SSE 里没有任何 error 事件。
- 对比 Portunus：对 Anthropic 客户端 Portunus 会先发 `{"type":"error",...}` 终止事件再断（`backend/gateway/stream.go:214-228`），cc-switch 对所有客户端一律裸断。
- 上游中途 RST/断连：透传流自然结束/报错，同样裸断。cc-switch **没有任何「上游 300s 总时长 RST」的防御**——流式请求虽把总超时放大到 24h（`forwarder.rs:2266-2269`），但那只是让自己别误杀，上游掐了照样死，且首包后不可 failover（`cc-switch-retry.md` §7 已述）。

### 3.3 无 ping/心跳

`response_processor.rs` 与转发路径 grep `ping|heartbeat|PING` 无命中（仅连通性检查 `stream_check.rs` 与需求无关）。cc-switch 完全依赖客户端自己保活。

### 3.4 客户端断开：未实现

`ErrorCategory::ClientAbort`（`src-tauri/src/proxy/error.rs:189-192`）带 `#[allow(dead_code)]`，全仓无构造点——是预留枚举。客户端断开后透传流继续读上游，直到 hyper 发现写端失败才终止。比 new-api（循环内显式检测 client_gone）还要被动。

---

## 4. 与 Portunus 现状的对比

| 维度 | Portunus | new-API | cc-switch |
|---|---|---|---|
| 空闲（静默）超时 | 看门狗 120s（首包 60s），超时掐断并归因 | STREAMING_TIMEOUT 300s ticker，只记因不掐上游连接语义 | idle_timeout 120s（可 0 禁用），yield io::Error 断流 |
| 首包专用超时 | 有（60s，响应头后计时） | 无（靠 ping 兜住客户端侧等待） | 有（60s，等首个 chunk，可 failover） |
| 总时长限制 | 无（刻意不设 Client.Timeout） | 默认无；RELAY_TIMEOUT>0 时**会掐长流**（危险项） | 流式 24h（变相无） |
| 断流后对客户端 | **Anthropic 发流内 error 事件**（overloaded_error/api_error）再断；OpenAI 客户端裸断 | **伪正常收尾**：补 finish_reason/usage/[DONE] 或 message_delta+message_stop，客户端以为完整 | 一律裸断，无任何帧 |
| 客户端可否感知/重试 | Claude Code 可重试（重试横幅/降级非流式） | 不可感知，静默丢内容 | 不可感知 |
| 下游 ping 保活 | 无 | **有**：`: PING` 注释行，两层（首包等待期 + 转发期），默认关 | 无 |
| 客户端断开感知 | ctx 链传播，取消整条读取链路 | 循环/写路径显式检测（client_gone），但不取消上游请求 | **未实现**（dead_code 枚举） |
| 已提交后 failover | 明确禁止（streamCommittedError） | 无 committed 概念；转换错误重试有流拼接隐患 | 首包后离开 failover 循环，等效禁止 |
| 归因落库 | err_kind 八类 + err_msg | StreamStatus 九类 EndReason 仅日志 | ErrorCategory 分类 + 日志 |

### 可借鉴点（按成本收益排序）

1. **new-api 的下游 ping（低成本，建议做）**：Portunus 首包等待期（上游模型思考几分钟）没有任何字节写给客户端——若客户端与 Portunus 之间还有反代/企业代理，空闲超时会误杀。对 Anthropic 客户端发 `event: ping`（Anthropic 官方流有此事件类型，见 `anthropic-messages-streaming.md`），对 OpenAI 客户端发 `: PING` 注释行，均不影响 SDK。它能防「我们与客户端之间」被掐，但**防不了上游 300s 总时长 RST**（那种掐法不看流量）。
2. **StreamStatus 式九类 EndReason**：Portunus err_kind 已覆盖同等信息，无需改。
3. **scanner buffer 上限**：new-api 默认 64MB vs Portunus 1MB；若未来遇到超长 SSE 行（大 base64 图）可再放宽，当前无实际案例，不动。
4. **反面教材**：new-api 的伪正常收尾（截断内容客户端不知情）与 RELAY_TIMEOUT 掐长流，Portunus 的「流内 error + 不设总超时」选择更正确，保持。

---

## 5. 参考文件清单

new-API：
- `relay/helper/stream_scanner.go`（流管线、ticker 空闲超时、ping、客户端断开感知、StreamStatus 归因）
- `relay/common/stream_status.go`（九类 EndReason）
- `relay/helper/common.go`（PingData `: PING`、写路径 ctx 检查、SSE 头）
- `relay/channel/api_request.go`（doRequest 提交响应头 + 首包期 ping、http.NewRequest 不绑客户端 ctx）
- `service/http_client.go`（RELAY_TIMEOUT → Client.Timeout）
- `common/init.go:104,132`（两个 env 默认值：0 / 300）
- `setting/operation_setting/general_setting.go:28-29`（ping 默认关/60s）
- `relay/channel/openai/relay-openai.go:106-196`（断流后伪收尾 return nil）
- `relay/channel/openai/helper.go:197-222`（HandleFinalResponse → claude 客户端补 message_delta/stop）
- `relay/channel/claude/relay-claude.go:800-893`（HandleStreamFinalResponse、ClaudeStreamHandler）
- `controller/relay.go:189-235, 318-347`（重试循环、shouldRetry）

cc-switch：
- `src-tauri/src/proxy/response_processor.rs:683-760`（透传流、首字节/静默超时 yield io::Error）
- `src-tauri/src/proxy/error.rs:180-200`（ErrorCategory 含 dead_code 的 ClientAbort）
- `src-tauri/src/proxy/forwarder.rs:2266-2290`（流式 24h 总超时、首包 timeout）
- 其余 failover/熔断/超时框架见 `cc-switch-retry.md` §11 索引。
