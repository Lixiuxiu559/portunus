# 分组服务端选模型与多成员创建验证

日期：2026-10-20。实现基线：`98f8d8a`；本报告随分组分页与多成员创建改动一同提交。

## 结果

分组选择器按渠道、名称向真实管理 API 查询，每页 20 条。创建分组支持跨页、跨渠道多选；故障转移可上移/下移；手动策略在创建事务内设置当前成员。添加成员复用分页查询并排除组内已有模型。成员显示直接使用分组响应中的 `model_name`、`channel_id`，不再依赖加载前 1000 条目录。

后端对省略 `active_model_id` 或传 0 的旧客户端保留原行为；非零值只允许 manual 且属于本次初始成员。无新增数据库列，成员元数据批量关联查询，分组列表固定两次查询。

## 验证环境

- 使用本机 Ego Lite 真实 Chromium 浏览器操作生产 renderer 构建。
- 使用真实 Gin 管理路由、GORM 和内存 SQLite。`fixture.go` 初始化两个合成渠道和 1102 个合成模型，监听 `127.0.0.1:13471`。
- 没有连接真实推理上游，不产生模型生成调用；没有读取、复制或修改正式和 Dev 数据。验证完成后已关闭浏览器任务空间并停止临时服务。
- 这是浏览器与真实后端管理流程验证，不等同于重新打包 Electron 后的安装验证。

## 自动检查

全部通过：

```sh
pnpm --dir web test                 # 94 项通过，含新增的 5 项选择/分页竞态测试
pnpm --dir web run build:renderer
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go test ./...
GOCACHE=/tmp/portunus-reasoning-cache GOTOOLCHAIN=local go vet ./...
git diff --check
```

新增后端回归覆盖：超过 1000 条目录外成员、跨渠道同名、创建/列表/详情/更新响应一致、旧请求兼容、非法 active、重复成员回滚、激活写入失败全回滚、无数据库迁移列、无逐成员查询。新增 active 字段测试在实现前已确认失败，实施后通过。

## 实际页面操作

| 场景 | 观察结果 |
| --- | --- |
| 已有分组引用第 1100 个模型 | 正确显示 catalog-1099、验证渠道 A 和当前标记 |
| 第一页选 catalog-0000，第二页选 catalog-0020，再搜索 zz-shared | 已选项保留，并能选出 ID 1101/1102 的跨渠道同名模型 |
| 故障转移上移渠道 B 同名模型 | 保存顺序为模型 ID 1、21、1102、1101；priority 为 0、1、2、3 |
| 第一次保存返回 503，再重试 | 弹窗及四个选择完整保留；重试成功且仅创建一个分组 |
| 手动多选渠道 A/B 同名模型，选择 B 为当前成员 | 一次创建后 active_item_id 对应渠道 B 的成员 |
| 已有分组添加成员，搜索已有 catalog-1099 | 提示本页模型均已在分组中；另搜索可添加 ID 1102 |
| catalog-109 查询故意延迟 1.8 秒，再搜 zz-shared | 旧结果晚到后页面仍只显示新的两个同名模型 |
| 模型查询一次返回 503 | 显示本地重试入口；重试后显示真实空结果 |
| 选择 A 后筛选渠道 B | 查询只显示 B，已选 A 保留；选择 B、移除 A 后当前成员回退到 B |
| 关闭后重载并重开 | 名称、搜索、筛选、选择恢复初始状态 |
| 轮询创建 | 正确保存 round_robin，active_item_id 为 0 |
| 深/浅色与 430px 窄窗 | 截图检查；窄窗弹窗宽 398px、scrollWidth/clientWidth 都为 398，无弹窗内横向溢出 |

网络记录确认没有 `page_size=1000`；首页只取 1 条用于读取总数，选择器请求 20 条并携带 name、channel_id 和 page。独立代码检查沿 correctness / standards / spec 三轴进行，未报告阻断问题。

## 证据与复现

- `assessment.json`：最终分组结果及分页请求自动断言。
- `groups.json`：真实管理 API 返回的最终分组与成员数据。
- `requests.json`：隔离服务收到的请求路径。
- `final-snapshot.txt`：最终页面可访问性快照。
- `manual-create.png`、`round-robin-light.png`、`narrow-dark.png`：实际浏览器截图。
- `fixture.go`：可复现隔离服务。先构建 renderer，在仓库根运行 `go run docs/research/2026-10-20-group-model-selection/fixture.go`，打开 `http://127.0.0.1:13471/#/groups`；仅创建内存数据。

现有非阻断项：构建仍提示大 chunk，Node 测试仍有 module type 提示。极窄屏下既有外层导航/页面宽度可超过视口，本轮确认分组弹窗本身不横向溢出。过期查询结果被忽略，但全局请求层仍可能对过期失败显示统一错误通知。
