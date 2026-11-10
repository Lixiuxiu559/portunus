# 配置导入导出：突变测试接入与审计

日期：2026-11-10。分支 `dev`，Git 基点 `a6bb26aa7c8b4dade3fab0dbf7c9b49f13fa03c5`。对象是当前工作区中的配置迁移实现，包含尚未跟踪的文件。

用户在原实施记录说明缺少工具后，明确授权“适配一下，把突变测试安排上”。本轮安装固定版本开发工具、接入现有测试、实际运行、分析存活项并补充业务断言。执行命令、范围、隔离方式和退出码见 [使用说明](../../mutation-testing.md)。此前实施记录中的“未运行突变测试”保留为当时事实，本记录是后续验证。

## 接入与补测

Go 使用 Gremlins v0.6.0；JavaScript 使用 StrykerJS 10.1.1 与同版本官方 TAP runner，保留 `node:test`。两端分别实施，文件范围互斥，由主任务核对入口、分类和集成；突变仅在系统临时副本中发生。

相对于原实施源码指纹，生产代码、Go 模块文件及业务契约保持一致。本轮增加工具入口、开发依赖、工具自测和以下业务测试：

- Go：密码精确上限、空载荷信封、密文精确上限；严格 JSON 的代理对／深度边界；引用、名称与 PNG 规则；可达的渠道／模型／分组／成员数量上限；SQLite 预览、原子提交、回执、恢复与结构检查；API 查询及 multipart 边界。
- Main：全部窄 IPC 的入参拒绝；提交四个标识与实例代次绑定；固定错误映射和敏感信息过滤；异常 JSON、预览及响应头；完整 multipart；同 fd 读取后复核；保存前导航／后端变化；在途请求取消和旧预览清理。
- Renderer：密码 UTF-8 与代理区间端点；分组 ID 筛选与缺失引用；十个桥方法和错误兜底；最新异步响应胜出；卸载后的迟到响应；重复终态通知、恢复、继续查询及关闭后 ACK。

Go Codec 首轮 28 个突变中有 6 个存活，补齐有意义的边界测试后 28 个全部被发现。JS 首轮固定范围生成 1,807 个突变，分类为 Killed 938、Survived 629、NoCoverage 165、Timeout 74、RuntimeError 1、CompileError 0；随后对同一生产范围补测并完整复跑。

没有为提高分数禁用突变器，也没有修改生产逻辑迎合突变。超时及编译失败分别记录；对 Renderer 测试没有设置会将挂起突变提前转换为 Killed 的内层测试期限。

## 最终活跃突变结果

下表仅将明确的 Killed 计作检出。原始工具把 Timeout 算入展示分数时，不沿用其分数作为结论。

| 范围 | 总数 | 明确检出 | 存活 | 无覆盖 | 超时 | 编译无效 | 运行错误 |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| JS Main | 1,197 | 861 | 258 | 37 | 41 | 0 | 0 |
| JS Renderer API | 56 | 52 | 3 | 0 | 0 | 0 | 1 |
| JS controller | 391 | 296 | 76 | 5 | 14 | 0 | 0 |
| JS model | 163 | 156 | 6 | 0 | 1 | 0 | 0 |
| **JS 合计** | **1,807** | **1,365** | **343** | **42** | **56** | **0** | **1** |
| Go codec | 28 | 28 | 0 | 0 | 0 | 0 | 0 |
| Go rules | 232 | 145 | 31 | 18 | 3 | 35 | 0 |
| Go recovery | 176 | 132 | 14 | 27 | 1 | 2 | 0 |
| Go storage | 100 | 75 | 5 | 0 | 0 | 20 | 0 |
| Go api | 55 | 47 | 4 | 4 | 0 | 0 | 0 |
| **Go 合计** | **591** | **427** | **54** | **49** | **4** | **57** | **0** |

上述运行没有跳过、未知状态或残留待执行项。两端的运行已结束，但测试有效性仍有未证明部分，**严格门禁尚未通过**。JS 同范围明确检出增加 427，无覆盖减少 123；这些是本次断言的实际增益，不等于剩余存活全部为等价。

JS 最终目录为 `web/reports/mutation/js/core/2026-11-10T02-54-05-330Z-a6f989e8/`，普通基线 80/80，源码前后 214 项完全一致。Go 最终五份活跃运行目录为：

```text
tmp/mutation-go/reports/2026-11-10T02-50-04.159Z-codec/
tmp/mutation-go/reports/2026-11-10T02-50-33.812Z-rules/
tmp/mutation-go/reports/2026-11-10T02-53-10.873Z-recovery/
tmp/mutation-go/reports/2026-11-10T02-54-36.844Z-storage/
tmp/mutation-go/reports/2026-11-10T02-55-35.374Z-api/
```

原始 JSON／HTML、逐项状态和测试输出保留在上述已忽略目录。可跟踪的执行摘要与源码指纹见本记录末尾的证据链接。

## 剩余项与结论边界

仍需补充的 JS 业务断言示例：

- Main `config-transfer.js:399`（id 994）：文件令牌持有期间，只改变后端 instance 或只改变 generation 的独立场景；当前提交前的四标识及代次变更测试不能替代此时点。
- Main `config-transfer.js:98`（id 272）：真实 HTTP 服务端尚未核对上传请求体字节。当前 multipart 内容断言使用注入 transport，不能杀死实际 `req.write(part)` 被移除的突变。
- Main 文件精确上限、读取期间增长、查询预算内多次网络失败等仍有存活／无覆盖项；不能由后端已经验证相同数据上限推断 Main 全部保护已证明。
- Controller `controller.js:197`（id 1700）：未知结果状态卸载后不得 discard；API `configTransfer.js:18`（id 1273/1277）：缺少 commit 桥方法的错误阶段；model `model.js:13`（id 1775）：高代理项之后接 `U+E000` 的非法组合。

已核对的等价示例：controller 的 summary／recovery 代次自增改自减，仍通过唯一代次相等性阻止过期响应；API 的错误阶段 `input` 改空字符串后由错误对象默认值恢复为 `input`；model 密码循环多走一次末尾 `NaN` 字符，不改变输出。这些仅是列明位置的证据，不代表其余存活项。

JS 的 56 个 Timeout 中，48 个为外层墙钟超时，8 个为工具的循环命中上限。1 个 RuntimeError 是 API 桥方法列表的 map 回调变为 `undefined`，使模块顶层 `Object.fromEntries` 在测试注册前抛错；保留为运行错误，不转成 Killed。

Go 的 57 个 NOT VIABLE 按实际编译失败输出单列；4 个 TIMED OUT 也不计为明确检出。Go 使用默认五类突变器，未运行额外可选突变器。分 profile 的测试选择、JS 测试文件粒度及 JSX 未纳入范围，均见使用说明。

Go 剩余的真实缺口包括：`json.go:182/196/203/215` 的未闭合／截断转义边界、`service.go:104` 的 SQLITE_BUSY 重试次数与方向，以及 API 真实连接读取 deadline。`httptest.ResponseRecorder` 不能验证真实连接的 `SetReadDeadline` 效果；相关存活不得当成等价。

Go 已核对的等价示例：`plan.go:80` 的截断条件在精确上限时改为包含等号，切片仍为原 prefix；`service.go:630` 在 more 分支已截取到正数 limit 后，非空判断包含零仍恒真；API `config_transfer.go:317` 的两 part 计数条件变宽后，第三 part 仍会先被未知字段／重复字段规则拒绝。Go 的全部存活、未覆盖位置、超时和编译证据保存在 [Go 聚合记录](mutation/go-summary.json)。

## 工具评审与最终验证

独立三轴评审分别保留：[Correctness](mutation/correctness-review.md)、[Standards](mutation/standards-review.md)、[Spec](mutation/spec-review.md)。Correctness 的指纹遗漏、清理及进程回收问题和 strict/dry-run 问题已修复并独立复测，未解决项 0；Standards 硬性违规 0，存在 1 个低优先级的进程控制重复代码判断项；Spec 0。

进程控制判断项保留为后续维护建议。两端目前各自负责工具输出、平台差异与分类，未在此次测试接入中另建公共进程框架。复杂度审计未重跑：本轮生产逻辑没有变化，原实施记录中的降级复杂度结论仍保持原范围；这不影响本轮真实执行的突变审计。

| 检查 | 实际结果 |
| --- | --- |
| `pnpm test` | **336/336**，无跳过；包含新增业务断言及 10 项 JS 工具测试 |
| `pnpm typecheck`、`pnpm build:renderer` | 均退出 0 |
| `go test ./... -count=1`（离线模块、只读依赖） | 全量回归退出 0 |
| `go test -race ./backend/configtransfer ./backend/api` | 通过，93.965s／5.640s |
| `pnpm test:mutation --dry-run` | 最终统一入口顺序完成 JS core 和 Go all，均退出 0；仅证明基线／插桩接入 |
| `node scripts/mutation-go.mjs codec --strict` | 最终适配器真实复跑，28/28 明确检出、退出 0，源码与临时目录恢复检查均为 true |
| 工具故障注入 | 新增／删除源文件、复制故障、超时子孙进程、中断、缺报告／未知状态、dry-run 严格模式分别验证；故障不伪报成功 |

活跃突变五份 Go 结果保留原始运行记录。工具评审之后只修正适配器完整性、清理和退出码，不改原始状态；最终一键 dry-run 与 Codec 严格复跑验证了修正后的入口。没有把 dry-run 当作完整突变复跑。

可跟踪证据：[执行摘要](mutation/evidence.json)、[最终源码指纹](mutation/source-manifest.json)、[运行器回归证据](mutation/runner-regression-evidence.json)、[复制故障证据](mutation/runner-copy-failure-evidence.json)。原始大型报告和临时日志保留在其记录路径，未提交缓存或二进制。当前交付是本地开发改动与审计记录。
