# 请求关联归并与上游尝试链界面验证

日期：2026-10-21。基线：`e532965`（上一项独立尝试记录已提交）。本报告随请求关联归并与尝试链界面改动一同提交。

## 当前交付

调用记录保留默认“日志记录”视图，新增“按请求归并”视图，归并由服务端执行，避免只合并当前页导致同请求被拆开。非空 request_id 相同的持久化日志关联到一起；空/NULL 请求 ID 的历史日志每行独立，key 使用 request:/log: 前缀避免碰撞。

新增 `GET /api/logs/requests`，兼容旧日志筛选与分页，返回 `{total,data:[{key,request_id,log_count,latest_log,first_at,last_at}]}`。任一日志命中筛选即可选中关联项，但摘要取该请求的全部日志。例如筛选 A 渠道的失败日志，仍能看到后续 B 渠道成功的最近日志。total 只是关联项数，包含独立历史行，不等于准确客户端请求数。原 `/api/logs/stat` 及四张统计卡仍按匹配日志行汇总。

摘要选择最大持久化 ID 的 latest_log，时间/渠道/模型/结果全部来自这条日志，不推断最终客户端结果。first_at/last_at 表示关联日志时间范围，可与写入顺序不同。后端在同一读事务中执行分组、分页及批量明细查询，非空页固定 3 次 SELECT，无逐项查询或内存全量分组；本轮没有 schema 变更。

单条日志及关联项详情都可读取上一步的独立尝试记录，按序展示首次调用、预算兼容重试、同目标重试、切换目标，以及渠道、模型、协议、预算字段名、状态、耗时和可复制错误。每页 20 条，支持翻页、刷新与失败重试。

## 结果与完整性边界

- 无请求 ID：独立历史行，不发送空 ID 的尝试查询。
- 无已采集尝试：明确说明可能未采集、未发起或记录缺失，不能推断实际调用数为 0。
- 序号连续：显示完整性未确认，仍可能进行中或尾部丢失。
- 序号有缺口：提示可能不完整，不推断完整请求结果。
- 处理成功仅表示这条尝试的处理结果，不代表模型正常结束或客户端请求最终成功。

当前没有请求终态与持久化完整性标记，因此本轮不展示“最终成功率”或把关联项数改称客户端请求数。不从历史数据回填未知尝试。

## 用例设计与自动检查

使用 implementation-workflow/test-design，复用现有 Go、Node 测试和真实浏览器验证，不新增组件测试框架。

| 规则 | 用例和断言 |
| --- | --- |
| 服务端归并 | 25 行同 ID 跨页不拆分，31 个关联项正确分页；空/NULL ID 每行独立；request:/log: 不碰撞 |
| 筛选语义 | 渠道、状态、模型、分组、请求 ID、协议、时间组合先选中关联项，摘要仍取整个请求；特殊字符字面匹配 |
| 摘要一致性 | 同一读快照、固定查询数；回填时 latest ID 与最大时间不同时，UI 时间/结果不混用 |
| 长尝试链 | 第二页显示 #21–25，不误报缺口；四类原因、HTTP 状态和预算字段对应显示 |
| 缺失信息 | 空尝试未知、缺口警告、无 ID 独立，连续不能证明完整 |
| 异步交互 | 换请求/页/关闭后的旧结果和错误失效；当前错误支持重试 |
| 既有行为 | 保持旧日志与统计；复制错误真实写入；HTML 字样只显示文本；窄窗详情可滚动 |

以下全部通过：

```sh
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test ./... -count=1
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test -race ./backend/shared ./backend/api -count=1
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go vet ./...
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go build ./...
pnpm --dir web test                   # 101 项，新增尝试状态/异步测试 3 项
pnpm --dir web run build:renderer
git diff --check
```

独立三轴评审：Standards 0、Spec 0；Correctness 发现最近日志时间读取 last_at 而其他字段读取 latest_log 的低严重问题，已改为 latest_log.created_at 并复核关闭。未运行专门复杂度工具；SQL 分组和读事务由场景测试、查询次数断言与代码评审验证。项目无突变工具，本轮未安装。

## 真实浏览器与管理 API 验证

本机 Ego Lite Chromium 打开 renderer 生产构建，真实 Gin 管理路由和 GORM 内存 SQLite 初始化 58 条合成日志、31 个关联项。未读取正式/Dev 数据，不执行真实收费推理。

实际验证：

1. 切换两种视图、归并分页。25 条同 ID 日志保留一个关联项，两条无 ID 历史日志独立。
2. mixed_%+&=中 关联 3 条日志，展示 4 次尝试：400 首次、503 预算兼容、503 同目标重试、200 切换渠道。
3. 过滤 mixed 请求、渠道 A、失败状态：归并仍为 1 项/3 条关联日志/最近渠道 B 成功，统计保持 2 条匹配失败日志；切回日志视图准确显示这 2 条。
4. long 请求有 25 次尝试，下一页实际显示 #21–25，完整性仍未确认。
5. gap 请求 #1/#3 显示序号缺口；old 请求无尝试显示未知提示；无 ID 行不发出 attempts 请求。
6. 服务一次返回 503 后显示重试入口，重试成功读取 4 次尝试。
7. 尝试错误复制后粘贴到临时测试 textarea，与完整多行错误摘要完全相同；临时元素随后移除。错误中的 script 字样未执行。
8. 检查归并列表、尝试链及 430px 窄窗截图，详情无横向溢出；长内容沿用弹窗内部滚动。

证据：`assessment.json`、`filtered-response.json`、`requests.json`、`final-snapshot.txt` 及三张 PNG。`fixture.go` 为隔离复现服务：先构建 renderer，再在仓库根运行 `go run docs/research/2026-10-21-request-associations/fixture.go`，打开 `http://127.0.0.1:13474/#/logs`。

测试结束已关闭 TaskSpace 和临时服务，不操作正式/Dev 进程。本轮不是桌面安装包或外部真实上游验证；上一步实际 main 的四次 HTTP 调用证据保留在 [上游尝试记录报告](../2026-10-21-upstream-attempts/README.md)。Node module type、大 chunk 构建提示仍为现有非阻断项。
