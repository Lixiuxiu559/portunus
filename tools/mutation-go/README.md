# Go 配置迁移突变测试

本工具使用 **Gremlins v0.6.0**，只审计本次配置导入导出代码。适配器依赖项目已有的 Node 24 与 Go 1.26.6；当前支持 macOS/Linux POSIX，Windows 尚未适配或实测。

```sh
# 首次安装：需要联网；工具和完整模块缓存准备不修改主 go.mod/go.sum
node scripts/mutation-go.mjs --install

# 默认串行执行本次全部配置迁移 profiles
node scripts/mutation-go.mjs all

# 先检查突变范围（会先运行普通测试及覆盖率，但不执行突变）
node scripts/mutation-go.mjs rules --dry-run

# 任一存活、未覆盖、超时、不可编译或跳过项均令退出码非零
node scripts/mutation-go.mjs all --strict
```

安装位置为仓库中已忽略的 `tmp/mutation-go/bin/gremlins`，报告为 `tmp/mutation-go/reports/<UTC时间>-<profile>/`。`--install` 固定使用版本标签，不使用 `latest`。工具通过 `go version -m` 核验实际模块版本；`go install` 构建的官方 `--version` 可能显示 `dev`，不据此认定已安装版本。安装时额外在系统临时目录中的 go.mod/go.sum 副本运行 `go mod download`，为 Gremlins 固定的预备下载步骤补齐缓存；实际基线与突变显式使用 `GOPROXY=off GOSUMDB=off GOFLAGS=-mod=readonly`。

## 范围与执行成本

| Profile | 突变目标 | 突变时执行的测试 |
| --- | --- | --- |
| `codec` | `backend/configtransfer/codec.go` | `TestCodec*`、`TestMutationCodec*`，包括真实 Argon2/GCM 与精确字节上限 |
| `rules` | `json.go`、`validate.go`、`plan.go`、`types.go` | 严格解析、业务关系、源完整性、非法 PNG，以及 `TestMutationJSON/Rule/Plan*` |
| `recovery` | `restore.go`、`service.go` | `TestMutationRestore/Recovery/Stored/Service/Commit*`；从已验证 payload 边界直接准备，执行真实 SQLite 事务、回执、回滚、重放、租约与分页 |
| `storage` | `snapshot.go`、`schema.go` | schema 精确匹配、源完整性、直接快照往返与 `TestMutationStorage*`，含可达的实体上限 |
| `api` | `backend/api/config_transfer.go` | `TestConfigTransfer*`，包括真实 Gin 适配、合成库、multipart 与查询契约 |
| `domain` | `backend/configtransfer/` 全部生产 Go 文件 | 完整领域测试；可选更昂贵的对照运行，不是 `all` 的重复步骤 |

`all` 包含前五个 profile，目标合并覆盖配置迁移领域的全部九个生产 Go 文件与本次 API 文件，不扩展到其他 API 或全仓库。每个 profile 在突变前先执行对应包的**完整普通测试基线**；基线失败立即终止该 profile。突变时按表选择测试以控制每个变异重复执行密码推导和并发 fixture 的成本。因此 `all` 表示目标文件均纳入，**不表示每个突变均经过全部集成测试**。并发上限为每个 profile 两个 Gremlins worker，`all` 串行执行 profile。

仅使用 Gremlins 默认的全部五类突变器：算术、条件边界、条件否定、自增自减、负数反转；未用删减突变器或黑名单提高分数。额外可选突变器并非本次已验证范围。

## 隔离、结果与退出码

脚本通过 `git ls-files -co --exclude-standard` 收集当前已跟踪及未跟踪的 Go 源码、模块文件与 backend 测试资源，复制**当前内容**到系统临时目录；不从 HEAD 导出、不复制真实配置/数据库、不改写 HOME。Gremlins 仅在该副本中继续创建 worker 副本。结束后核对工作区与种子副本的 SHA-256，移除本次全部临时源码，恢复证明保存在 `run.json`。SIGINT/SIGTERM 只转发给本脚本创建的进程组，等待本次子进程退出再清理。

每份报告包含：

- `baseline.log`：完整普通测试基线。
- `gremlins.log` / `gremlins.json`：官方原始输出，包含逐个位置与状态。
- `test-runs/*.json`：每次实际测试的命令、输出、退出信息及突变前后源文本；仅使用合成测试数据。
- `summary.json`：按逐个突变计算的完整分类与总数。
- `run.json`：执行参数、输入源码指纹、结束时间、退出状态与临时源码清理证明。

分类为 `KILLED`、`LIVED`、`NOT COVERED`、`TIMED OUT`、`NOT VIABLE`、`SKIPPED`，dry-run 的 `RUNNABLE` 单独保留。普通命令退出 0 只表示工具完整运行；`--strict` 在任何非已杀死类别存在或仅执行 dry-run 时返回 10。summary 的 `toolCompleted`/`completed` 表示工具过程完成，`runCompleted` 表示已实际执行且没有 RUNNABLE，`resultComplete` 还要求没有超时、不可编译或跳过项，`allKilled` 才表示全部已杀死。缺报告、未知状态、零总数、缺目标文件、实际运行残留 RUNNABLE、普通测试失败、工具失败或源码指纹变化均失败，不解释为测试通过。

v0.6.0 有两个需要适配的报告细节：其执行器把 `go test` 退出 1 当成已杀死，而 Go 编译失败也可能返回 1；Node `go` shim 将明确的 `[build failed]` 转为工具识别的退出 2，单列 `NOT VIABLE`。Go 自身的 `panic: test timed out` 会保留到 Gremlins 外层 deadline，单列 `TIMED OUT`，不当作断言杀死。其原始 `mutants_total` 只累加已杀死、存活和不可编译，遗漏超时与未覆盖；项目 summary 总数直接累加逐文件的全部状态，不沿用该不完整字段。

不可编译和超时不计作已杀死。存活并不自动等于实现缺陷：可能是业务测试缺口、组合约束下不可达或等价改动；需要对照每个补丁分析。未覆盖项也保留在结果中，不通过排除文件隐藏。

## 官方依据

选择 Gremlins 而非旧 go-mutesting：官方 [v0.6.0 发布记录](https://github.com/go-gremlins/gremlins/releases/tag/v0.6.0)包含 Go 1.25 及依赖更新、文件排除和状态输出；本项目已用 Go 1.26.6 实际安装并执行。其 [命令文档](https://github.com/go-gremlins/gremlins/blob/v0.6.0/docs/docs/usage/commands/unleash/index.md)提供覆盖率筛选、worker、timeout、JSON 与各状态。固定执行器的退出码映射见 [executor.go](https://github.com/go-gremlins/gremlins/blob/v0.6.0/internal/engine/executor.go)，总数字段实现见 [report.go](https://github.com/go-gremlins/gremlins/blob/v0.6.0/internal/report/report.go)。[go-mutesting 官方说明](https://github.com/zimmski/go-mutesting)作为对照；本次没有安装或修改它。
