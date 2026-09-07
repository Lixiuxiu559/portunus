---
name: release
description: Portunus 发版流程：升版本号、打 tag、触发 GitHub Actions 矩阵构建 Windows/macOS 安装包、验收 draft release 后发布给客户。当用户说"发版"、"发布新版本"、"出新包"、"release"、"打 tag 发布"、"把这版交付给客户"、"更新版本号"，或任何想要把 portunus 当前代码做成安装包发布的意图时，务必使用本技能。仅适用于 portunus 项目。
---

# Portunus 发版

把仓库当前代码做成 Windows + macOS 安装包并发布。核心链路：

**前置检查 → 定版本号 → push + tag（闸 1）→ 盯 CI → draft 验收（闸 2）→ publish**

## 硬约束（违反会造成线上更新异常）

1. **版本号一致性**：`web/package.json` 的 `"version"` 必须与 git tag 严格对应（`v0.2.0` ↔ `0.2.0`）。electron-updater 靠 release 资产里 `latest.yml` 的版本号比较新旧，两者不一致会导致客户端"永远提示有新版"或"永远不提示"。
2. **CI 构建的是仓库代码**：CI 从 git checkout 构建，本地未提交的改动不会进包。工作区不干净就发版 = 发出去的不是你以为的那份代码。
3. **tag 必须推送到远端才触发构建**：只打本地 tag 什么都不会发生。且触发的前提是 **workflow 文件已在 GitHub 默认分支上**——本地有 `release.yml` 不算数，远端没有它，tag 推上去 CI 会**静默不触发**（不跑也不报错）。

## 阶段 0：前置检查（自动执行）

1. `git status --porcelain` 非空 → 停下来问用户：要发布的代码必须先提交并推送。选项：帮用户整理提交 / 用户自己处理 / 取消发版。不要替用户决定把 WIP 卷进发版提交。
2. `git fetch origin && git status -sb` 确认本地与 `origin/main` 的关系：**ahead** 属正常（阶段 2 会一并 push，但要数清领先几个提交）；**behind 或 diverged** 说明远端有你没见过的历史，必须停下来问用户怎么对齐，不能直接发。
3. 确认发布 workflow 已在 GitHub 默认分支：`gh api repos/Lixiuxiu559/portunus/contents/.github/workflows --jq '.[].name'` 的输出必须含 `release.yml`。缺失（或 `gh run list --workflow=release.yml` 返回 404）意味着 tag 推上去 CI 静默不触发——先解决这个，再谈发版。
4. `gh auth status` 确认 gh CLI 可用；不可用则提示用户 `gh auth login`（可建议用 `! gh auth login` 在会话内跑）。
5. `go test ./...` 快速回归。测试红了就停下，发版流程终止——带病发版比不发版更糟。
6. **主进程改动必须冒烟**：若本次发版包含 `web/src/main/**` 的改动，提醒用户先跑一次 `pnpm run dev` 确认 app 能正常启动。`node --check` 只查语法（查不出 TDZ/引用顺序错误，v0.1.1 坏包根因），`vite build` 只覆盖渲染层——主进程的运行时错误没有任何现有检查能兜住。

## 阶段 1：定版本号

1. 读 `web/package.json` 的 `"version"`，向用户报当前版本。
2. 若用户请求里已经指定了版本号（如「发一版 0.2.0」），直接采用，不要再问一遍——重复确认已经明确的事是在浪费用户耐心。否则问用户要哪种：patch（修 bug）/ minor（新功能）/ major（破坏性），或直接指定版本号。这是用户的决定，不要自作主张。
3. 用 Edit 修改 `web/package.json`，提交：`chore(release): vX.Y.Z`。
4. 顺带确认 `web/src/renderer/pages/Settings.jsx` 没有硬编码版本号残留（版本号应从 `app.getVersion()` 读；若有硬编码，提醒用户一并处理——但不要默默改 UI 代码）。

## 阶段 2：推送发版【确认闸 1】

push tag 会立刻触发 CI 并最终把包发给客户，属于外向且难撤销的操作。先向用户展示摘要等确认：

```
版本：X.Y.Z（原 A.B.C）
tag：vX.Y.Z
待推送提交：<本地领先 origin/main 的提交清单>
将构建：Windows NSIS (x64) + macOS DMG/ZIP (arm64)
产物去向：GitHub draft Release（publish 后对客户可见）
```

若发现以下任何一项，必须在摘要里作为风险一并披露，解决之前不推 tag：
- 本地与远端历史 **diverged**（push 会被 non-fast-forward 拒绝，或推上去的不是单一事实源）；
- **远端默认分支没有 `release.yml`**（tag 推上去 CI 静默不触发）；
- 本地/远端**已存在同名 tag**——先用 `git rev-list -1 vX.Y.Z` 和 `git ls-remote --tags origin` 核对其指向，指向不对就删掉重打，来路不明的 tag 要向用户点明。

用户明确同意后才执行：

```bash
git push origin main
git tag vX.Y.Z
git push origin vX.Y.Z
```

## 阶段 3：盯 CI（自动执行）

1. `gh run list --workflow=release.yml --limit 1` 找到本次 run，`gh run watch <run-id> --exit-status` 跟踪。整个矩阵构建约 10-20 分钟，期间告知用户进展即可。
2. 失败排查（按命中率排序，前三条均为 v0.1.0 首发实测踩过）：
   - **构建步骤报 `VAR=x is not recognized as a cmdlet`** → Windows runner 默认 PowerShell，不认 bash 的 `VAR=x cmd` 前缀语法；环境变量必须写进 step 的 `env:` 块。
   - **release job 报 secondary rate limit / asset already exists (race condition)** → 上传列表混入了 Electron 运行时杂散文件（locale.pak 等），且 win/mac artifact 有同名小文件互相覆盖触发重试风暴；只上传发版必需资产（exe/dmg/zip/blockmap/latest*.yml），排除 `*unpacked*`。
   - **重发时 draft 里有上一轮遗留的杂散资产** → softprops 对已存在的 draft 只追加不清理；重发前先清点资产数量，多余的手动删（`gh api -X DELETE repos/{owner}/{repo}/releases/assets/{id}`，加 sleep 防限流）。
   - **release job 报 403 / resource not accessible** → 仓库 Settings → Actions → General → Workflow permissions 被锁死，需允许写；workflow 内已声明 `contents: write`，个人仓库默认没问题。
   - **构建失败** → `gh run view <run-id> --log-failed` 看哪个平台挂了。常见：`pnpm install --frozen-lockfile` 与 pnpm-lock.yaml 不匹配、Go 编译错误。
   - **release job 报 `cp: cannot stat 'release-assets/...': No such file or directory`** → 固定名副本通配符与实际产物名不符。electron-builder 的 `${arch}` 各平台输出不一致：win→`x64`、mac→`arm64`、**linux→`x86_64`**——改 `artifactName` 或写通配符时以构建日志里 `building target=... file=...` 的实际产物名为准，别按统一规则想当然（v0.2.1 实测踩过：本地只验 mac，linux 到 CI 才暴露）。
   - **盯 CI 用 `gh run watch --exit-status | tail` 这类管道会吞失败退出码** → 管道的退出码取自最后一个命令，构建失败也显示"全绿"。watch 直跑不接管道，或输出重定向落文件后再读退出码（v0.2.1 实测踩过）。
   - **mac 报签名错误** → 不应发生（workflow 已设 `CSC_IDENTITY_AUTO_DISCOVERY: false`），发生说明 workflow 被改过。
3. 修复后重发：删掉远端 tag 重新打（`git push origin :refs/tags/vX.Y.Z && git tag -d vX.Y.Z`），从阶段 2 闸 1 重新走。CI 支持手动触发（workflow_dispatch）做纯构建演练——不出 release，产物在 Actions 页面下载。

## 阶段 4：draft 验收【确认闸 2】

`gh release view vX.Y.Z --json isDraft,assets` 核对产物齐全。预期资产（文件名以实际输出为准，关键看**类别**全不全）：

| 类别 | 用途 |
|---|---|
| Windows 安装包（`*.exe`，NSIS） | 客户首次安装 + win 自动更新 |
| `latest.yml` + `*.blockmap` | **Windows 自动更新的必需品**，缺了 win 客户端检测不到更新 |
| macOS DMG（`*.dmg`） | 客户首次安装 / mac 手动更新 |
| macOS ZIP（`*.zip`） | 将来 mac 签名后自动更新用；另有固定名副本 `Portunus-mac-arm64.zip` 供 README 免绕命令直装 |
| `latest-mac.yml` | mac 更新元数据 |
| Linux AppImage（`*.AppImage`） | Linux 分发；固定名副本 `Portunus-linux-x64.AppImage` 供 README 免绕命令 |
| `latest-linux.yml` | linux 更新元数据 |

向用户展示清单，建议（尤其首次发版）下载安装包本机实测。用户确认后发布：

```bash
gh release edit vX.Y.Z --draft=false
```

## 阶段 5：收尾

发布完成后向用户交代客户端获取更新的方式：

- **Windows（已装）**：下次启动自动检测 → "下载更新" → "安装并重启"，全自动。
- **macOS（已装）**：检测到新版后按钮是"去下载"，浏览器打开 Releases 页手动覆盖（未签名降级路径；买了 Apple 证书切全自动时改动点在 `web/src/main/updater.js` + `useUpdater.js`）。
- **新客户**：Releases 页下载安装包。

提醒：镜像兜底（`PORTUNUS_UPDATE_MIRROR`，见 `web/src/main/updater.js`）尚未配置，国内客户从 GitHub 下载百兆安装包可能慢；若客户反馈下载体验差，再接镜像方案。
