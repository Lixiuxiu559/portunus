# Portunus 产品介绍（宣传底稿）

> 用途：各平台宣传的母版。先读「定位与口径」，再按平台取用对应文案。
> 链接口径：仓库 https://github.com/Lixiuxiu559/portunus ｜ 下载 https://github.com/Lixiuxiu559/portunus/releases/latest
> 口径注意：不说「开源」（仓库未附 LICENSE）、不承诺价格、不提内部端口（客户无感）。

---

## 一、定位与一句话

**一句话**：把你手里散落的 LLM API Key，变成一个本地端点。

**定位**：给个人开发者的 LLM 网关桌面应用。装完即用，无需 Docker、无需服务器。

**对标差异**（克制版说法）：one-api / new-api 这类自建网关面向服务器和团队，要部署要运维；Portunus 是装在个人电脑上的桌面应用——双击安装，打开就能用。不是替代关系，是两种使用形态。

---

## 二、痛点 → 解法（长文骨架）

你大概也有这样的经历：

- 手里一堆渠道的 Key：官方的、云厂商的、第三方代理的。Claude Code 用一个、Codex 用一个，换渠道要挨个改配置文件。
- 某个渠道半夜挂了，第二天下午才发现，一下午的活全耽误。
- 月底一看账单，完全不知道钱花在哪个模型、哪个渠道上。
- 想让 Claude Code 用上国产模型 / 想让 Codex 走 Anthropic 协议——协议对不上，改不动。

Portunus 的解法：

1. **渠道统一接入**：OpenAI Chat / Responses、Anthropic、Gemini 三家协议全收，填个 Key 就录入，模型列表自动拉取。
2. **协议互转**：客户端和上游协议不一致时自动转换，SSE 流式透传、流式工具调用全协议可用。模型名加 `-thinking` 后缀即可开关思考模式。
3. **分组路由**：多个模型编成一个组对外暴露，手动指定 / 轮询 / 故障转移三种策略，上游假死快速切换，失败重试 + 熔断。
4. **Claude Code / Codex 一键接入**（王牌）：在应用内选好分组，服务地址和令牌自动写进 `~/.claude/settings.json` / `~/.codex/config.toml`——改前自动备份，支持一键回滚。按槽位映射模型，连 1M 上下文槽位都有。
5. **四维计费**：输入 / 输出 / 缓存读 / 缓存写 token 分开计价，内置主流模型默认价，调用日志记到每次请求（协议、模型、分组、耗时、费用、request_id）。

---

## 三、三步上手

1. 下载安装：[GitHub Releases](https://github.com/Lixiuxiu559/portunus/releases/latest)（macOS Apple Silicon / Windows x64）
2. 添加渠道：填 Key，保存即自动拉取模型列表
3. 一键接入：客户端页选分组，Claude Code / Codex 配置自动写好

---

## 四、适合谁 / 不适合谁（诚实边界）

**适合**：同时用多个 LLM 渠道、重度使用 Claude Code / Codex 等 CLI 工具的个人开发者；想搞清楚自己 AI 花费的人；想在国内稳定用上各家模型协议互转的人。

**不适合**：需要给整个团队 / 多台服务器共用网关的运维场景——那是自建网关软件的主场（Portunus 也提供局域网监听模式，但产品定位是个人桌面）。

---

## 五、各平台分发文案（直接复制）

### ① 即刻 / 朋友圈（短版）

> 做了个小工具：Portunus —— 把你手里散落的 LLM API Key 变成一个本地端点。
>
> 起因很简单：Claude Code、Codex 各配各的渠道，换 Key 要挨个改配置，渠道挂了半天才发现。现在统一接进 Portunus：OpenAI / Anthropic / Gemini 协议自动互转，分组路由故障转移，Claude Code 和 Codex 一键接入（自动写配置、可回滚），每次调用的 token 和费用都记着。
>
> 最重要的是：它是个桌面应用。双击安装就能用，不用 Docker、不用服务器。竞品都在卷服务端，我想先把「个人开发者装完即用」这件事做好。
>
> macOS / Windows 都有，v0.1.0 刚发：
> https://github.com/Lixiuxiu559/portunus/releases/latest
>
> 求反馈，特别是 Claude Code 重度用户。

### ② V2EX（第一人称叙事开头）

标题备选：
- 「写了个桌面端 LLM 网关：多渠道聚合 + Claude Code/Codex 一键接入，v0.1.0 发布」
- 「分享一个自己写的 LLM 聚合工具 Portunus：双击安装即用，不用 Docker」

正文开头（接长文骨架第二节）：

> 之前管理自己的 LLM 渠道用的是 one-api，功能很全，但每次重装系统 / 换机器都要重新起 Docker、导配置，而且我只是一个人用，跑个常驻服务有点杀鸡挪用。更烦的是 Claude Code 和 Codex 的配置文件格式不一样，换渠道要分别改。
>
> 所以写了 Portunus：……（接第二节 1-5 条）

结尾：

> v0.1.0 刚发，macOS（Apple Silicon）/ Windows 都有。应用内自动更新已经打通，后面小版本迭代直接在 app 里升级。技术栈：Go + Gin + GORM（SQLite）/ Electron + React。求拍砖，尤其欢迎 Claude Code 重度用户提需求。

### ③ 掘金 / 公众号（长文大纲）

标题备选：
- 《我用 Go + Electron 写了个本地 LLM 网关：给 Claude Code 管理所有渠道》
- 《别再手改 Claude Code 配置了：一个桌面应用管住你所有的 LLM Key》

大纲：痛点场景（第二节）→ 架构（Go sidecar + Electron，纯 Go SQLite 无 CGO，应用内自动更新链路）→ 核心功能逐个演示截图（渠道录入 / 分组路由 / 客户端一键接入 / 日志计费）→ 协议互转细节（流式透传、工具调用映射、-thinking）→ 三步上手 → roadmap。

### ④ 小红书（风格要点）

- 标题：「Claude Code 用户的 Key 管理神器」「再也不用手改 Codex 配置了」
- 排版：短句 + emoji，每个功能一行，截图 3-4 张（渠道页 / 客户端页 / 日志页）
- 结尾引导：评论交流 + 主页/置顶放链接

### ⑤ 英文一句话（X / HN）

> Portunus — a desktop LLM gateway for individual devs: aggregate all your API keys behind one local endpoint, one-click onboard Claude Code & Codex, per-model billing. No Docker, no server. Install and go.
> https://github.com/Lixiuxiu559/portunus

---

## 六、发布检查单

- [ ] 每篇配 2-3 张应用截图（浅色 + 深色主题各一）
- [ ] 链接统一用 releases/latest（不用带版本号的链接，老链接会过时）
- [ ] 提及 mac「已损坏」解法的地方附 README 链接，不贴长命令
- [ ] 各平台发布后 48h 内盯评论区，反馈记进 GitHub Issues
- [ ] 数据点（如「已支持 X 个渠道」「N 次下载」）有真实数据再加，宁缺毋滥
