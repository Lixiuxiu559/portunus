# 开发调试版与正式版隔离

桌面开发、未打包运行和打包预览统一使用 **Portunus Dev**。正式版保留原有身份、数据路径和更新行为，不需要退出即可开发。

| 项目 | 正式安装版 | 开发调试版 |
| --- | --- | --- |
| 名称 | Portunus | Portunus Dev（Dock 显示 DEV） |
| Bundle ID | com.portunus.app | com.portunus.app.dev |
| 后端端口 | 13060 | 13061 |
| macOS 数据根目录 | 沿用原有目录，当前为 `~/Library/Application Support/@portunus/web` | `~/Library/Application Support/Portunus Dev` |
| 数据库与令牌 | 沿用现有数据 | 独立 `data/`；首次启动新库、新令牌 |
| 浏览器会话 | 沿用原有路径 | `session/` |
| Electron 日志 | 沿用原有路径 | `logs/` |
| 后端工作目录 | 保留既有行为 | 独立 `backend/`，不读仓库配置 |
| 更新 | 正式发布源 | 禁用检查、下载和安装，不加载正式更新器 |
| 构建产物 | `web/dist-electron/` | 固定 `web/dist-dev/` |

单实例锁在设置独立 `userData` 后获取。热开发和打包 Dev 共用一个 Dev 身份；两种 Dev 启动方式之间需先退出当前 Dev，正式版可以一直运行。Dev 不自动读取、迁移、复制正式数据库、上游密钥、设置或缓存。需要真实渠道时，在 Dev 中单独添加测试配置。

## 启动方式

在 `web/` 执行：

```bash
pnpm dev            # 编译 Go → Vite → Electron，主进程管理自己的 Dev 后端
pnpm build:dev      # 编译后端、渲染层，构建独立 Portunus Dev 目录包
pnpm preview:dev    # 启动该目录包，不安装到 /Applications
pnpm test           # 离线单元测试
pnpm test:e2e       # 仅针对 Dev 产物与 13061 的启动检查；先退出已有 Dev
```

macOS 预览路径：`web/dist-dev/mac-arm64/Portunus Dev.app`（Intel 为 `mac/`）。正式安装包仍用 `build:mac` / `build:win`，不要把开发产物拖入 `/Applications` 覆盖正式应用。

独立运行 `go run .` 是命令行后端模式，仍保留其默认 3060 与仓库 `data/`，不属于桌面 Dev；Vite 代理统一指向桌面 Dev 的 13061。

## 失败与停止边界

- 13061 已占用：拒绝启动后端，不接管已有服务，也不关闭占用进程。
- 健康检查要求实例标识匹配且自己的子进程仍存活，避免误认别的服务。
- Dev 启动失败：停在错误页，不让业务界面请求已有服务。
- 退出或切换监听范围：只停止或重启当前主进程创建的子进程，不按进程名批量结束应用。
- 不通过 `HOME`、任意数据目录覆盖参数或正式数据库副本实现隔离。
- 端到端脚本使用 Dev 数据目录（正常启动检查不写业务配置）；不会重写 HOME，也不会停止正式版。


## 持续集成与发布检查

配置提交：`337e216`。工作流见 [CI](../../.github/workflows/ci.yml) 和 [Release](../../.github/workflows/release.yml)。

- 分支 push、PR 和手动触发 CI 时，在 Ubuntu 上执行两个独立任务：`Go checks`、`Web checks`。PR 更新会取消同组旧检查。
- Go 版本取自 `go.mod`；执行 `go test ./... -count=1`、`go vet ./...`、`go build ./...`。
- 前端使用 Node 24、pnpm 10 与依赖缓存；在 `web/` 执行 `pnpm install --frozen-lockfile`、`pnpm test`、`pnpm run build:renderer`。仅构建 renderer，跳过 Electron 运行时下载，不启动桌面应用。
- `v*` 标签及手动 Release 均先复用上述 CI，全部通过才进入三平台安装包构建。标签创建草稿 Release；手动 Release 只构建产物。草稿仍需人工确认发布。
- CI 不加载用户配置、正式/Dev 数据或上游密钥，不执行真实生成调用。桌面启动检查 `pnpm test:e2e`、跨设备 LAN 和真实上游验证单独进行，不能以 CI 通过代替。

本地已通过工作流 actionlint 检查、Go 全量测试/静态检查/构建，以及临时干净目录锁文件安装后的 94 项前端测试和 renderer 构建。尚未验证 GitHub 线上首次运行，未修改远端分支保护或 ruleset。构建大 chunk 和 Node module type 提示仍为现有非阻断项。

若需要禁止失败的 PR 合并，待远端产生实际检查结果后，将 `Go checks`、`Web checks` 设为必需状态检查。新增工作流本身不会启用合并限制。
