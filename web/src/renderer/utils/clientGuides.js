// 接入步骤来自各客户端官方文档。只描述手动操作，不访问客户端文件。
export const additionalClientGuides = [
  {
    parameters: [['model', '默认模型引用使用 portunus/分组名称。'], ['provider.portunus', '1.x 的自定义服务商，options 中配置 baseURL 与 apiKey。'], ['models', '以分组名称为键注册可选择的模型。'], ['providers.portunus', '2.x 使用此结构，并通过 package 与 settings 选择接口、地址和令牌。']],
    id: 'opencode', label: 'OpenCode', sampleKey: 'opencode', sampleTitle: 'opencode.json · OpenCode 1.x 合并示例',
    description: '为 OpenCode 添加自定义 Portunus provider，使用分组作为模型。',
    docs: 'https://opencode.ai/docs/providers/#custom-provider',
    requirement: '已安装 OpenCode，可通过 opencode --version 确认版本。',
    setup: ['OpenCode 1.x：在项目目录打开或创建 opencode.json，将下方 provider 与 model 字段合并进去。', '模型列表中的键是 Portunus 分组名称；默认模型引用为 portunus/分组名称。'],
    note: '不要覆盖已有 provider。示例在 options.apiKey 内提供令牌，无需再次执行 /connect；若使用凭据管理，provider ID 也需为 portunus。OpenCode 2.x 的配置结构不同，请使用下方单独的示例。',
    launch: '运行 opencode，执行 /models，选择 Portunus 下的分组后新建会话。',
  },
  {
    parameters: [['agents.defaults.model.primary', '默认模型使用 portunus/分组名称。'], ['models.providers.portunus', '自定义服务商，包含 baseUrl、apiKey、api 和 models。'], ['api', 'openai-completions 对应 Chat Completions 接口。'], ['models[].id', '填写 Portunus 的分组名称。']],
    id: 'openclaw', label: 'OpenClaw', sampleKey: 'openclaw', sampleTitle: 'openclaw.json · 合并示例',
    description: '为本机 OpenClaw 添加自定义模型服务，交由 Portunus 聚合和路由。',
    docs: 'https://docs.openclaw.ai/concepts/model-providers/custom-providers',
    requirement: '已安装并完成 OpenClaw 的初始设置；网关与 OpenClaw 服务应能互相访问。',
    setup: ['打开 ~/.openclaw/openclaw.json（Windows 为 %USERPROFILE%\\.openclaw\\openclaw.json）。', '将 models.providers.portunus 合并进现有配置，并将 agents.defaults.model.primary 设为示例中的 provider/分组引用。'],
    note: '保留原有频道、工具与其他模型配置。若配置了模型允许列表，也需允许所选分组；上下文和工具能力应按实际上游设置。',
    launch: '保存并按 OpenClaw 提示应用配置；打开 openclaw dashboard 或已有聊天入口，新建会话并选用 Portunus 模型。',
  },
];
