# 自定义余额查询界面验收

2026-10-21，使用 ego-browser，独立静态构建与内存 mock `127.0.0.1:18749`。未访问 Portunus Dev 13061 或正式版，未使用真实账号或凭据。mock 进程及专用浏览器空间 28 已关闭。

## 实际检查结果

- 无效请求头 JSON `{`：界面就地报错；mock 收到 preview 请求数为 0。
- 切换“无鉴权”：Token 占位请求头被去掉，preview 发送 `headers={}`、无 credentials；测试草稿没有来源保存请求。
- 普通保存：mock 收到 enabled=false、auto_refresh_minutes=0，没有立即刷新请求。POST 请求体 `{"account":9007199254740993,"fraction":0.123456789012345678}` 原文保留；请求记录见 mock-receipts.json。其中 `fixture-only-token` 为专用测试假凭据。
- 编辑已保存来源：原地址显示留空保留 Token；改为另一测试域名后显示需重新输入，密码框为空，不再承诺保留旧凭据。此次地址修改未保存。
- 可添加第二额度项、再删除；勾选“总额度减已用”后剩余额度路径清空且禁止填写。未保存这些草稿修改。
- 1024×576 明暗模式没有横向溢出。模态框内容滚动，关闭按钮始终可达，截图已实际查看：dark-1024-form.png、light-1024-form.png、light-1024-request.png。

## 自动验证

- `cd web && pnpm test`：125 项通过。
- `cd web && pnpm build:renderer`：通过；现有打包体积提示仍存在。
- `git diff --check`：通过。
- 新增业务用例覆盖结构化配置深拷贝、JSON 语法和标量检查、单位/除数、改地址后不保留 Token、null 与高精度条件 roundtrip、POST body 精度、无鉴权去占位头。

## 验证边界

mock 只验证界面、请求形状与编辑行为，不执行自定义适配器、不连接上游、不验证真实账户余额或鉴权。后端适配器、URL/凭据约束、Decimal 解析与查询生命周期由后端测试单独验证。第一轮草稿预览与 JSON 拦截验证后 mock 进程退出并重启，因此最终 mock-receipts.json 仅含第二轮普通保存与编辑观察记录，不能用该文件反推第一轮请求历史。
