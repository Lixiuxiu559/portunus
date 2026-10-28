# 网关与协议转换

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | `/v1` 鉴权、请求编排、协议接入、重试/切换与资源边界 |
| 事实状态 | 部分核验 |
| 核验基线 | `9bf6dbb`；2026-10-27 当前工作区 |
| 核验时间 | 2026-10-27 |

## 用途与边界

`gateway` 把客户端请求路由到一个或多个上游目标，并在需要时调用 `protocol` 做协议语义转换。`upstream` 从已保存渠道生成连接 DTO，`protocol.Upstream` 提供对话 URL、鉴权头和模型发现；`gateway` 负责请求生命周期，不负责厂商预设或余额查询。

对外路由为：

- `GET /v1/models`
- `POST /v1/chat/completions`
- `POST /v1/responses`
- `POST /v1/messages`

客户端令牌与上游 Key 不混用。分组名称是路由入口，模型名称中的兼容后缀等处理属于网关的分组/意图知识，具体协议字段表达属于 `protocol`。

## 主要流程

```text
API Key 鉴权 → 请求 ID 与诊断上下文 → 有界读取及 JSON 深度检查
→ 提取 model/stream → 分组查询 → router.Resolve
→ 候选：请求控制检查 → 429 冷却 → 熔断/半开状态
→ 请求转换 → 可取消退避 → 再查冷却 → 渠道并发与半开租约
→ 尝试预算 → http.Client.Do → 响应/流处理与协议回转
→ 写日志、尝试及切换诊断
```

### 候选与失败处置

- manual 只有一个候选；failover 按固定主备顺序遍历，失败是否可切换由失败分类和交付状态决定。
- 同目标普通重试与目标切换是外层编排；Chat 预算字段兼容重试发生在尚未交付响应时，属于同一目标的一次额外显式 HTTP 尝试。
- 请求控制包含总 HTTP 尝试预算、首内容期限、非流式累计期限和可取消退避。实际 `Do` 前还需再次检查冷却、容量和租约。
- 429 冷却可按模型或渠道作用域配置；它不同于故障熔断。半开探测租约限制真实恢复探测，渠道并发限制当前进程的在途 relay。
- 已开始向客户端交付、可能已执行原生操作、客户端取消或资源硬停止等路径不能按普通失败重放。流式中途断流不能承诺无损换渠道。

### HTTP 与转换边界

`NewHTTPClient` 设置连接和响应头超时，不设置会截断长流的总 `Client.Timeout`；实际请求预算由请求控制另行管理。`doRequest` 在实际 `Client.Do` 前完成控制检查，尝试记录只覆盖这个显式调用边界。

同协议路径保留原生载荷，跨协议按已实现的语义映射转换。类型化流、SSE 终态、工具/思考兼容和资源限额的完整细节见 `backend/protocol`、`backend/gateway/stream*.go` 及 `ARCHITECTURE.md` 所列专项说明。本轮未逐字段复核这些转换，不能将本页视为完整兼容性矩阵。

### 观测边界

`Deps.LogWrite`、`AttemptWrite`、`RoutingWrite` 在生产由 `main` 注入同步落库实现。观测写入失败不能成为再次调用上游的理由。尝试与诊断的准确口径见[调用观测](../observability/README.md)。

## 阅读导航

- [分组如何产生有序候选](../resources/README.md)
- [调用日志、尝试和诊断的区别](../observability/README.md)
- [后端如何装配依赖](../runtime/README.md)
- [协议和故障转移的实现总览](../../../../ARCHITECTURE.md)

## 依据与核验

核对 `backend/gateway/gateway.go`、`dep.go`、`relay.go`、`upstream.go`、`request_control.go`、`backend/protocol/upstream.go`、`backend/shared/config.go`、`backend/shared/upstream_attempt.go` 及 `ARCHITECTURE.md`。显式 Do 的位置、候选前置检查和三类写入依赖已对照源码。

未执行真实模型调用、流式/工具闭环、故障注入、资源压力或跨协议全量测试。历史真实上游验收不代表本轮重新验证，也不代表所有模型能力等价。

## 待确认项

1. 完整协议字段、工具、思考、原生扩展和结束原因兼容性需要查对应专项测试与报告，本版未建立完整矩阵。
2. 当前健康与并发控制为进程内状态；多实例部署的一致性和公网部署安全边界未在本版核实。
