# 核心概念与跨模块规则

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | 渠道、模型、分组、两类凭据、路由策略与调用观测的共同语义 |
| 事实状态 | 已核验 |
| 核验基线 | `b44d75a788f8aa5ef0ad024273d4bf5dd8c95c7e`（`dev`）；2026-10-28 源码 |
| 核验时间 | 2026-10-28 |

## 术语与概念

### 上游资源与稳定调用入口

| 概念 | 当前实现与修改影响 |
|---|---|
| 渠道 Channel | 一个上游账户的连接单元，保存协议、API 根地址、凭据及连接级控制配置。厂商身份与协议分离，同一厂商预设可以产生独立渠道。修改地址或凭据会影响经该连接建立的模型发现和生成调用。 |
| 模型 Model | 归属某个渠道的上游资源目录条目。同名模型在不同渠道仍是不同资源；目录条目可由发现导入或手动添加，不证明实际生成能力。 |
| 分组 Group | 跨渠道引用模型成员，其名称就是客户端请求中的 `model`。删除或改名会改变调用入口；更换有效成员不要求客户端修改分组名。 |
| 分组成员 GroupItem | 分组对某个模型的引用，故障转移时使用 `priority`，相同优先级按成员 ID 升序。成员不是另一个独立客户端模型。 |
| 模型列表 | `GET /v1/models` 只公布有成员的分组，跳过空组。非空只表示配置存在，不表示路由完整、上游健康或调用已验证。 |

依据：`backend/group/group.go` 的实体与策略、`backend/model/model.go`、`backend/gateway/gateway.go` 的 `listModels`。渠道接入和删除引用规则见[资源模块](modules/resources/README.md)。

### 路由策略与调用状态

`backend/router/router.go` 的 `Resolve` 先拒绝无成员分组：

- `manual` 只返回 ID 等于 `ActiveItemID` 的成员；没有有效激活项返回 `ErrNoActiveItem`，不会因失败自动换成员。
- `failover` 返回按 `priority`、成员 ID 排序的候选序列；是否继续到下一个目标由网关的失败条件和交付状态共同决定，不是所有错误都无条件切换。
- 两种策略最终都解析为具体“模型 + 渠道”。缺少模型或渠道会在解析时报错，而不是把分组名直接交给上游。
- 启动迁移会把历史 `round_robin` 分组转为 `failover`，当前创建和更新不再接受旧策略。熔断、429 冷却、半开探测及并发准入是运行态控制，不是第三种用户路由策略。

配置不完整、运行态不可用和最近调用结果是不同概念。没有真实调用证据时，不应从成员存在、模型同步成功或 `/v1/models` 可见推导“在线”。详见[网关模块](modules/gateway/README.md)。

### 凭据与访问边界

- **上游密钥**保存在渠道或余额来源中，用于访问供应商，不是 Portunus 客户端凭据。
- **Portunus API Key**是命名的服务令牌，用于 `/v1`。网关兼容 `Authorization: Bearer` 与 `x-api-key`，鉴权后将稳定 Key ID 和名称快照放入上下文，供日志归属使用。禁用、重置与软删除的语义见[运行模块](modules/runtime/README.md)。
- **管理访问**不接受服务令牌作为替代认证。`/api` 中间件检查真实 TCP 对端为回环地址，同时校验本机 Host 和允许的 Origin；转发头不授予管理权限。`file://` 桌面的 `Origin: null` 还依赖 `X-Portunus-Local: 1`。本机反向代理仍在管理信任边界内，不能向远端公开 `/api`（`backend/api/local_access.go`）。

### 用量、日志和请求观测

`shared.Log`、`shared.UpstreamAttempt` 和 `shared.RoutingTrace` 是三类不同记录：

1. **Log**：一轮 relay 的持久化日志，保留 token、缓存用量、渠道/模型、结果和错误等。同一 `request_id` 可对应普通重试或目标切换的多条日志，旧统计按日志行聚合，不计算费用。
2. **UpstreamAttempt**：一次应用显式 `http.Client.Do`，包括兼容重试，不重复记 token。它不证明 Transport 的物理发送次数、上游确实收到请求或模型最终完成。
3. **RoutingTrace**：选路、准入和停止原因等诊断，不是客户端完整接收或跨表采集完整性凭证。

`request_id` 关联归并把有相同 ID 的已持久化日志合成一个检索项；缺少 ID 的历史日志各自独立。筛选命中其中一条即可选中关联项，但摘要取该 ID 的全部日志，`latest_log` 按最大写入 ID 选取。关联项数、日志行数和已采集 Do 数都不能称为准确客户端请求数或最终成功率；空尝试列表也不能证明零调用。详见[观测模块](modules/observability/README.md)。

### 协议与用量边界

客户端协议与渠道上游协议独立。同协议路径保留原生请求/响应，跨协议路径按已有字段语义转换；不承诺任意工具、思考、缓存或供应商扩展等价。流式内容开始向客户端交付后，不能假设可以无损重放或自动切换。托管工具由支持的上游执行，Portunus 不在网关侧代跑搜索。

系统保留上游报告的 token 与缓存 usage；缺失 usage 不等于零消耗。真实生成和额度查询都可能受上游账户与计费规则约束，Wiki 的源码核验不触发这些操作。

## 依据与核验

本页核对 `backend/group`、`backend/router`、`backend/gateway/gateway.go`、`backend/shared/log.go`、`upstream_attempt.go`、`log_requests.go`、`backend/api/local_access.go`，以及 `docs/product/PRODUCT.md` 的术语约定。入口、流程和条件以代码为准，产品文档用于解释对象职责与范围。

本次只进行静态源码核对，未执行真实调用、桌面启动或数据迁移演练。

## 待确认项

无。
