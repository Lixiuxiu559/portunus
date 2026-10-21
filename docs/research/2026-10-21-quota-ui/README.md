# 余额 / 额度查询前端模拟验收

日期：2026-10-21。使用 ego-browser 技能，独立临时 Node mock 仅监听 `127.0.0.1:18741`，静态资源来自 renderer 构建。未访问 Dev 13061、正式数据或真实上游，未验证真实厂商账号。

## 视口与截图

ego 宿主将原生浏览器窗口固定为 1686×863，CDP 调整尺寸未生效。因此使用同源隔离 iframe 承载真实应用，读取其 `innerWidth=1024`、`innerHeight=576`，在该区域截图。应用媒体查询、滚动容器与弹窗布局按 iframe 的真实视口生效；并非缩放大截图。

- [浅色渠道页](channels-light-1024.png)
- [深色渠道页](channels-dark-1024.png)
- [来源关闭态与操作](source-disabled-light-1024.png)
- [草稿查询与原始额度详情](source-original-dark-1024.png)

已通过截图目检；两种主题均无横向溢出。长渠道名截断保留 title；长表单在弹窗正文内部滚动，底部关闭动作可见。余额 0 CNY、负余额 -0.125 USD 分别展示。

## 实测证据

| 操作 | mock 请求 / DOM 结果 |
|---|---|
| 打开额度入口 | 仅打开独立额度 Modal，未触发渠道 ExpandEditor；DOM 只有一个 dialog |
| 目录驱动 NewAPI 表单 | 自动选择与渠道 provider_id 匹配的目录项；仅专用凭据；user_id 必填，换算与展示币种均为空 |
| 草稿查询原始单位 | 主值完整显示 `999999.123456789 quota`，未默认 USD，未在前端转浮点 |
| 草稿在途修改名称 | 修改后取消请求；等待原 mock 延迟 2.3 秒后 `.quota-preview` 仍为空，新草稿名称保留 |
| 普通新建保存 | POST `enabled:false`、`auto_refresh_minutes:0`、`options:{user_id:"123"}`；未增加 refresh POST；显示“查询已关闭” |
| 专用秘密读取 / 清除 | 编辑密码框值为空；清除后 PUT 含 `credentials:{clear:true}`，来源 `has_token:false`；未额外刷新 |
| 切换适配器 | 原来源 `has_token:true`，切换后专用输入提示“填写专用 Token”，不再显示保留或清除旧 Token 提示 |
| 重复来源 | 相同 adapter 与根地址出现非阻塞提示，保存按钮仍可操作 |
| 保存并查询 | PUT 包含 `enabled:true`、`auto_refresh_minutes:0`、`expected_revision:1`；Token 留空时省略 credentials，随后仅一个 refresh POST |
| 202 在途 | 显示“查询中…”，后续只 GET 轮询，完成后收敛到最新状态，没有重复 refresh POST |
| 隐藏 / 可见切换 | 在 iframe document.hidden 上模拟 visibilitychange；在途时隐藏 2.2 秒，mock API 请求计数增量为 0；恢复可见立即 GET 并读到完成状态 |
| 配置失效 | 普通保存后主摘要立刻显示“配置已变更，需重新查询”，不保留旧金额作为当前结果 |
| 转换值的原始详情 | 使用固定归一化 fixture，显示 `1.999998246913578 credits`，详情保留 `999999.123456789 quota`；DOM 只有一个 item，不当成第二个账户 |
| 删除确认 | 独立“删除查询来源”确认，确认后仅一次 DELETE，来源数 2→1，渠道数仍为 2 |

隐藏测试覆盖事件处理和停止定时逻辑，未以操作系统切换窗口验证浏览器原生 visibilitychange。转换数值由 mock 提供，只验前端展示，不作为后端换算正确性的证据。

## 自动验证

- `pnpm --dir web test`：119 项通过，其中 quota 纯函数用例 9 项。
- `pnpm --dir web build:renderer`：通过；现有 bundle 大小提示仍存在。
- `git diff --check`：通过。
- 轮询纯函数测试覆盖读取失败后继续调度、隐藏停止、恢复可见读取、卸载后迟到响应不能重新启动定时器。

浏览器任务已结束，独立临时 mock 服务已关闭。
