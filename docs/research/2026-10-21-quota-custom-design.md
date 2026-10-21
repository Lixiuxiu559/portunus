# D 调整：自定义余额查询

2026-10-21。用户取消 Sub2API Key 限额及套餐额度，随后明确参考 CC Switch 的自定义能力。本轮将 D 调整为结构化自定义查询，不扩展具体厂商或 OAuth，不执行 JavaScript。

## 依据和实现选择

本地 CC Switch `src/components/UsageScriptModal.tsx` 的 custom 模板为 request + JavaScript extractor；`src-tauri/src/usage_script.rs` 分阶段提取请求和解析结果。事实与版本依据复用[调查记录](./2026-10-21-cc-switch-balance-query.md)。其自定义模式可适配私有站点，不要求先开发厂商适配器。

Portunus 采用可配置请求和结果映射覆盖常见用途：GET/POST、站点根地址及相对路径、请求头、JSON 请求体、JSON Pointer 字段提取、成功条件、多项额度、显式除数、总量减已用、无限条件。保留原生适配器以提供经过核对的标准配置。不兼容直接粘贴 CC Switch 的 JavaScript；需要映射为结构化字段。

## 边界与结构

- 新增 `custom.balance` 适配器；`QueryOptions.Custom` 类型化持久化，不增加表。核心配置比较使用值语义，避免指针导致无变更保存误失效。
- 请求和解析留在适配器；执行器只按目录声明开放 POST 读取，单次请求、15秒超时、1MiB响应、禁止重定向、无通用自动重试维持不变。
- 新增无鉴权模式，仅 custom 支持；继承渠道 Key 与独立 Token 保留。`{{token}}` 仅能在请求头值中结构化替换，URL/Body 不插值。敏感请求头必须使用占位符，不在配置中保存明文凭据。
- 修改 custom 查询目标根地址时不复用旧独立 Token；同目标修改提取规则可保留 Token，但增加配置版本并使旧结果失效。预览同样遵守此规则，不能借已保存秘密向新站点发送。
- POST 是用户声明的查询接口，仅支持 JSON body，不能自动识别任意接口是否有副作用；界面明确仅配置只读接口。请求体上限16KiB。禁止传输控制头和 URL 凭据、重定向及跨站路径。
- 成功/无限条件使用 `equals_json` 字符串保存 JSON 标量原文，避免前端 JSON Number 往返舍入及 null 默认值改变。
- JSON Pointer 采用 `/data/balance`、`/wallets/0/value`、`~0`/`~1` 转义；不支持表达式、通配、递归或 eval。最多20项结果、有限路径长度/层级、有限请求头。
- 未返回/null 为未知，非法字段类型不能伪装成功。配置字段至少有一个有效数值或明确无限条件。金额使用已有十进制算法；显式换算要求给出原始单位并保留原始值，0、负数、未知与无限不混淆。
- UI 使用独立 CustomQuotaEditor 和纯草稿转换函数，复用既有预览、保存、刷新、缓存和调度。界面无数值解析运算、无厂商字段分支。

## 测试设计

复用 Go testing、注入 HTTPDoer、临时 SQLite、前端 node:test 与独立浏览器 mock，不搭建新框架。

| 规则 | 代表输入/操作 | 预期与入口 |
|---|---|---|
| 请求准确 | GET含静态query；POST JSON；根地址含部署前缀 | fake Doer校验完整URL、方法、头、body，每次一次；读取缓存不触网 |
| 地址与秘密 | path为绝对URL、//、点段、fragment、含token；CRLF与控制头；敏感头静态secret | 保存和预览触网前拒绝，错误不回显秘密；独立/继承/无鉴权分别验证 |
| 配置身份 | 目标A→B且不提供新Token；相同目标尾斜杠；仅修改映射；no-op保存 | 新目标旧Token不可用；同目标可保留；有效修改revision增加，no-op保持版本 |
| 精度与解析 | `9007199254740993.123456789`、0、负值、null、bool、无效数字 | 十进制精确保留，未知不当0，非法类型失败；adapter fixtures |
| 提取 | `/data/a~1b/~0x`、数组索引、缺失路径、无效转义、路径过长 | 标准指针正确，受限路径拒绝，不执行脚本或递归查询 |
| 多项和计算 | CNY/USD两个item；显式除数；total-used；无限匹配 | 不合计钱包，不猜单位，保留Original，稳定唯一ID |
| 业务条件 | success=true/false、code数值/字符串，condition缺失 | 精确类型比较；不满足是business failure，不将任意HTTP200当成功 |
| 生命周期 | 草稿改动/关闭、刷新在途修改映射、失败后读取缓存 | 复用版本/CAS/取消语义，迟到结果不覆盖新草稿，无第二套调度 |
| UI | headers/body/condition非法JSON；多项编辑；adapter切换 | 本地错误阻止发送；typed payload roundtrip，草稿不修改来源对象 |

通用429/鉴权失效/陈旧TTL/取消和全局并发沿用A/B测试；新增custom通过真实Service+fake HTTPDoer与管理API闭环验证，不仅测试私有解析函数。

## 验收记录

已实施。验证见[实施记录](./2026-10-21-quota-custom-implementation.md)和[界面验收](./2026-10-21-quota-custom-ui/README.md)。模拟验证不代表真实站点已通过；当前 Dev 13061 和正式数据未参与测试。
