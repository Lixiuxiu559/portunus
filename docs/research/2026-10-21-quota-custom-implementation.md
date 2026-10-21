# 自定义余额查询实施记录

2026-10-21。范围依据[设计与测试用例](./2026-10-21-quota-custom-design.md)。用户取消 C，D 经澄清实施结构化自定义查询。未实现 Sub2API、厂商套餐、AK/SK、OAuth 或 JavaScript 引擎。

## 完成内容

- 后端：custom.balance 适配器、独立配置校验/JSON Pointer 解析、GET/POST 目录能力、精确金额/条件、多项结果、显式换算和原始值。
- 核心集成：类型化配置、无鉴权模式、独立 Token 跨目标保护、配置值比较及空请求头规范化。复用来源/状态表、已有 CAS、取消、缓存与调度，不增新表或依赖框架。
- 前端：独立 CustomQuotaEditor 和 customQuotaModel；复用现有查询入口、草稿生命周期与展示。输入 Token 不回填，目标变更要求重新输入，JSON 语法错误在发请求前提示。
- 测试：新增适配器请求/解析/约束测试，API闭环（预览、保存、查询、只读缓存、秘密隔离、无改动保存），前端配置往返与界面验收。

## 实际验证

| 命令/范围 | 结果 |
|---|---|
| `GOCACHE=/private/tmp/portunus-go-cache go test ./... -count=1 -timeout=120s` | 全部通过；现有 httptest 用例授权使用本机临时端口 |
| `GOCACHE=/private/tmp/portunus-go-cache go test -race ./backend/quota ./backend/api -run 'TestCustom\|TestQuota\|TestService\|TestExecute\|TestAdapter' -count=1 -timeout=90s` | 通过 |
| `GOCACHE=/private/tmp/portunus-go-cache go vet ./...` | 通过 |
| `GOCACHE=/private/tmp/portunus-go-cache go build -o /private/tmp/portunus-custom-quota-check .` | 通过 |
| `cd web && pnpm test` | 125/125通过 |
| `cd web && pnpm run build:renderer` | 通过 |
| `git diff --check` | 通过 |

Go build 有现有模块版本元数据缓存写入沙箱提示，最终退出码0；Node module 类型提示和 Vite chunk 体积提示仍为非阻断项。未增依赖，未运行线上 CI。量化复杂度/突变测试无现成工具，未安装或声称通过。

## 独立评审

固定代码点 `6df6c613120fa2a04d123d8107151f7b2100813a`，本轮只审 custom 与相关集成，工作区仍有此前未提交改动。

- Correctness：发现 null 条件被默认值覆盖、大数条件经 JS Number 舍入，已改 `equals_json` 原文保存并在前后端补往返/匹配测试；独立定点复核通过。额外规范化 nil/空请求头，避免无鉴权来源无改动保存误递增版本，API测试通过。
- Spec：补齐 POST 仅能配置只读接口的界面提示；独立定点复核通过，未解决项0。
- Standards：未发现确定硬性违规或有证据的结构 smell。适配器不依赖渠道/网关；UI配置、序列化与请求生命周期分离。

## 界面验收和边界

[独立 mock 验收记录与截图](./2026-10-21-quota-custom-ui/README.md)覆盖字段输入、预览不保存、非法JSON不发送、无鉴权、普通保存不查询、多项增删、目标变更及1024×576明暗布局。验证进程和浏览器空间已关闭。

API集成使用真实quota.Service及注入HTTPDoer，校验完整方法/URL/头/请求体、单次查询、精确金额与Original，不连接外部站点。浏览器mock只证明UI请求形状，不能替代适配器或真实厂商验证。未使用真实账号、未复制正式数据，测试不访问13061。

## 开发版交付

使用已有隔离开发版流程构建并重启 Portunus Dev；只读检查 custom.balance 注册。保留开发数据，不操作正式版。开发版接口就绪不代表真实用户自定义站点的权限与字段映射已验证。

新增设计、实施及界面证据文件当前未跟踪，所有本轮改动未提交。
