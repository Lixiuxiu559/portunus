# 桌面应用更新分发

Portunus 默认从腾讯云 COS 获取更新，检查更新或下载安装包失败时自动切换到 GitHub Releases。应用不提供更新源设置。

- COS 根地址：`https://portunus-1321735248.cos.ap-shanghai.myqcloud.com/releases/`
- GitHub 备用源：沿用 `web/package.json` 中的 `build.publish`。
- 每次独立检查都先访问 COS；COS 返回“无更新”时，不额外检查 GitHub。
- COS 下载失败后，先重新检查 GitHub，只有相同版本可用时才继续下载。备用源版本不一致时提示重新检查，不替换用户已确认的版本。
- 两个源都失败时才向界面报告错误。旧版应用仍使用其原有更新源，需要先通过 GitHub 升级到包含本改动的版本。

## 自动同步正式版本

发布流程：推送 `v*` 标签 → `Release` 工作流构建安装包并创建草稿 → 人工确认后点击 Publish release → `Sync release to COS` 工作流自动上传。

`.github/workflows/sync-cos.yml` 监听 `release.published`。草稿和预发布版本不触发正式源同步；只允许同步 GitHub 当前最新正式版，避免旧版本覆盖更新清单。保留现有的人工发布确认，不需要每次手动上传文件。

### 一次性配置

在 GitHub 仓库的 **Settings → Secrets and variables → Actions → New repository secret** 中添加：

| Secret | 内容 |
| --- | --- |
| `COS_SECRET_ID` | 腾讯云上传账号的 SecretId |
| `COS_SECRET_KEY` | 同一账号的 SecretKey |

使用可写入 `portunus-1321735248` 桶 `releases/*` 的腾讯云子账号密钥。脚本使用官方 COS Python SDK 的分块上传，需要对象上传、分块上传及相关查询权限（例如 `PutObject`、`InitiateMultipartUpload`、`UploadPart`、`CompleteMultipartUpload`、`ListMultipartUploads`、`ListParts`、`AbortMultipartUpload`）；其中分块任务列表查询属于桶级操作，需授权对应存储桶，上传权限限定到 `releases/*`。具体授权配置参见[腾讯云 COS 的 CAM 文档](https://cloud.tencent.com/document/product/436/11714)。密钥仅放在 GitHub Secrets 中，不放入客户端、仓库文件或聊天。

目标桶、地域和前缀已固定在 `.github/scripts/sync_cos.py` 中，与客户端默认源一致，无需额外设置域名变量。

### 同步顺序

1. 从正式 GitHub Release 下载 EXE、DMG、ZIP、AppImage、`.blockmap` 和 `latest*.yml`，不重新构建。
2. 确认三个平台的更新清单齐全、版本号一致，并验证清单引用的安装包存在、SHA-512 和文件大小匹配。
3. 上传所有安装包和 `.blockmap`，任一文件上传失败则停止，不更新清单。
4. 再次确认该版本仍是 GitHub 最新正式版，然后上传三个 `latest*.yml`，设置 `Cache-Control: no-cache`。

同步任务串行执行，不取消正在上传的任务。上传失败后可以重试；COS 原有文件不会被自动删除。跨平台清单逐个发布，若此阶段失败，部分平台可能已更新，需重新运行同步补齐。

### 首次补同步与失败重试

当 GitHub Secrets 配置完成、工作流已合入默认分支后，可到 **Actions → Sync release to COS → Run workflow**，选择默认分支并填写最新正式版标签（例如 `v0.4.0`），将当前版本补同步到 COS，无需再发布一次。

后续任务失败可点击 Re-run failed jobs，或使用上述手动入口。历史版和预发布版不会覆盖正式更新源。

如果将来改为由其他工作流通过 `GITHUB_TOKEN` 直接发布正式 Release，需要在该工作流中显式调用同步任务；此类事件通常不会再触发新的工作流。当前流程由你在 GitHub 页面手动发布草稿，不受此限制。

目录示例（实际文件名以 Release 为准）：

```text
releases/
  Portunus-<版本>-windows-x64.exe
  Portunus-<版本>-windows-x64.exe.blockmap
  Portunus-<版本>-mac-arm64.dmg
  Portunus-<版本>-mac-arm64.zip
  Portunus-<版本>-mac-arm64.zip.blockmap
  Portunus-<版本>-linux-x86_64.AppImage
  latest.yml
  latest-mac.yml
  latest-linux.yml
```

COS 使用公开读、私有写的访问方式。客户端仅访问永久文件地址，不携带临时签名或 COS 密钥。应用内没有 COS 文件上传权限。

## 验证

同步脚本验证：先安装 `.github/scripts/cos-requirements.txt`，再运行 `python -m unittest discover -s .github/scripts -p 'test_*.py'`。测试覆盖校验失败、安装包上传失败、发布过程中出现新版本等场景，不连接真实 COS。

在 `web/` 目录运行 `pnpm test`。`src/main/update-service.test.js` 覆盖默认源、检查与下载故障切源、版本不一致、并发操作、错误转发和窗口重建。离线测试不替代正式安装包的真实下载与安装验证。
