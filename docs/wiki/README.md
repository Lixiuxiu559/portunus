# Portunus 项目知识库

## 文档信息

| 字段 | 内容 |
|---|---|
| 适用范围 | 当前仓库的产品边界、核心对象、后端模块、桌面运行与开发约定 |
| 事实状态 | 部分核验 |
| 核验基线 | `9bf6dbb506d2448d1564ebc2ffc7f5a3940cd372`；2026-10-27 工作区源码与文档 |
| 核验时间 | 2026-10-27 |

## 项目概览

Portunus 是本地运行的多上游 API 聚合网关：保存上游渠道及其模型，把跨渠道模型组成分组，再以分组名向客户端提供统一 `model`。主要交付形态为 Electron 桌面应用，Go 后端提供管理面 `/api` 和服务面 `/v1`。

- 当前代码支持 `manual`、`failover` 两种路由策略；旧轮询策略不属于当前接口能力。
- 服务入口为 Chat Completions、Responses、Messages 与模型列表；Gemini 当前是上游协议，不是独立客户端生成入口。
- 本地保存配置、令牌和调用观测；没有多用户、团队权限、按调用方配额或公网托管承诺。
- 不提供价格配置、调用费用计算或费用统计。余额查询只用于观测，不参与路由。
- 本 Wiki 描述源码基线，不等同于已发布安装包、生产部署或所有上游均已实测。

## 阅读导航

- [核心概念与跨模块规则](concepts.md)：先区分渠道模型、分组名、两类凭据及不同观测计数。
- [运行入口与公共基础设施](modules/runtime/README.md)：启动装配、数据库、管理/服务边界和定时任务。
- [渠道、模型与分组](modules/resources/README.md)：连接解析、选择性导入、同步与路由候选。
- [网关与协议转换](modules/gateway/README.md)：请求链路、故障处置、显式 HTTP 尝试及转换边界。
- [余额与额度](modules/quota/README.md)：独立查询来源、缓存、调度与 NewAPI 单位识别。
- [调用观测](modules/observability/README.md)：日志、关联归并、尝试链和切换诊断。
- [桌面与开发环境](modules/desktop/README.md)：Dev/正式隔离、sidecar、页面和构建命令。

## 知识覆盖

本版覆盖以上七个知识页面的入口和跨模块约束，不逐行复述实现。协议转换只记录主链路与已核对的边界，完整字段映射以源码和专项研究为准。历史验证报告仅在其原基线、环境与测试范围内有效。

产品定义见[产品文档](../product/PRODUCT.md)，实现总览见[架构说明](../../../ARCHITECTURE.md)，开发与发布边界见[开发说明](../product/DEVELOPMENT.md)。`docs/research/` 保存调查与验证证据，不是当前能力清单的替代品。

现行文档已按代码统一界面口径：默认进入 `/channels`，导航顺序为渠道、模型、分组、日志、接入、设置；路由策略只保留手动和故障转移。产品文档、README 与宣传底稿已同步，历史研究报告仍保留其原基线。

## 依据与核验

2026-10-27 通过 CodeGraph 读取 `main.go`、`backend/api`、`backend/gateway`、`backend/group`、`backend/router`、`backend/channel`、`backend/upstream`、`backend/model`、`backend/quota`、`backend/shared`、`backend/cron` 的相关入口，以及 Electron 的运行身份、sidecar、页面路由和构建脚本；另对照上述产品、架构及专题文档。各页面列出实际使用的来源。

本次仅进行知识初始化与静态来源核对，没有执行 Go/前端测试、真实上游调用、桌面启动或发布包验证。结构检查配置见 `wiki.config.json`，复核记录只代表声明时的文件指纹和已说明的核对范围。

## 待确认项

1. 本次未核实已发布 Release 的实际版本内容、线上 CI 状态、生产部署版本或全量真实上游兼容性。
