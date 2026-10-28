# Portunus 开发知识库

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | 当前仓库的产品边界、公共概念及六个开发知识模块的入口 |
| 事实状态 | 部分核验 |
| 核验基线 | `b44d75a788f8aa5ef0ad024273d4bf5dd8c95c7e`（`dev`）；2026-10-28 工作区 |
| 核验时间 | 2026-10-28 |

## 项目概览

Portunus 是本地运行的多上游 API 聚合网关。渠道保存一个上游账户的连接和凭据，模型目录保存该渠道的可用资源，分组再把这些资源组合成客户端使用的稳定 `model`。Go 后端同时提供管理面 `/api` 和服务面 `/v1`，主要交付形态是 Electron 桌面应用。

当前客户端生成入口是 Chat Completions、Responses 和 Messages；上游协议包括 OpenAI Chat、OpenAI Responses、Anthropic 与 Gemini。Gemini 不是独立的客户端生成入口。分组只支持 `manual`（手动）和 `failover`（固定顺序故障转移），不提供轮询、权重或费用路由。架构知识以本 Wiki 为统一入口。产品定义见 `docs/product/PRODUCT.md`，组件依赖与数据流由下方各模块按当前代码维护。

产品不提供价格配置、默认价格套用、调用费用计算或费用统计；历史价格、货币和费用列仅为存储兼容保留。余额查询只用于观测，不参与选路。模型发现成功不等于真实生成调用通过，一次真实调用成功也不能推广为所有模型和协议均已验证。

## 阅读导航

- [核心概念与跨模块规则](concepts.md)：区分渠道模型与分组名、上游密钥与服务令牌，以及日志、HTTP 尝试和请求关联项。
- [运行入口与公共基础设施](modules/runtime/README.md)：Go 启动装配、配置与数据库、管理接口访问边界、定时维护。
- [渠道、模型与分组](modules/resources/README.md)：连接解析、选择性导入、模型同步、路由候选和删除级联。
- [网关与协议转换](modules/gateway/README.md)：服务鉴权、relay、转换、流式交付与失败控制。
- [余额与额度](modules/quota/README.md)：独立来源、适配器、缓存、有界调度和前端查询语义。
- [调用观测](modules/observability/README.md)：日志归并、显式 HTTP 尝试、切换诊断及统计边界。
- [桌面与开发工程](modules/desktop/README.md)：Electron 进程、Dev/正式隔离、页面、构建、发布与更新。

## 知识覆盖

本版覆盖概览、核心对象、后端运行、资源管理、生成网关、额度查询、调用观测和桌面工程。构建与发布位于桌面模块，本地定时维护位于运行模块，调用排障位于观测模块；项目不是面向公网集群的部署系统，因此没有虚构集群、服务等级或已演练的灾备流程。

渠道和模型删除已包含只读影响预览与确认后的事务级联清理，具体保留项和激活项规则以[资源模块](modules/resources/README.md)为准。协议转换记录主链路和修改边界，不代替逐字段源码。

维护知识与代码的对应关系见 [wiki.config.json](wiki.config.json)，复核声明见 [reviews.json](reviews.json)。架构知识以本 Wiki 为唯一活跃入口，不再单独维护 `ARCHITECTURE.md`。`freshness` 只比较已映射来源、文档及配置的指纹，不能判断语义是否正确。当前没有为 Wiki 接入自动监听、定时同步或合并门禁；使用本库前应运行技能提供的 `freshness`，开发收尾核对实际 diff 后再保存相应模块的复核声明。命令格式：

```bash
node <wiki技能目录>/scripts/wiki.mjs freshness --root <项目根> --config docs/wiki/wiki.config.json
node <wiki技能目录>/scripts/wiki.mjs check --root <项目根> --wiki docs/wiki --config docs/wiki/wiki.config.json
```

## 依据与核验

入口核对 `README.md`、`CLAUDE.md`、`docs/product/PRODUCT.md`、`docs/product/DEVELOPMENT.md`，并以 `web/src/renderer/App.jsx`、`pageResources.js`、`components/Layout.jsx` 验证默认 `/channels` 和六页导航；以 `backend/gateway/gateway.go`、`backend/router/router.go`、`backend/api/local_access.go` 核对服务入口、路由和管理边界。架构正文统一维护于本 Wiki，不再使用独立 `ARCHITECTURE.md`。

本次为源码初始化与静态核验，没有扩大历史验证结论的适用范围。主 Agent 于 2026-10-28 执行并通过 `go test ./...`、`go vet ./...`、前端 245 项测试及 `pnpm --dir web run typecheck`；没有启动正式或 Dev 桌面应用，没有访问真实上游、读取业务凭据或验证安装包。

## 待确认项

无。GitHub 线上检查、真实上游能力和安装包状态均属于需要在相应环境验证的事项，不阻碍本 Wiki 描述当前源码。
