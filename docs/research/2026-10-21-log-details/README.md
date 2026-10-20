# 调用记录筛选与详情验证

日期：2026-10-21。基线：`97a95de`。本报告随日志筛选与详情改动一同提交。

## 交付范围

- `/api/logs` 与 `/api/logs/stat` 新增可选 `request_id` 等值查询，和渠道、分组、模型、状态等条件组合，分页数据、总数与统计共用过滤逻辑。
- 日志页面新增渠道与请求 ID 筛选；输入条件后点击查询回到第一页；不匹配时显示筛选空态及零统计。
- 列表同时显示实际渠道 ID、当前渠道名称和上游模型。同名跨渠道资源可区分；删除或未加载渠道保留 ID，缺失 ID 显示未记录。
- 行内详情展示时间、分组、渠道、模型、请求 ID、结果、HTTP 状态、客户端协议、响应方式、耗时、用量及现有兼容遥测。错误多行原文可选择和复制，请求 ID 有独立复制按钮；复制失败不会提示成功。
- 不新增数据库字段，不保存提示词、响应正文或密钥，不将当前渠道协议推断为历史请求协议。详情响应方式只读取显式 stream 布尔值；缺失显示未记录。
- 未实现请求归并、完整尝试链或新的计数口径。“记录数”仍按日志行统计。

## 用例设计与验证方式

依据用户批准的筛选/详情范围及 `docs/product/PRODUCT.md`，使用现有 Go 测试、Node test 与实际浏览器，不引入组件测试框架。

| 规则 | 用例与预期 | 验证入口 |
| --- | --- | --- |
| 请求精确定位 | 同 request_id 跨渠道同名；前缀、`%`、`_`、`+` 不作通配；未匹配为空 | shared 查询、API URL 编码回归 |
| 筛选一致 | 渠道+请求+分组/模型组合；分页只裁剪数据，不影响总数/统计 | shared/API 测试及真实浏览器 |
| 历史记录 | 不筛请求时保留空 ID；缺渠道/协议不伪造；未知错误分类原样显示 | Go、Node、浏览器 |
| 查询交互 | 第二页输入新条件，查询回第一页；统计与结果同步 | 浏览器 |
| 复制 | 等待实际写入；拒绝权限时提示失败；保留多行和特殊字符 | Node、浏览器复制/粘贴及模拟拒绝 |
| 内容与布局 | HTML 字样作为文本；长行可换行/滚动；深浅色、窄窗详情 | 浏览器和截图 |

## 自动检查结果

全部通过：

```sh
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test ./... -count=1
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go vet ./...
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go build ./...
pnpm --dir web test                   # 98 项通过，原有 94 项 + 新增 4 项
pnpm --dir web run build:renderer
git diff --check
```

新增测试为 `backend/api/log_test.go`、`backend/shared/log_query_test.go`、`web/src/renderer/utils/logQuery.test.js`、`web/src/renderer/utils/logDetails.test.js`。

代码评审按 correctness / standards / spec 三轴串行只读完成：前两轴无发现；Spec 发现详情从首包推断缺失 stream 的低严重偏差，已改为显式布尔值并独立复核关闭。未运行专门复杂度工具；本轮主要是等值查询和展示，未引入复杂路由或状态机。项目无突变测试设施，本轮未安装。

## 实际浏览器与后端结果

本机 Ego Lite Chromium 打开 renderer 生产构建，连接真实 Gin 管理路由和 GORM 内存 SQLite。`fixture.go` 初始化 46 条合成日志、两个合成渠道，未读取正式/Dev 数据，未连接收费上游。

- 初始显示 46 条记录，包括无请求 ID/渠道/协议的旧记录和已删除渠道 #99。
- 翻到第二页后输入 `req_%+&=中` 查询，结果准确为 25 条，页码回到 1/2；相似前缀的其余日志不混入。
- 组合渠道 A、分组 coding 后为 12 条，非缓存输入 120、输出 24；渠道 B 为 13 条。列表渠道与筛选一致。
- 未匹配请求显示筛选空态，四张统计卡均为 0。
- 请求 ID 复制后粘贴回输入框，特殊字符完全一致。错误摘要复制后粘贴到临时测试 textarea，分类和多行错误原文完全一致。该 textarea 仅用于浏览器验证，随后移除，不是产品功能。
- 用浏览器临时替身令 clipboard.writeText 拒绝，页面显示“复制失败，请手动选择文本复制”；随后恢复原 API。这项验证是模拟失败，不声称触发了真实系统权限拒绝。
- 错误包含 `<script>window.unexpected=true</script>` 字面文本，未执行。未知 future_error 和已删除渠道 #99 正确保留。
- 查看深色详情及浅色列表截图；430px 视口下详情 clientWidth/scrollWidth 均为 398px，无详情横向溢出。沿用宽表格水平滚动。

`assessment.json` 汇总实际观察与 API 断言；`filtered-response.json` 保留组合查询的真实列表/统计响应；`requests.json` 保留实际请求路径；`final-snapshot.txt` 和三张 PNG 为页面证据。近期记录随 60 秒窗口自然减少，不作为总记录数断言。

## 复现与限制

先构建 renderer，在仓库根运行：

```sh
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go run docs/research/2026-10-21-log-details/fixture.go
```

打开 `http://127.0.0.1:13472/#/logs`。验证结束已关闭本轮 TaskSpace 并停止临时服务；正式版/Dev 实例未操作。

本轮是浏览器与真实管理后端验证，不等于重新打包 Electron 安装验证；也不包含真实推理调用、GitHub Actions 线上运行或请求重试链完整性验证。渠道名称来自当前目录，历史名称未保存；日志已有的 0 值无法区分缺失 usage 与实际 0 消耗，页面保留说明。原有 Node module type、大 chunk 构建提示仍非阻断。
