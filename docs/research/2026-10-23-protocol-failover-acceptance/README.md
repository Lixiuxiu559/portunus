# 协议转换与故障转移实际验收（2026-10-23）

本轮停止新增功能，按普通对话、流式输出、函数工具闭环、主失败切备用四项验收。常用路径可以使用；发现并修复一处指定函数 `tool_choice` 格式转换缺陷。上游模型的思考模式与强制工具选择限制仍需调用方遵守，不承诺所有供应商能力跨协议等价。

## 环境与证据口径

使用当前工作区构建的实际 main 二进制，运行于独立 Dev 数据和回环端口13061；前端使用当前 Vite 页面，真实浏览器检查配置表单和调用详情。正式版13060端口的进程未停止、替换或重启。本轮不是打包 Electron、真实 SDK/CLI、LAN、多进程或压力验收。

Dev 已配置两种真实上游：Chat 渠道的 `deepseek-flash`、Responses 渠道的 `deepseek-v4-flash`。分别通过临时手动分组调用 Chat Completions、Responses、Messages 三个入口，不修改原有分组的主备顺序。密钥仅在验收进程内从 Dev 读取，报告不保存密钥、上游原始推理或完整响应。

[脱敏逐项证据](evidence.json)保存54个唯一用例文件的摘要、请求ID、状态、终态/工具校验和路由事件；包含修复前失败与已知限制，不能把总数当成全部通过数，也不是全部HTTP调用次数（含复验、覆盖与临时故障尝试）。[构建指纹](builds.json)区分修复前后可执行文件及本轮修复diff；构建基于 HEAD `5823690b71d4ce53aaaf814c6e086144ff409ff7` 加工作区已有阶段改动及本轮修复，不等于干净HEAD。

## 验收结果

| 场景 | 实际检查与结果 |
| --- | --- |
| 普通对话 | 两种上游 × 三个入口，非流式均200且正文为OK |
| 流式输出 | 同一六路径均有正文OK及协议对应终态：Chat `[DONE]`、Responses `response.completed`、Messages `message_stop`，不是只检查200 |
| 函数工具 | 校验 `sum_numbers`、有效call ID、流式参数拼合为a=2/b=3；回传本地加法结果后得到5。两种上游、三个入口均完成闭环；Chat上游Responses/Messages入口在auto工具选择下闭环通过，强制工具选择另按下述约束复验 |
| 503主失败 | 本机临时故障主渠道返回503，备用调用真实Responses上游：非流式主3次+备用1次，流式后续请求主1次触发开路+备用1次，均取得OK和正确终态；统一4次预算未超出 |
| 429主限流 | 主429后同请求直接调用真实备用；后续流式请求跳过冷却主渠道，只调用备用1次，未为跳过制造HTTP attempt |
| 手动模式 | 相同故障/冷却条件返回503/429，不自动调用备用；全跳过时没有HTTP attempt |
| 浏览器诊断 | 日志详情实际展示冷却跳过候选1、选择备用候选2、唯一Do、容量取得与释放；见[页面文本证据](browser-routing.txt) |

故障演练的主渠道是本机可控503/429服务，备用是真实供应商；这不能证明真实供应商停机行为或所有异常响应均兼容。临时故障渠道删除后，历史详情显示“渠道已删除或未加载”是清理后的正常表现。

一个验收脚本最初假设Chat工具index必须从0开始；Responses上游先输出文本后工具的实际index为1。改为按实际index聚合后，工具名称、参数及ID正确，回传结果得到5（请求 `970372dada653ef4`）。这项修正仅属于验收脚本，不作为产品修复。

## 实际问题与修复

Responses指定函数格式为 `{"type":"function","name":"sum_numbers"}`；Chat格式为 `{"type":"function","function":{"name":"sum_numbers"}}`。原实现直接复制tool_choice，Responses入口转Chat上游时真实返回422。

本轮只在跨协议请求边界补双向标准函数形状转换，保持字符串选择、托管工具、无法确定的结构及同协议原生透传行为；保留非冲突顶层扩展，不修改原对象。代码位于 `backend/protocol/responses.go`，新增 `responses_tool_choice_test.go` 覆盖双向、流式标志、同协议、未知形态及扩展保留。没有修改思考模式、重试规则、预算或重放边界。

复验：Responses入口转Chat上游，显式 `reasoning.effort=none` 时流式与非流式强制函数均200且工具参数正确；Messages入口转Chat上游，显式 `thinking.type=disabled` 时同样通过。Chat/Messages入口转Responses上游及Responses原生路径的强制工具流式/非流式也通过。

**保留的上游约束：** 原始Responses请求不关闭DeepSeek默认思考模式时，修复后格式422已消失，但上游仍返回400 `Thinking mode does not support this tool_choice`；Messages强制工具请求也有相同限制。使用auto工具选择已完成闭环；需要强制工具时应显式使用该上游支持的非思考设置。网关没有静默关闭思考或将强制工具降为auto。此项记为兼容限制，不能宣称原始请求已通过。

## 验证与复现

本轮修复先以定向测试复现失败，再验证通过；最终 `go test ./...`、`go vet ./...`、`go test -race ./backend/protocol ./backend/gateway` 和实际二进制构建均通过。前端本轮没有业务代码修改，完成真实浏览器检查；不把前阶段前端测试数字算作本轮重新执行。

复现时使用独立Dev配置，分别给Chat/Responses真实模型建立临时manual分组。以Dev API令牌调用 `/v1/chat/completions`、`/v1/responses`、`/v1/messages`，提示只回复OK，分别设置stream false/true；检查正文和上述协议终态。工具声明sum_numbers(a:integer,b:integer)，提示计算2加3，按实际工具索引拼合参数，回传相同call ID和结果5；不要调用外部有副作用工具。验证格式修复时，Responses强制工具使用上述flat形状，并以 `reasoning:{"effort":"none"}` 检查Chat目标。

故障复现使用临时本机HTTP服务器固定返回503或429，临时failover分组按“故障主、真实备用”固定排序；通过 `/api/logs/attempts?request_id=...` 和 `/api/logs/routing?request_id=...` 对照Do数、跳过和最终候选。429后立即重复流式请求应仅有备用Do；manual应不切备用。管理接口仅在回环侧使用。请求ID和精简事件见证据JSON。

## 收口与未覆盖范围

仅清理本轮创建的临时分组、模型和故障渠道；保留Dev验收日志。Dev正常执行了新字段迁移，模型同步可能更新Dev模型目录，不回滚或删除发现结果。原有渠道连接配置、分组成员及优先级保留。自建浏览器任务、Dev服务和Vite已结束，正式版进程仍在原端口运行。

本轮没有实际覆盖多模态/托管工具、多候选等价映射、长时高并发压力、所有上游错误体、半开大量并发及可选并发上限的真实供应商负载；这些路径已有的自动化回归与本次实测范围应区分。已交付后重放、原生操作重放、资源及deadline等安全边界继续强制执行，本轮未放宽；不能仅凭本轮正常调用断言所有安全边界都已实测。

待完善内容集中在[待办记录](../2026-10-23-protocol-failover-backlog.md)，按实际需求或问题触发，不自动进入下一轮功能扩展。

## 后续本地正式版更新（同日，另获用户授权）

验收结束后，用户明确要求将修复更新到本地正式版。已重新构建正式身份的macOS arm64目录应用，本地签名校验后替换 `/Applications/Portunus.app` 并重启；未发布远端、未新增业务功能。181项前端测试通过。实际安装包与构建产物的后端及app.asar指纹一致。

退出旧版后先备份旧应用和完整用户数据，再沿用正式数据目录 `~/Library/Application Support/@portunus/web`。按旧列逐表核对，渠道、模型、分组、成员、API Key、设置和历史日志原有行全部保留，config.json一致。重启后13060健康接口、渠道/分组及routing接口200，原生桌面渠道与分组页面正常加载。备份及脱敏校验结果保存在本机 `~/Library/Application Support/Portunus Local Backups/20261023-212151`，含敏感配置的数据备份不纳入仓库。此节是后续安装记录，不改变前文隔离验收当时未替换正式版的事实。

## 更新后正式版最小复验

随后按用户要求，在13060正式版使用已有deepseek-v4-flash分组执行5次真实请求：Chat普通/流式正文OK及正常终态，Responses指定sum_numbers函数的非流式/流式参数a=2、b=3，回传工具结果后回答5。工具请求显式reasoning.effort=none；已采集尝试确认走Responses→Chat，而非同协议透传。5次均200且每次仅1次显式Do，证明格式修复已在安装后的正式版生效。渠道、分组、成员、API Key、设置及config.json前后指纹一致；只新增正常调用日志，不改配置、不加功能。见[脱敏复验证据](production-smoke.json)。
