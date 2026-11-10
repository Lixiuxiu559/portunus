# Standards 单轴评审

## 评审范围

- 固定点与 merge-base 均已由 git 确认：`a6bb26aa7c8b4dade3fab0dbf7c9b49f13fa03c5`；当前分支 `dev`。
- 主评审为 10 个文件：`scripts/mutation-go.mjs`、`tools/mutation-go/README.md`、`web/scripts/mutation-all.mjs`、`mutation-js.mjs`、`mutation-js-lib.mjs`、`mutation-js-process.mjs`、`mutation-js.test.mjs`（后五项位于 `web/scripts/`）、`web/stryker.config.mjs`、`web/package.json` 的新增脚本和 Stryker 依赖、`web/.gitignore`。前八项为未跟踪文件，按当前完整内容评审；后两项按固定点 diff 评审。
- 补充查看 `backend/configtransfer/mutation*_test.go`、`backend/api/config_transfer_test.go` 与四个迁移 JS 测试的 fixture、文件/数据库/进程隔离用法；没有全面复审此前生产实现或测试业务逻辑。
- 规范源：根 `AGENTS.md`、`CLAUDE.md`、`docs/product/DEVELOPMENT.md`，并检查 `docs/wiki/指南/本地开发与测试.md`、系统架构约定。相关祖先与目录未发现额外 AGENTS/CLAUDE、CONTRIBUTING、CODING_STANDARDS、REVIEW、editorconfig。
- 工具强制范围依据 `.github/workflows/ci.yml`、`web/package.json`、`web/tsconfig.json`：Go tests/vet/build，Node tests，前端 tsc 与 renderer build；未发现 ESLint/Prettier/Biome 等格式规则。tsc include 仅 `src`，且 checkJs=false，不能声称对新增 mjs 做了类型检查。本评审不重复格式、语法或已有工具可确定的检查。
- 使用 `codegraph explore` 优先定位；新工具未被索引，随后直接读取。独立执行本轴，不使用 Correctness/Spec 结论。

## 硬性违规

未发现违反仓库文档化规范的地方。主工具在系统临时目录准备源码副本，没有改写 HOME、启动正式/Dev 应用、按应用名终止进程或直接使用正式数据库的代码；抽查补测使用临时目录、合成库和 fixture。

## 判断项（非硬性违规）

- `scripts/mutation-go.mjs:32–45` 与 `web/scripts/mutation-js-process.mjs:14–36`：possible **Duplicated Code**（低优先级）。两处各自维护 POSIX 进程组存活检测、ESRCH 处理、TERM → 宽限轮询 → KILL 的同一逻辑形状：
  ```js
  // Go 适配器
  process.kill(-pid, 0);
  while (groupAlive(pid) && Date.now() < termDeadline) ...
  process.kill(-pid, 'SIGKILL');
  // JS 适配器
  process.kill(process.platform === 'win32' ? child.pid : -child.pid, 0);
  while (groupExists(child) && Date.now() < deadline) await delay(20);
  signalOwnedTree(child, 'SIGKILL');
  ```
  后续修改清理时限或信号策略时需同步维护两套机制，可考虑抽取共享 POSIX 进程组原语。Go 的 Gremlins shim 和 JS 的 Windows 分支有不同需求，因此这里只建议复用共同形状，不要求统一执行器；未将其当作已证实缺陷或规范违规。

## 指纹与边界

2026-11-10 03:02 UTC 记录并补读 Go 当前版本。Go 从初读 SHA `3c64f781c4de487ae6e829ca833e7be8c27bb0713fc34deb069e72990de77282` 变为下列版本，变动内容已静态补读；后续若再变化，根 agent 应增量复核。

| 文件 | SHA-256 |
|---|---|
| scripts/mutation-go.mjs | cb463063c91aae4b5c4a5a837aadd09154d2a38bdbd81ed2b7a863532d1c5451 |
| tools/mutation-go/README.md | 91d1394f666ce8ae37b7d501d109650ac23300dc77cddbe70d952743a19aacdf |
| web/scripts/mutation-all.mjs | 3410e98fd6b1d9e74e213478e29bd416f92c0ae7d7eb9f382bf0afaf33fd07e1 |
| web/scripts/mutation-js.mjs | 1b7e42252483b7411e81627c8106b674c07ab58293a65365a7597ccc29576621 |
| web/scripts/mutation-js-lib.mjs | 2037235a6263976898a20f0b597c83a67c094cd99654120f207f3cbc92ee9fcc |
| web/scripts/mutation-js-process.mjs | 63c741753a12718391b161a82a96053a3cc003e8e98ff4719715111ba405ef14 |
| web/scripts/mutation-js.test.mjs | c1fba130b100d4c5777825a110524f3785a6910119e4a394c56655ea36a0e267 |
| web/stryker.config.mjs | aaaa89accf6bdfa57061eff88f08f6ee148a15d54cb179ab06dd10a74d82ac08 |
| web/package.json | b63a8286431c69a8e930cdf2bcac1b714f9b46f4027900540e6f4d130edd3cfb |
| web/.gitignore | e8c1c4994b6a2b1892c6d980a6f6f455a89eddc6db6091cc3fd9d2a216ed8c1d |

全程只读仓库；仅写本报告。未读取 `.claude/settings.json`，未运行突变、测试、应用或真实数据库，未检查外网链接内容。Standards 合计 1 条：硬性 0、判断项 1；最严重为低优先级 possible Duplicated Code。
