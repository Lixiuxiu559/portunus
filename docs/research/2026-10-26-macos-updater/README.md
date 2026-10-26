# macOS 独立更新改造验证

日期：2026-10-26。开发分支 `dev`，代码基线 `87147c3`，结果包含工作区更新器与此前已授权的安装包压缩改动。没有创建发布版本、上传 COS 或替换本机正式安装。

## 实现

继续使用 electron-updater 6.8.9 的检查、ZIP 下载、缓存与进度能力，覆盖 macOS 的下载完成和安装阶段，不交给 Squirrel.Mac。独立 Go helper 使用内置 Ed25519 公钥验证 ZIP 的版本、架构、大小及 SHA-512 签名；在同一文件系统准备应用，限制解压路径与符号链接，清理隔离属性并进行 ad-hoc 签名。安装时等待旧应用退出，备份并替换应用，启动新版。后端 ready 且页面成功提交后通过受限主 frame IPC 确认，超时则停止新版进程组并恢复旧应用。

macOS 普通退出不安装。同一待安装包再次准备时重新验证并复用，避免重复解压占空间。内核文件锁在 helper 被强制结束后自动释放；只读锁 FD 支持其他有安装目录权限的账号。

发布构建自动生成 `.zip.sig`，CI 缺少签名 Secret 时失败；COS 同步要求新版 mac ZIP 携带匹配的签名元数据，签名与包均先于更新清单上传。私钥留在本机受限文件中，仓库和 helper 只有公钥。

## 真实隔离验收

使用本机真实 Electron 42.3.3、当前 Go 后端、原生 helper 与本机 HTTPS 更新源；每个场景独立应用副本、缓存、数据库和随机端口。测试签名使用与 helper 内置公钥匹配的独立更新密钥，无 Apple Developer ID。仅验收 fixture 的 Electron 会话信任本机自签 TLS 证书，生产证书校验未放宽。

| 场景 | 实际结果 |
| --- | --- |
| 正常更新 | fixture 0.5.0 → 0.6.0；下载 ZIP 与 `.sig`，验签、解包、替换、启动后端和页面、preload IPC 确认；结果 installed，应用版本 0.6.0，无 quarantine 属性 |
| 同时篡改 ZIP 与下载清单哈希 | electron-updater 传输哈希校验可通过，helper 独立签名/摘要校验仍拒绝，旧应用未退出或替换 |
| 页面加载但未发就绪确认 | 等待真实 90 秒超时后停止新版进程组，恢复并重新启动 0.5.0，结果记录“已恢复旧版” |
| 最终只读锁及准备包复用调整 | 重新构建 helper，再跑正常更新；重复 prepare 返回同一事务目录，最终 installed |

三场景原始结果：系统临时目录中的 `portunus-mac-update-smoke-hgUxWp/verification.json`。最终复用验证：`portunus-mac-update-smoke-mjO1pw/verification.json` 和 `success/reuse.json`。临时目录属于操作系统缓存，不作为长期交付路径；重跑命令见下文。

这些是有真实原生进程、真实后端和 IPC 的 Electron fixture，页面为测试页面，不是完整产品的正式安装升级；React ReadyPage 的接入经过代码评审、类型检查及实际 renderer 打包，不能将 fixture 等同于产品页面端到端验收。

## 自动化与构建验证

- `go test ./...`、`go vet ./...`：通过。
- `go test -race ./cmd/portunus-updater`：通过，覆盖签名/版本/架构/篡改拒绝、解压边界、framework 链接、替换失败恢复、新版启动失败/错误确认回滚，以及 SIGKILL 后重新获取只读锁。
- `cd web && pnpm test`：201 项通过；`pnpm run typecheck`、`pnpm run build:renderer` 通过。
- Python COS 同步测试：28 项通过，包括新版缺签名或签名元数据不匹配时拒绝发布清单。
- electron-builder 真实生成 macOS ZIP、DMG、blockmap、更新清单及 `.zip.sig`；最终 ZIP 131,467,295 字节，DMG 132,039,648 字节。独立检查签名、包摘要、大小、ZIP CRC 和执行权限匹配；原生 helper 对完整产品 ZIP 的验签、解包及代码签名准备也通过（仅临时副本，未启动或安装）。产物仅用于本地验证，版本号仍为 0.5.0，不能覆盖已发布的 0.5.0。验证输出位于 `/tmp/portunus-custom-updater-package/verification.json`。
- 三轴独立只读评审：Correctness 发现并修复页面过早确认、超时遗留锁、替换失败未重启和跨账号锁权限四项，复核关闭；Standards 无 finding；Spec 的恢复重启问题复核关闭。未运行复杂度/突变专项审计，本轮未引入额外审计工具链。

重跑真实验收（macOS，在 `web/`）：

```sh
pnpm run build:go:release
node scripts/mac-update-smoke.mjs                  # 全部三场景，含 90 秒超时
node scripts/mac-update-smoke.mjs success          # 仅正常更新与准备包复用
```

## 发布前条件与边界

1. 在 GitHub 配置 `PORTUNUS_UPDATE_SIGNING_KEY` Secret，值为本机对应私钥 PEM；需要安全备份私钥。本次未自动上传密钥。
2. 0.5.0 老客户端需手动安装一次包含新更新器的版本，服务端无法替换它内置的旧安装流程。
3. 尚未验证真实 COS/GitHub 正式版本升级、其他 macOS 版本、完整产品 UI 升级、跨账号实机升级、断电中断恢复或需提权安装目录。安装器不提权。
4. helper 不写或回滚用户数据库；新版应用自身的数据迁移仍须保持兼容。成功后清理备份和 ZIP，失败应用及诊断留在事务目录供恢复。
