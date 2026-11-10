# 配置迁移突变测试

本项目为配置导入导出接入两条突变测试链：Go 使用固定版本 Gremlins v0.6.0；Main 与 Renderer 的 JavaScript 使用 StrykerJS 10.1.1 和同版本官方 TAP runner，对接原有 `node:test`。工具仅供开发，不进入应用运行依赖。

## 安装与运行

需要项目指定的 Go 1.26.6、Node.js 24 和 pnpm。首次准备工具及模块缓存需要联网；Go 工具安装到仓库已忽略的 `tmp/mutation-go/bin/`，不修改主项目 `go.mod`、`go.sum` 或全局 `GOBIN`。

```sh
# 项目根目录，首次安装
pnpm --dir web install --frozen-lockfile
node scripts/mutation-go.mjs --install

cd web
pnpm test:mutation --dry-run   # 普通测试基线、插桩和覆盖采集；不执行活跃突变
pnpm test:mutation            # 顺序执行 JS core、Go all
pnpm test:mutation --strict   # 同样执行；存在任何未明确杀死项时返回非零
```

默认模式的退出 0 只表示工具分析结束，不代表测试已发现全部逻辑错误。`--strict` 要求所有突变明确为 Killed，适合作为严格门禁；当前仍有待分析项，不能将其描述为通过门禁。普通测试基线失败、报告缺失或状态未知时，入口返回非零，不会伪装成审计完成。

分开运行可以缩短修改后的反馈时间：

```sh
cd web
pnpm test:mutation:js
pnpm test:mutation:js:main
pnpm test:mutation:js:renderer
pnpm test:mutation:go

# 项目根目录；支持 --dry-run 和 --strict
node scripts/mutation-go.mjs codec
node scripts/mutation-go.mjs rules
node scripts/mutation-go.mjs recovery
node scripts/mutation-go.mjs storage
node scripts/mutation-go.mjs api
```

## 实际范围

| 入口 | 突变的生产代码 | 执行的测试 |
| --- | --- | --- |
| JS core | Main `config-transfer.js`；Renderer `api/configTransfer.js`、`controller.js`、`model.js` | 对应的四个 `node:test` 文件 |
| JS main / renderer | 上述范围按进程拆分 | 对应测试，Main 额外保留三层桥接回归 |
| Go codec | 加密信封、密码与密文边界 | Codec 原有及新增边界测试 |
| Go rules | JSON、载荷校验、名称计划、领域类型 | 解析、规则、PNG、引用与计划测试 |
| Go recovery | service、restore | 真实临时 SQLite 的预览、提交、恢复、事务、回执及生命周期测试 |
| Go storage | snapshot、schema | 快照、数据库结构、实体上限与恢复往返测试 |
| Go api | HTTP 配置迁移适配层 | 配置迁移 API 测试 |

Go 每个 profile 先运行对应包的完整普通测试，再以报告中记录的 `-run` 选择专注测试执行突变。`all` 顺序执行上述五个 profile；另有成本更高的 `domain`，使用领域包全部测试。每个 profile 的无覆盖项按实际结果保留，不能用完整普通基线替代突变检出证据。

JS 使用全部默认生产 mutator，包含静态突变；没有为分数排除字符串、条件或正则突变。官方 TAP runner 按测试文件采集覆盖与选择测试，不是按文件内每个 `test()` 采集。JSX 界面组件及既有 Main 启动、preload、sidecar 不在本次突变范围，尽管部分集成测试会加载它们。

## 隔离与报告

- 工具读取当前工作区源码，包括本次尚未跟踪的文件，复制到本次独有的系统临时目录；突变只发生在副本。Go 不拷贝 Git 元数据、用户配置、数据库或应用数据；JS 仅拷贝 `src` 和列明的必要配置，并拒绝源码符号链接。
- 普通测试使用合成凭据、临时文件和临时数据库。HTTP 测试仅监听自行创建的随机回环端口。受限环境须允许此测试监听，不使用正式或 Dev 服务。
- Go 活跃突变时固定 `GOPROXY=off`、`GOSUMDB=off`、`GOTOOLCHAIN=local` 和只读模块模式；模块下载限于显式 `--install`。
- 两端保存原源码的 SHA-256 前后指纹。并发编辑只报告，不用旧快照覆盖工作区；清理只作用于本次创建的临时目录。信号转发仅针对本次子进程组。
- JS 报告：`web/reports/mutation/js/<profile>/<run-id>/`，包含普通基线日志、工具日志、JSON、源码交互 HTML、分类摘要和源码指纹。
- Go 报告：`tmp/mutation-go/reports/<run-id>-<profile>/`，包含普通基线、Gremlins JSON、逐次突变真实测试输出／源码变化、分类摘要和运行元数据。
- 报告、缓存和工具二进制已被 Git 忽略。入口不上传 dashboard。

当前 Go 入口的进程组及 shim 适配针对 macOS/Linux（POSIX）；Windows 尚未适配验证。不要将本机执行结果外推为全部桌面平台验证。

## 分类规则

只有 Killed / KILLED 计为测试明确发现错误。Survived / LIVED 是测试没有发现的已执行突变；NoCoverage / NOT COVERED 是未触达项；超时、编译失败、运行错误、跳过和待执行项分别记录。

Gremlins v0.6.0 会把 `go test` 退出 1 统一当作 KILLED，而编译失败也可能返回 1。本项目的隔离 Go shim 保存真实输出，将明确的 `[build failed]` 转为 NOT VIABLE；Go 内层测试超时交由 Gremlins 外层超时分类，避免误计为已杀死。逐文件状态是汇总依据，原始工具的比例不能替代本项目分类。

Stryker 的展示分数可能把 Timeout 作为检测结果；本项目摘要把 Timeout 单列。等价突变只有在核对上下文后才能标注，不能把全部存活项称为等价，也不以修改生产行为提高分数。

首次接入的执行记录、补测及剩余限制见 [突变审计记录](requirements/2026-11-09-config-import-export/mutation-report.md)。
