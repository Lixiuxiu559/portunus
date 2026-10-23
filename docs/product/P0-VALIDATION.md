# P0 本地验证记录

日期：2026-10-20。范围为 [产品定义](./PRODUCT.md) 第 0 节的 P0：页面与文档定位统一、管理面访问限制、保留现有配置。

## 验证规则与结果

| 规则 / 场景 | 入口与数据 | 预期与实际结果 |
|---|---|---|
| 管理面仅本机 | Go `TestManagementAccessBoundary`，IPv4/IPv6/mapped 回环、LAN、公网、非法对端 | 回环通过；其余 403，通过 |
| 不能伪造管理来源 | 同一用例，伪造 X-Forwarded-For / X-Real-IP / Forwarded、外部 Host / Origin | 不影响真实对端判断，非法来源 403，通过 |
| 所有管理路由受保护 | `TestManagementRemoteCannotReachAnyRegisteredHandler`，遍历注册路由，远端带服务令牌和管理头 | 均在 handler 前拒绝，通过 |
| LAN 服务接口保留 | `TestManagementBoundaryDoesNotAffectGatewayAuth`，远端 `/v1/chat/completions` | 无令牌 401；原有效令牌通过鉴权，非法请求体进入业务校验 400；不发真实生成请求，通过 |
| 开发代理不能代远端获取管理权限 | Node `dev-management-access.test.js` | 代理前拒绝远端 `/api`，服务路径不受管理 guard 影响，2 项通过 |
| Electron 管理兼容 | 独立隐藏 BrowserWindow，加载临时 file:// 页面，自定义管理头，请求临时 Go API | GET ping 200；POST 模型预览缺参数返回业务校验 400，无 CORS 网络错误，通过 |
| 分组配置状态不冒充健康 | `groupService.test.js`，空组、手动项失效、有效手动项、故障转移及废弃/未知策略 | 配置完整仍显示未验证，缺失项显示配置不完整，通过 |
| 分组跨页不丢标识 | `groupService.test.js` 与 `logQuery.test.js`，中文、空格、&、?、# 等 | 接入/记录链接和 group_name 筛选保留完整名称，通过 |
| 不存在分组不自动换服务 | `serviceAccess.test.js`，显式 deleted / 空名 / 原选择失效 | 显式无效链接保持未选；无显式链接时才允许默认分组，通过 |
| 三类 API 示例 | `serviceAccess.test.js`，stream true/false、特殊引号密钥/分组 | Chat/Messages=max_tokens，Responses=max_output_tokens，预算 128，保留流式及认证，4 项通过 |
| 分组首页及入口操作 | 浏览器打开生产构建，内存合成渠道与两个分组 | 默认分组页；六导航顺序正确；手动未选提示；接入链接预选特殊字符分组，通过 |
| 改名先确认 | 浏览器编辑合成 `coding & 快` 为 `coding-renamed` | 点击保存出现影响说明，此时无写请求；取消可返回；再次确认只提交一条 PUT，通过 |
| 关联调用记录 | 浏览器从合成分组点击查看调用 | 输入框与 `/api/logs`、`/api/logs/stat` 都包含正确 group_name，通过 |
| 设置职责迁移 | 浏览器设置页 | 令牌与监听范围改为服务接入跳转，同步和更新仍保留，通过 |

浏览器使用独立本机静态服务和内存 fixture，未读取实际数据库。上述成功的页面检查使用生产构建；尾部文案统一和卡片恢复原高度之后再次构建通过。后续浏览器空间已不可恢复，因此本轮没有完成首用空态与流式复选框的完整浏览器点击回归；分组状态及两种流式示例由 Node 测试覆盖。没有将部分浏览器检查写成全面端到端测试通过。

## 执行命令

- `GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test ./... -count=1`：全量通过，包括本地 HTTP mock。
- `GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go vet ./...`：通过。
- `GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go build -buildvcs=false ./...`：通过。
- `cd web && pnpm test`：74 项通过。
- `cd web && pnpm run build:renderer`：通过；仍有构建产物超过 500 kB 的体积提示，不影响构建。
- `git diff --check`：通过。

独立评审固定参考提交 `999bc0b410363e814fda414546c7e75579f1f8f0`，结合已批准产品范围区分用户已有工作区改动。Correctness、Standards、Spec 三轴均未报告实质问题；安全实现者也参与了 Correctness 轴，其独立性有限，主流程另抽查了实际对端、Host、Origin、开发代理以及对应测试。纯文案收尾不改变安全规则。

没有发现项目已配置突变测试工具，未新增依赖；本轮不生成 CRAP 等复杂度指标。安全逻辑拆成小函数，并以正常、拒绝和绕过场景测试及代码评审验证。

## 配置与部署边界

- 未增加数据库迁移、配置必填字段或自动令牌重置；现有渠道、分组标识、凭据和 local/lan 配置继续使用。
- 管理接口改为仅本机，远端管理访问是此次有意关闭的行为；远端服务调用仍使用既有令牌。
- 本机进程在信任边界内。自行配置的反向代理不得将本机 `/api` 转发给远端，项目自带开发代理已加前置限制。
- 本轮未调用真实收费上游，未打包发布或替换当前运行应用。源码变更需要重新构建/启动后才会进入实际服务。
- 独立探针和内存测试服务均不使用真实凭据；收尾时停止临时服务。浏览器工具提示可升级，本轮未升级。
