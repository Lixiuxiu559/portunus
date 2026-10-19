# 桌面应用更新分发

Portunus 默认从腾讯云 COS 获取更新，检查更新或下载安装包失败时自动切换到 GitHub Releases。应用不提供更新源设置。

- COS 根地址：`https://portunus-1321735248.cos.ap-shanghai.myqcloud.com/releases/`
- GitHub 备用源：沿用 `web/package.json` 中的 `build.publish`。
- 每次独立检查都先访问 COS；COS 返回“无更新”时，不额外检查 GitHub。
- COS 下载失败后先重新检查 GitHub，只有相同版本可用时才继续下载。
- 两个源都失败时才向界面报告错误。旧版应用需要先通过 GitHub 升级到包含 COS 默认源的新版本。

## 从本地同步正式版本

GitHub Actions 继续负责构建安装包并生成草稿 Release；人工确认并发布后，在本地运行同步脚本。项目不再自动从 GitHub Runner 上传 COS。

环境要求：Python 3.11+、GitHub CLI（`gh`）。脚本将 Python 依赖安装到系统临时目录中的独立虚拟环境。

```bash
# 在仓库根目录运行，默认同步 GitHub 最新正式版
bash scripts/sync-cos.sh

# 指定当前正式版标签
bash scripts/sync-cos.sh --tag v0.4.0
```

脚本会下载该 Release 的全部安装文件、校验更新清单，然后上传 COS。首次运行时，在终端按隐藏提示输入 COS SecretId 和 SecretKey；凭证仅用于本次进程，不保存到文件。

也支持从环境变量 `COS_SECRET_ID` / `COS_SECRET_KEY`，或 `TENCENTCLOUD_SECRET_ID` / `TENCENTCLOUD_SECRET_KEY` 读取凭证。GitHub Secrets 中的密钥不能通过 API 取回到本地；无需将密钥发送到聊天或提交到仓库。

上传账号需要具备此桶的对象上传、分块上传与相关查询权限，包括 `PutObject`、`InitiateMultipartUpload`、`UploadPart`、`CompleteMultipartUpload`、`ListMultipartUploads`、`ListParts` 等；分块任务列表查询需授权对应桶，上传权限限定到 `releases/*`。

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

更新清单逐个发布，此阶段失败可能导致不同平台暂时处于不同版本，重新运行脚本补齐即可。旧安装包不会自动删除，需按存储用量自行保留或清理。

## 验证

同步脚本测试：安装 `scripts/cos-requirements.txt` 后运行：

```bash
python -m unittest discover -s scripts -p 'test_*.py'
```

客户端更新逻辑：在 `web/` 目录运行 `pnpm test`。离线测试不替代正式安装包的真实下载与安装验证。
