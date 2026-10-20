# 取消模型计价：实施与本地验证

日期：2026-10-20。依据：用户确认移除模型价格配置和费用展示，保留 token 用量、调用诊断及历史数据库字段，并同步文档。

## 实际变更

- 上游模型页移除价格、币种和价格编辑器；新增模型仅填写渠道及上游模型 ID，保留搜索、分页和删除。
- 调用记录移除费用列和 USD/CNY 费用统计；保留记录数、非缓存输入、输出、缓存读取/写入、耗时、首包、错误、request_id 与分组筛选。
- 模型创建、更新及响应 DTO 不再提供价格/币种；旧客户端额外发送这些 JSON 字段会被忽略，不能修改历史价格。
- 删除内置默认价格表及套用逻辑，停止新调用费用计算和费用统计聚合。缓存读写汇总独立提供，不与非缓存输入重复计算。
- Model 与 Log 的历史价格、货币和费用列保留原 GORM 定义，仅从 JSON 隐藏；不删除、不清零、不回填既有数据。新日志旧 cost 列的零值表示未计算，不代表上游免费。
- README、产品定义、架构、开发说明及宣传底稿同步为用量与诊断；README 撤下旧计价截图。宣传片文字源已调整，原音轨、截图和成片未重新制作，工程 README 已明确其历史状态。

## 关键场景与结果

| 需求 | 验证入口 | 实际结果 |
|---|---|---|
| 新建不套默认价 | `backend/model/create_test.go`，gpt-4o / deepseek-v4-pro / custom | 四类历史价格列保持零值，通过 |
| 同步不覆盖旧价格 | `backend/model/sync_test.go`，已有非零价格模型再次同步 | 旧值保留，新模型不套价，通过 |
| 旧管理请求不能改价 | `backend/api/model_retirement_test.go` | 名称/渠道更新保留原价格，创建/读取/列表不返回旧字段，通过 |
| 新日志不计费且诊断完整 | `TestLogCallRetainsUsageWithoutBilling`，带历史高单价的目标 | 不继承币种、不计算费用；四类用量、request_id、错误、耗时、协议与桥接遥测均保留，通过 |
| 统计不暴露费用 | `backend/shared/log_query_test.go` 与 API 测试 | 无费用响应/聚合，缓存 SUM 遵从分组等筛选，历史费用原值不变，通过 |
| 旧 models 表兼容 | `TestLegacyModelSchemaPreservesRetiredColumns`，手写旧 DDL 与非零 CNY 单价 | 正常 AutoMigrate 后旧列、单价、时间、行数及唯一索引保留，通过 |
| 旧 logs 表兼容 | `TestLegacyLogSchemaPreservesRetiredColumns`，手写旧 DDL 与 cost=18.75 | 生产 InitLogDB 后旧费用、用量、请求/错误、时间与行数保留，并补齐新诊断列，通过 |
| 模型页面不再显示旧价格 | 浏览器读取带 currency/input_price 等字段的合成模型 | 无价格/币种/费用展示，无价格编辑入口，通过 |
| 新增模型请求真实收敛 | 浏览器选择合成渠道并创建 custom-usage-model | 只提交一次 `{channel_id:1,name:"custom-usage-model"}`，通过 |
| 调用记录保留用量和诊断 | 浏览器读取带 cost=123.456789 的合成失败记录 | 不显示旧费用；显示非缓存输入100、输出50、缓存读40/写5、流中断、请求ID和耗时，通过 |

旧表验证仅覆盖上述两个明确构造的 SQLite DDL fixture，不推广为任意历史版本升级保证。本轮没有读写用户真实数据库。

## 执行结果

- `GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test ./... -count=1`：全量通过。
- 随后新增的两项手写旧 DDL 测试：定向运行通过；未因仅新增独立测试重复运行整套 HTTP mock。
- `go vet ./...`、`go build -buildvcs=false ./...`（同上本地环境变量）：通过。
- `cd web && pnpm test`：74 项通过。
- `cd web && pnpm run build:renderer`：通过，仍有大于 500 kB 的构建体积提示。
- 独立 Correctness / Standards / Spec 三轴评审：均 0 项实质问题，主流程抽查旧 DDL、接口及浏览器请求。
- `git diff --check` 与现役文档本地链接检查：通过。

没有配置突变测试工具，未新增依赖；本轮主要移除计价分支并保留既有存储定义，未新增复杂状态机，未生成专门复杂度指标。

浏览器使用只监听本机的内存 fixture 和生产前端构建，未使用真实密钥、未执行生成示例，任务完成后已关闭浏览器空间并停止测试服务。一次浏览器命令的自动权限审查超时，没有执行；按工具允许重试后完成检查。

本轮未发起收费上游调用，未重新打包发布或替换当前运行应用。现有渠道、分组、访问令牌和监听配置保持。
