# max_tokens 与 max_completion_tokens 单变量真实对照

日期：2026-10-20。用户明确授权进行小规模真实调用，未授权修改业务代码。本轮共 4 次生成，不重试，不额外补样。

## 控制条件

- 两条现有路由：乐聚-newapi / deepseek/deepseek-v4.1-flash；DeepSeek 官方 / deepseek-flash。
- 同一条资源排程提示，复用前一轮案例；无工具、无历史；reasoning_effort=high、stream=true、stream_options.include_usage=true。
- 每次只提供一个预算字段，数值固定 256；不同时提供两个字段。
- 同一渠道内请求体除了预算字段名称外完全一致；跨渠道只另有实际模型名不同。
- 顺序交叉：第一对乐聚 max_tokens / 官方 max_completion_tokens；第二对乐聚 max_completion_tokens / 官方 max_tokens。每对两路并发，同一渠道串行。
- 请求由当前源码 ComposeUpstreamRequest 的 Chat 同协议路径处理，仅替换上游模型名，再通过 NewUpstream/NewHTTPClient 发送；避免重新走 Responses 转换导致实验变量被改变。
- 凭据只读本机桌面应用 SQLite 后在内存使用；不更改服务、模型配置、持久调用日志。
- 每次 180 秒总超时；预算字段若被忽略，允许该次完成并记录实际用量，不自动取消后重发。

判定：不能只看 200。使用上游报告的 completion_tokens（包含 reasoning_tokens）和 finish_reason 检查预算是否执行。预期受限请求在思考阶段达到 256 即 length；若大量超过 256 后自然 stop，则该字段没有形成期望的总生成上限。

## 结果

全部 4 次 HTTP 200、收到 [DONE]；相同提示哈希、high 档位、输入各 360 token，其中缓存命中各 128 token。每个渠道两次只切换预算字段名称。

| 渠道 | 字段（均为256） | 实际生成token | 其中思考token | 结束原因 | 耗时 |
|---|---|---:|---:|---|---:|
| 个人-deepseek | max_completion_tokens | 6,404 | 6,362 | stop | 32.195s |
| 个人-deepseek | max_tokens | 256 | 256 | length | 2.408s |
| 乐聚-newapi | max_completion_tokens | 5,089 | 5,047 | stop | 25.289s |
| 乐聚-newapi | max_tokens | 256 | 256 | length | 1.467s |

## 结论

**已验证，置信度高：当前两条配置路由中，max_tokens 能限制包含思考在内的总生成量；max_completion_tokens 没有形成同等上限。** 两条受限请求均恰好 256 token、全部用于思考、正文为空且 finish_reason=length；这是预算耗尽，不是请求失败。未受限的对应请求生成 5,089/6,404 token 后自然 stop。

这把上轮“有证据支持”的字段兼容解释推进为单变量实验验证：当前 Portunus Responses→Chat 固定将 max_output_tokens 放到 max_completion_tokens，对标准 OpenAI 有协议依据，但对本次乐聚/DeepSeek 目标不能保住输出上限。直接官方也复现，不能只归因于乐聚。

仍不声称已知上游内部实现：可能忽略该字段或以其他方式处理；外部实验已经足够确认它在这两条路由未按总生成预算执行。不能推广为所有 Chat 兼容上游都不支持 max_completion_tokens，也不能据此全局改用 max_tokens。

业务代码、运行服务和现有配置均未修改；只新增本次验证资料，临时运行器已删除。未发起额外对照、重试或后续收费调用。

## 可复核文件

case.json 保存逐字提示；results.jsonl 保存每次实际字段、提示哈希、用量、结束原因、耗时、请求 ID 和最终答案，不保存密钥或思考原文。benchmark.go.txt 为无凭据运行器，复制成临时 .go 文件后从仓库根运行并传本目录路径。已有结果文件时拒绝覆盖，避免误重复计费。
