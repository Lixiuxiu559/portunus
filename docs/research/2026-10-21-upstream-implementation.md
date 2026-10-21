# 上游接入实施与验证记录

依据：[整体方案](./2026-10-21-upstream-integration-design.md)。状态：A/B 和 C 功能已实施并完成本地验证；真实厂商账号验收尚未执行，D 专属适配器后续按需实施。未提交、未发布，未重启现有 Portunus。

## 测试设计

复用 Go testing、httptest、临时 SQLite、现有 gateway 测试和前端 node:test；不新增测试框架。

| 规则 | 输入或操作 | 必须观察到的结果 | 验证入口 |
|---|---|---|---|
| R1 旧连接兼容 | 原渠道包含自定义根路径；迁移后调用四协议 | URL/认证保持一致，模型与分组 ID 不变 | upstream/channel/gateway 测试 |
| R2 默认值快照 | 从预设创建，显式 auto_sync=false；再次编辑名称 | 地址正确展开，false 保留，无关更新不改变连接 | channel/API 测试 |
| R3 安全解析 | 完整操作 URL、URL 凭据、query、未知适配器 | 新连接拒绝；响应不包含密钥 | upstream/API 测试 |
| R4 原子保存 | 创建选择 m1/m1/m2；模型写入失败；重复导入 | 去重；失败回滚渠道；重复导入不重建模型 | 临时 SQLite 集成测试 |
| R5 草稿隔离 | 保存地址 A，草稿地址 B，省略 key；预览后取消 | 请求 B 使用旧 key，DB 地址仍 A；更新时不覆盖为脱敏 key | API 测试 |
| R6 同步语义 | 预览取消模型后保存；手动全量同步 | 本次仅保存所选；后续同步只新增全部发现项 | API/model 测试 |
| R7 验证独立 | 指定单模型测试；上游返回 401、非 JSON、流错误或延迟 | 阶段错误可识别，无渠道切换，不改熔断/正常调用统计；可取消 | httptest/网关测试 |
| R8 表单状态 | 改厂商、地址、协议；预览后再编辑 | 自定义值不被静默覆盖，旧预览/结果失效，保存携带模型选择 | 纯函数 node:test 与 UI 验证 |

真实厂商测试须有真实凭据；无凭据时仅记录官方资料与本地模拟验证，不标为真实调用通过。真实测试按钮须主动触发并显示输出预算。

## 阶段记录

- A 已完成：`backend/upstream` 集中预设目录和连接解析；渠道新增厂商来源、配置档、适配器、结构化选项；网关和同步共用连接入口。旧地址、密钥、模型与分组 ID 保持。模型发现支持 context、15 秒超时、4 MiB 上限和安全错误。
- B 已完成：创建/更新与所选模型同事务保存；幂等导入保留已有模型 ID；创建/编辑共享草稿 Hook 与三个小组件。编辑预览使用草稿地址并复用后端旧 Key，取消不写库。新 UI 默认关闭自动同步，避免保存所选模型后被定时全量导入；用户可明确开启，旧渠道原选择保持。
- C 功能已完成：主动单模型 JSON/SSE 验证复用原 relay；25 秒总期限、1–128 输出预算、1 MiB 响应上限，不路由切换、不普通重试、不写正常日志、不影响正式熔断。结果分阶段，不回显原始上游错误和密钥，验证原文也不写 stdout。目录包含通用四协议及六个厂商配置预设。
- C 外部验收待执行：未读取现有真实密钥，也未发送真实厂商生成请求。官方资料与模拟协议验证不等于账号可调用性。
- D 未进入本轮：Azure、AWS、Vertex 等特殊认证，以及其他候选厂商。

## 模块边界

`upstream` 只依赖协议模块；`channel` 保存连接快照，`model` 导入模型，管理 API 编排事务。现有网关增加连接入口和独立 probe，未增加厂商 switch 或第二套发送/重试循环。前端 `useChannelDraft` 管理草稿代次与取消，`ConnectionFields`、`ModelPicker`、`CallTestPanel` 分别负责连接、模型与主动验证。

`Options.Effective` 集中解释旧空选项，`UpdateRequest.OverlayConnection` 集中叠加连接补丁，语义等价的选项不改写旧快照，不触发历史地址重新校验。新增严格校验只作用于新建或真实连接变更。

## 实际验证

| 命令/方式 | 结果 |
|---|---|
| `GOCACHE=/private/tmp/portunus-go-cache go test ./... -count=1 -timeout=120s` | 全部通过，gateway 约 10 秒；使用临时 SQLite 与本机模拟上游 |
| `GOCACHE=/private/tmp/portunus-go-cache go vet ./...` | 通过 |
| `GOCACHE=/private/tmp/portunus-go-cache go build ./...` | 通过，不运行或替换已安装应用 |
| `cd web && pnpm test` | 110 项通过 |
| `cd web && pnpm build:renderer` | 通过；现有打包体积超过 500 kB 提示仍存在 |
| API 草稿到网关集成 | 临时上游验证 JSON/SSE、继承 Key、显式预算、单次 HTTP、无模型落库、正式地址不变 |
| ego-browser 独立静态 mock | 协议档切换、地址保留、模型勾选保存、手动模式、草稿失效、主动验证；1024×576 明暗主题无横向溢出，底部保存按钮可见 |
| `git diff --check` | 通过 |

第一次沙箱内网络测试因本地监听被拒绝，使用自动审批授权后重跑。一次早期运行使用了尚未修正的取消 fixture，因服务端未消费请求体卡在 `httptest.Server.Close`；仅停止该测试子进程，修正 fixture 后最新全量测试通过，没有停止现有 Portunus。

截图保存在临时目录 `/private/tmp/portunus-channel-edit-small-dark.png`、`/private/tmp/portunus-channel-edit-small-light.png`、`/private/tmp/portunus-channel-create-small-light.png`。模拟服务已关闭，浏览器任务空间已结束。未启动或操作正式应用。

## 独立评审与修复

固定基线：`6df6c613120fa2a04d123d8107151f7b2100813a`；包含已跟踪差异及本轮未跟踪源码。最终三轴由独立上下文执行：

- Correctness：发现旧渠道空选项导致自动同步关闭；早期定向评审另外发现已有地址清空时意外套用预设、验证丢失已存 thinking=false。三处均修正并补回归，定点复核通过。
- Standards：未发现硬性规范违规或证据充分的结构 smell。厂商数据集中、连接字段合并复用、UI 复用、probe 复用正式 relay 均核对。
- Spec：确认旧空选项问题，现已修正；C 的真实账号逐档验收保留未完成状态，未冒充真实验证通过。

额外集成修正：旧渠道按 ID 直接解析/预览时保留未修改的连接；诊断 URL 通过白名单去除凭据、query 和 fragment；主动取消的前端请求不弹“连接失败”；真实验证关闭客户端失败重发及 HTTP 重定向。

## 质量门

已执行定向与全量检查及独立 diff-review。复杂度度量等级为 `unavailable`：项目没有现成 gocyclo/gocognit 或 JS complexity 配置，本机也没有对应可执行工具，未安装依赖、未伪造 CRAP。结构风险通过独立 Standards 评审检查，不能替代量化审计。未发现现有突变测试配置，跳过 mutation-testing。

## 预设资料依据（2026-10-21）

只用于确认配置档与根地址，不证明真实模型权限或全部功能兼容：

- OpenAI：[API overview](https://developers.openai.com/api/reference/overview)。
- Anthropic：[API overview](https://platform.claude.com/docs/en/api/overview)。
- Gemini：[Models](https://ai.google.dev/api/models)。
- DeepSeek：[首次 API 调用](https://api-docs.deepseek.com/)。
- OpenRouter：[模型列表接口](https://openrouter.ai/docs/api/api-reference/models/get-models)。
- SiliconFlow：[快速开始](https://api-docs.siliconflow.cn/docs/userguide/quickstart)、[模型列表](https://api-docs.siliconflow.cn/docs/api/models-get)。
