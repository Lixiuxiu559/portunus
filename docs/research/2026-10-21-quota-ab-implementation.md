# 余额查询 A/B 实施与验证

依据：[整体方案](./2026-10-21-channel-quota-design.md)。本次只实施 A/B，C/D 不进入生产适配器目录。

## 测试设计

复用现有 Go testing、httptest、临时 SQLite、前端 node:test；无真实账号调用。

| 规则 | 输入/操作 | 预期与验证入口 |
|---|---|---|
| R1 数值语义 | 数字字符串、0、负余额、缺失、NaN/超长输入 | 解析 fixture 验证十进制与未知/无限分离，非法响应不伪成功 |
| R2 秘密隔离 | 继承渠道 Key、独立 Token、缺失/clear | API/服务验证归属、读取无明文；清除与保留语义一致 |
| R3 身份与并发 | 请求中改 Key/来源、删除或禁用；双击刷新 | 版本冲突不提交旧结果，同来源单请求，全局有界 |
| R4 旧值失效 | 成功→401→断网；成功→5xx→时间过期 | 状态测试不复活鉴权失效值，陈旧值不刷新成功时间 |
| R5 NewAPI | 原始 quota、显式500000/其他换算、账号Token+userId | 模拟HTTP校验路径/头，结果无默认USD，保留部署路径 |
| R6 任务边界 | 后台到期、429冷却、用户取消、应用退出 | 调度测试无自动重发、无僵尸任务，读取摘要不触网 |
| R7 UI与回归 | 配置/预览/保存/刷新、草稿变更、旧结果迟到 | node:test与隔离UI检查；旧渠道生成/分组链路测试继续通过 |

## 实施状态

A/B 已实施，分为共享契约、核心服务、适配器、界面四个边界；集成代码由管理 API 与主进程负责。C/D 未实施。

- A：来源/状态两张表、版本锁、安全 DTO、注册表、只读执行器、分层缓存状态、管理 API、描述符驱动表单和共用展示。
- B：四个生产适配器，预览/手动刷新/有界自动调度，十进制金额、原始额度与显式换算；继承渠道 Key 与独立凭据两种模式。
- 数据迁移仅创建新表；无余额结果参与路由、熔断、日志用量或计费。未改动当前开发版或正式版真实配置，未发送真实账号查询。

## 统一验证

2026-10-21 在最终集成代码上执行：

| 检查 | 结果 |
|---|---|
| `GOCACHE=/private/tmp/portunus-go-cache go test ./... -count=1 -timeout=120s` | 全部通过；现有 httptest 用例经授权运行本机临时端口 |
| `GOCACHE=/private/tmp/portunus-go-cache go vet ./...` | 通过 |
| `GOCACHE=/private/tmp/portunus-go-cache go build -o /private/tmp/portunus-quota-ab-check .` | 通过；Go版本元数据缓存写入受沙箱限制产生提示，最终退出码0 |
| `go test -race ./backend/quota ./backend/api -run 'TestQuota\|TestService\|TestExecute\|TestAdapter' -count=1 -timeout=90s` | 通过，重点覆盖服务并发、取消及API事务集成 |
| `cd web && pnpm test` | 119/119通过 |
| `cd web && pnpm run build:renderer` | 通过；保留现有大chunk体积提示 |
| `git diff --check` | 通过 |
| 隔离进程启动/退出 | 临时数据库、随机回环端口，读取4个适配器，SIGTERM后退出码0；未访问13061或真实数据 |

适配器单独执行过完整 `go test -race ./backend/quota`，通过。前端 Node 测试还会输出既有 module 类型提示，不影响通过结果。浏览器验收使用独立 mock 服务和模拟 Token，包括1024×576明暗主题、重复点击202、普通保存不刷新、隐藏页面停轮询、迟到预览隔离；详见[界面验证](./2026-10-21-quota-ui/README.md)。

## 评审与修复

以 `6df6c613120fa2a04d123d8107151f7b2100813a` 为代码基线，评审仅覆盖本轮 quota 与集成代码，不把既有上游接入改动混入。

- Correctness 独立评审发现跨适配器保留旧 Token 和一次读取失败后轮询停止。均已修复并定点复核：切适配器先清秘密；轮询每轮 finally 调度下一轮并保留隐藏/卸载保护。
- Spec 独立评审要求配置提交后真正取消旧查询、NewAPI 换算保留原值、重复来源提示、busy 202 与轮询建议。均已补齐；API新增在途查询时 Key 事务回滚不取消、成功修改/删除立即取消的集成测试。
- Standards 独立评审未发现确定硬性违规或需报告的结构风险。
- 未配置可用的量化复杂度与突变测试工具，本次未安装新工具，相关指标未验证。通过包依赖检查、模块边界评审、行为测试和 race 检查控制本轮风险，不将这些检查等同于量化审计通过。

## 实际限制

官方适配器依据研究记录中的官方接口和响应 fixture 验证，真实账户权限、私有 NewAPI 版本与站点换算需用户实际账号另验。OpenRouter 只接受独立 Management Key；NewAPI 只接受独立账户 Token 与 User ID。套餐、多窗口、Sub2API、OAuth、自定义脚本及余额告警保持后续阶段范围。
