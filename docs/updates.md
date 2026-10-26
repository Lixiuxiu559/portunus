# 桌面应用更新分发

Portunus 默认从腾讯云 COS 获取更新，检查更新或下载安装包失败时自动切换到 GitHub Releases。应用不提供更新源设置。

- COS 根地址：`https://portunus-1321735248.cos.ap-shanghai.myqcloud.com/releases/`
- GitHub 备用源：`Lixiuxiu559/portunus` 的正式 Releases，与打包发布配置保持一致。
- 每次独立检查都先访问 COS；COS 返回“无更新”时，不额外检查 GitHub。
- COS 下载失败后先重新检查 GitHub，只有相同版本可用时才继续下载。
- 两个源都失败时才向界面报告错误。旧版应用需要先通过 GitHub 升级到包含 COS 默认源的新版本。

## 客户端更新体验

三个平台启动后检查更新，发现新版后在后台自动下载。下载完成后，所有页面均显示「重启以更新」，设置页也提供同一入口。下载期间可以继续使用应用；下载失败后可在设置页重试。更新状态由主进程保存，切页或重建窗口不会丢失下载进度或待安装版本。

- Windows：点击「重启以更新」后静默运行 NSIS 更新并启动新版，不显示安装向导。首次安装仍可选择目录；已有安装需要管理员权限时，Windows 仍可能显示 UAC 授权。
- macOS：ZIP 下载仍由 electron-updater 管理，安装阶段使用独立 Go helper，不经过 Squirrel.Mac。helper 校验内置 Ed25519 公钥对应的更新签名，在可写的应用父目录准备新版，处理隔离属性并进行 ad-hoc 签名。点击「重启以更新」后等待旧应用及其后端退出，再备份旧版、替换并启动新版。新版后端 ready 且 React 页面首次挂载后通过受限 IPC 握手才确认成功；90 秒未确认则停止新版进程组并恢复旧应用。不要求 Apple Developer ID；首次安装的系统放行仍由用户完成。
- Linux：当前发布 AppImage，更新器替换 AppImage 后重新启动。需要从 AppImage 运行，且文件及所在目录允许当前用户写入。

服务保留 `autoDownload = false`，由自己的检查流程自动调度下载，以确保后台下载同样经过 COS → GitHub 同版本回退。下载完成前拒绝安装；下载中或已就绪时，重复检查不会切换更新源或更换待安装版本。Windows/Linux 保留正常退出时自动安装行为；macOS 仅在点击「重启以更新」后替换，普通退出不会安装。Windows 关闭窗口到托盘不等于退出应用。

## macOS 独立更新签名

普通退出后，下一次启动会重新验签并复用同版本、同摘要的准备包，避免反复解压留下重复应用副本。只有准备包的签名、应用身份和代码签名仍有效才复用。

首次接入需要手动安装一次包含新 helper 的版本；已发布的 0.5.0 使用旧更新器，不能通过服务端清单让它自动获得新的安装能力。新更新器只接受比当前版本更新的稳定三段版本号、当前 CPU 架构和 `com.portunus.app` 应用标识；当前支持发布的 arm64 包以及未来同架构的 x64 单架构包，不接受 universal 包。

- 仓库 `cmd/portunus-updater/public-key.pem` 是内置验证公钥。私钥仅保存在本机 `~/.config/portunus/update-signing-key.pem`（权限 0600），不进入仓库、安装包或日志；需要单独安全备份。更换公钥需要先让现有客户端通过旧密钥可信地接收新公钥，不能直接替换后继续发布。
- `build:mac` 的 `afterAllArtifactBuild` 自动生成 `<版本ZIP>.sig`；缺失私钥或私钥与公钥不匹配时构建失败。私钥可通过 `PORTUNUS_UPDATE_SIGNING_KEY`（PEM 文本）或 `PORTUNUS_UPDATE_SIGNING_KEY_FILE` 指定。独立签名命令：`node web/scripts/sign-mac-update.mjs <ZIP路径> <版本> arm64`。
- GitHub Release workflow 需配置 `PORTUNUS_UPDATE_SIGNING_KEY` 仓库 Secret，值为同一私钥的 PEM 文本。本次本地改造不自动上传私钥；配置完成前 CI 的 macOS 发版会明确失败。
- 签名绑定协议标识、应用 ID、版本、架构、文件大小和 ZIP 的 SHA-512。更新源提供的 SHA-512 用于传输完整性检查，独立签名防止同时篡改包和清单。更新器只通过 HTTPS 下载签名文件，缺少签名也会触发原有同版本 GitHub 回退。
- COS 脚本对 0.5.0 之后的 mac ZIP 要求存在匹配的 `.sig` 元数据，签名文件随安装包先上传，最后才发布更新清单。密码学验证在客户端 helper 中执行；上传脚本检查签名字段格式与包摘要，不代替客户端验签。

安装器只使用当前用户权限，不调用 sudo、不更改 Gatekeeper 全局设置。应用父目录必须可写；从只读 DMG 运行时先移到可写安装目录。准备或预检失败会留在当前应用中显示错误，事务诊断保存在应用旁的 `.portunus-update-*/install.log` 和 `result.json`。失败后旧应用无法自动启动时，可从原路径手动打开；备份恢复失败时日志会给出 `Previous.app` 路径。

成功后清理旧应用备份与 ZIP，保留小型事务日志；失败事务保留失败应用供诊断。应用旁保留一个小型锁文件，内核锁随 helper 退出自动释放，不需删除该文件。更新器只替换 `.app`，不备份或回滚用户数据库。新版自身执行的数据迁移若不兼容旧版，应用回滚不能撤销迁移，发布前仍需验证兼容性。异常断电或 SIGKILL 中断替换后的事务恢复尚未自动化，不能将本轮结果视为这些情况已通过。

## 从本地同步正式版本

GitHub Actions 先复用 [CI 质量检查](../.github/workflows/ci.yml)，全部通过后才构建安装包；标签触发生成草稿 Release，手动触发只构建产物。人工确认并发布后，在本地运行同步脚本。项目不再自动从 GitHub Runner 上传 COS。

环境要求：Python 3.11+、GitHub CLI（`gh`）。脚本将 Python 依赖安装到系统临时目录中的独立虚拟环境。

```bash
# 在仓库根目录运行，默认同步 GitHub 最新正式版
bash scripts/sync-cos.sh

# 指定当前正式版标签
bash scripts/sync-cos.sh --tag v0.4.0
```

脚本会下载该 Release 的全部安装文件、校验更新清单，然后上传 COS。首次运行时，在终端按隐藏提示输入 COS SecretId 和 SecretKey；凭证仅用于本次进程，不保存到文件。

也支持从环境变量 `COS_SECRET_ID` / `COS_SECRET_KEY`，或 `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` 读取凭证。GitHub Secrets 中的密钥不能通过 API 取回到本地；无需将密钥发送到聊天或提交到仓库。

上传账号需要具备此桶的对象上传、分块上传与相关查询权限，包括 `PutObject`、`InitiateMultipartUpload`、`UploadPart`、`CompleteMultipartUpload`、`ListMultipartUploads`、`ListParts`、`GetBucket`、`DeleteObject` 等；分块任务列表查询需授权对应桶，上传和删除权限限定到 `releases/*`，对象列举权限授权到该桶。

### 下载与上传分开执行

```bash
# 只下载并校验，输出缓存目录，不连接 COS
bash scripts/sync-cos.sh --tag v0.4.0 --download-only

# 使用上一步输出的目录上传，目录路径以脚本实际输出为准
bash scripts/sync-cos.sh --tag v0.4.0 --assets-dir /tmp/portunus-cos-releases/v0.4.0
```

自动下载的安装文件保存在系统临时目录的 `portunus-cos-releases/<版本>/` 下，可复用已下载文件。若校验提示文件损坏，删除对应缓存文件后重新下载。也可用 `--assets-dir` 指定自行下载的完整 Release 目录。

### 上传规则

1. 只同步 GitHub 当前最新正式版，拒绝草稿和预发布版。
2. 校验三个平台更新清单的版本号、安装包 SHA-512 和大小。
3. 按大小依次上传安装包及 `.blockmap`，输出分块进度。任何安装包失败，都不发布更新清单。
4. 再次检查版本仍为最新正式版，最后上传 `latest.yml`、`latest-mac.yml`、`latest-linux.yml`，设置 `Cache-Control: no-cache`。

SDK 使用腾讯云官方公网域名 `tencentcos.cn` 上传，客户端通过 `myqcloud.com` 下载，两个域名访问同一存储桶。未启用全球加速。

更新清单逐个发布，此阶段失败可能导致不同平台暂时处于不同版本，重新运行脚本补齐即可。安装包或任一更新清单上传失败时，不执行旧版本清理。

### 自动保留最近 3 个正式版本

安装包和三个更新清单全部上传成功后，脚本列举 COS 的 `releases/` 文件，并结合 GitHub 正式 Release 记录自动清理旧版本：

- 始终保留本次同步版本；其余按正式发布时间，保留 COS 中实际存在的最近两个正式版本。未同步到 COS 的 Release 不占保留名额。
- 删除更早版本的安装包和对应 `.blockmap`，只处理符合 Portunus 平台安装包命名规则的文件。
- 保留 `latest*.yml`、无版本号的固定下载文件、其他文件及无法确认属于正式版的文件。
- 清理前再次确认本次版本仍是 GitHub 最新正式版；列举或版本查询失败时不开始删除。
- 清理失败会报告错误，已发布的新版本仍然有效。重新运行脚本可继续完成清理。

此规则清理的是当前可见对象，不清理未完成的分块任务；若存储桶启用了版本控制，删除旧文件也不等于清除了其历史对象版本。

## 验证

同步脚本测试：安装 `scripts/cos-requirements.txt` 后运行：

```bash
python -m unittest discover -s scripts -p 'test_*.py'
```

客户端更新逻辑：在 `web/` 目录运行 `pnpm test`。离线测试不替代正式安装包的真实下载与安装验证。

独立 helper：`go test ./cmd/portunus-updater`。macOS 实包隔离验收：先在 `web/` 执行 `pnpm run build:go:release`，再运行 `node scripts/mac-update-smoke.mjs`。它创建临时 Electron 应用、本机 HTTPS 更新源、随机端口与独立数据，验证正常更新、包被篡改及启动未确认回滚，不操作正式版或 Dev 安装。验收设计见[macOS 更新测试设计](test-design/2026-10-26-macos-updater.md)。
