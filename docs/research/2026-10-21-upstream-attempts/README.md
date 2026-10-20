# 独立上游尝试记录：实施与验证

日期：2026-10-21。实现基线：`8121470`。本报告随独立上游尝试记录改动一同提交。

## 交付范围与边界

本轮完成“先补齐记录”：新增日志库表 `upstream_attempts`，不把预算兼容重试混入旧 `logs` 表，不改原有日志行数与 token 聚合，不做请求归并界面。

这里的“一次 HTTP 尝试”指网关显式调用一次 `http.Client.Do`，包括连接失败和取消。它不是上游实际收到请求的证明，也不计 Transport 内部重发或自动重定向的每个物理发送。请求装配/转换失败、全部目标熔断等发生在 Do 之前的故障不会生成 HTTP 尝试行，仍保留原有日志行为。

每次尝试记录：

- `request_id`、请求内连续递增的 `sequence`、外层 `relay_attempt`。
- `reason`：`initial`（首次目标调用）、`retry`（同目标普通重试）、`failover`（改用后续目标）、`budget_compat`（预算字段兼容重试）。兼容重试与其前一次共享 `relay_attempt`。
- 分组、渠道 ID、模型 ID/名称、入站与上游协议、stream。
- `budget_field`：实际发送正文顶层识别到的预算字段名，按固定顺序列出；不存预算值。无已识别字段时为空，不能据此推断所有协议均未携带预算。
- HTTP 状态（无响应为 0）、处理结果、错误分类/截断摘要、开始时间、耗时和写入时间。`success` 指该次 HTTP/转换/流处理是否成功，不代表模型 finish_reason=completed 或最终客户端请求成功。

预算兼容重试前先结束第一条 400/422 尝试，再记录下一次 Do，普通重试继续使用协商后的字段；切换目标后重新按该目标独立协商。流已提交后中断只记实际失败，不新增重试。尝试写入失败记录警告，不因此重试上游；可能存在记录缺口，不能宣称历史或失败写库后的尝试链完整。

生产 main 显式装配 writer，支持独立 LogDB；启动迁移只新建表，不从旧 Log 回填。清理沿用旧日志的时间截止参数，按尝试 started_at 清理，原 API 的 deleted 数仍只计算旧 Log。记录不含 URL、鉴权头、请求正文或响应正文；错误摘要沿用截断归因，传输层 url.Error 不保存 URL 部分。

## 查询接口

```text
GET /api/logs/attempts?request_id=<精确ID>&page=1&page_size=20
```

沿用本机管理访问边界。request_id 缺失或全空白返回 400；其他字符串精确匹配、不当作 LIKE。按 sequence、id 升序分页，返回 `{ total, data }`，页大小沿用 20 默认、100 上限。历史请求没有已采集数据时返回 `data: []`，表示没有记录，不能推断物理调用数为 0。现有 `/api/logs` 与 `/api/logs/stat` 不混入新表数据。

## 用例设计与自动检查

使用 test-design 技能梳理规则，由 implementation-workflow 实施测试；复用 Go testing、httptest、临时 SQLite 和已有注入式网关依赖。

| 规则/场景 | 期望与实际结果 |
| --- | --- |
| 400 预算拒绝 → 503 → 同目标重试 503 → 换渠道成功 | 4 次 Do、4 条尝试，sequence=1/2/3/4，relay_attempt=1/1/2/3；原因 initial/budget_compat/retry/failover，状态400/503/503/200，全部通过 |
| 预算协商与换目标 | 字段依次 max_tokens、max_completion_tokens、max_completion_tokens、max_tokens；不改变请求预算/重试次数，通过 |
| 旧计数与用量 | 混合路径仍只产生 3 条旧 relay 日志，成功 usage 仅计一次，通过 |
| 网络失败与取消 | 注入 Transport 返回连接错误或 context.Canceled，次数与原策略一致，失败分类和 status=0 正确，通过；取消不是实际客户端断开模拟 |
| 流已提交后中断 | 真实 httptest SSE 输出首包后静默触发 watchdog，只有一条失败尝试，不重试，通过 |
| 转换失败、全目标熔断 | 无 Do、无 HTTP 尝试记录，保留原日志，通过 |
| writer 返回失败 | 不增加上游调用，响应仍成功，通过 |
| 8 个并发请求，各一次兼容重试 | 8 个独立请求各有 sequence=1/2、相同请求内 relay_attempt=1，不串线，race 通过 |
| 迁移、独立 LogDB、历史数据 | 保留旧价格/用量，尝试写到日志库，不回填历史，通过 |
| API/存储 | 特殊字符精确匹配、乱序插入后排序、分页总数、请求+序号唯一、空数组、必填校验，通过 |
| 留存清理 | StartedAt 截止边界保留、旧尝试分批删除、不增加旧 deleted 计数，通过 |

实际检查全部通过：

```sh
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test ./... -count=1
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test -race ./backend/gateway ./backend/shared ./backend/api -count=1
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test -race ./backend/gateway -run '^TestAttempt' -count=1
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go vet ./...
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go build ./...
git diff --check
```

三轴评审由独立上下文完成：Correctness 0、Standards 0、Spec 0。评审提出旧 duration_ms 应在新增写库前采样，已固定并复核关闭，相关 race 回归通过。未运行专门复杂度工具或生成 CRAP 指标；新增追踪独立于已有失败策略，未修改其判断表。项目无突变测试设施，本轮未安装。前端未改，不重复前端/浏览器测试。

## 实际 main 进程验证

`live_probe.py` 启动本次构建的实际 main 二进制，在临时目录用全新主库和独立日志库，经真实管理 API 建立两个合成渠道和故障转移分组。健康检查要求匹配新进程的实例标识，避免误认占用临时端口的服务。上游是本机 ThreadingHTTPServer，未读取生产/Dev 配置或密钥，未连接外部收费模型。

探针实际触发 400→503→503→200，HTTP 端观测到 4 次调用，按请求 ID 从生产管理 API 查得 4 条尝试，次序、原因、状态和字段与出站一致；旧日志与统计仍为 3 条，输入/输出 token 为 7/1；客户端 Responses 结果 completed 且正文 OK。结果见 `main-probe-result.json`。临时子进程、上游和数据库已自动清理，不停止正式/Dev 实例。

复现（临时端口 13473 须空闲）：

```sh
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go build -o /tmp/portunus-attempt-main .
python3 docs/research/2026-10-21-upstream-attempts/live_probe.py /tmp/portunus-attempt-main
```

本轮不是外部真实模型供应商、桌面安装包或浏览器界面验证；没有新增尝试链 UI。下一步才是读取独立尝试记录，按请求展示过程并定义新请求统计；需对历史缺失、进行中与写库失败保留不完整标记。
