import { useState } from 'react';
import { Link } from 'react-router-dom';
import { Button, Select, ListBox } from '@heroui/react';
import { findClientGuide } from './tools';
import { buildServiceExamples } from '../../utils/serviceAccess';
import { CodeSample, CopyValue, DocLink, GuideStep, Note, Parameters } from './shared';

const STRATEGY_LABELS = { manual: '手动选择', round_robin: '轮询', failover: '故障转移' };

/** 接入示例：模型分组 + API / 各客户端的手动配置指南。 */
export default function ExamplesPane({
  groups,
  selectedGroup,
  onSelectGroup,
  selected,
  selectedDescription,
  missingRequestedGroup,
  loading,
  tool,
  guide,
  preview,
  ready,
  apiKey,
  serverUrl,
  showKey,
  logsPath,
}) {
  const [stream, setStream] = useState(false);
  const [apiProtocol, setAPIProtocol] = useState('chat');

  const examples = buildServiceExamples({ serverUrl, apiKey, model: selectedGroup, stream });
  const examplePreview = buildServiceExamples({ serverUrl, apiKey: showKey ? apiKey : '', model: selectedGroup, stream });
  const protocolIndex = Math.max(0, examples.findIndex((example) => example.id === apiProtocol));
  const protocolExample = examples[protocolIndex];

  const current = findClientGuide(tool);
  const isClaude = tool === 'claude';
  const isCodex = tool === 'codex';
  const fileName = isClaude ? 'settings.json' : 'config.toml';
  const directory = isClaude ? '.claude' : '.codex';
  const parameterRows = isClaude ? [
    ['ANTHROPIC_BASE_URL', 'Portunus 网关地址，不包含 /v1；客户端会自动拼接请求路径。'],
    ['ANTHROPIC_AUTH_TOKEN', 'Portunus 令牌，用于访问本地网关。'],
    ['ANTHROPIC_MODEL', '默认使用的分组名称，不是上游渠道的模型 ID。'],
    ['ANTHROPIC_DEFAULT_*_MODEL', '将 Sonnet、Opus、Haiku 档位映射到分组；也可分别填写不同分组。'],
  ] : isCodex ? [
    ['model_provider', '选择下方定义的 portunus provider。'],
    ['model', 'Portunus 中的分组名称，由网关选择实际调用的上游模型。'],
    ['base_url', 'OpenAI 兼容地址，需要包含 /v1。'],
    ['wire_api', '使用 responses，与 Portunus 的 Responses 接口对接。'],
    ['experimental_bearer_token', '该 provider 使用的 Portunus 令牌，无需修改 auth.json。'],
  ] : current.parameters;

  return (
    <div className="service-examples">
      <div className="service-examples-head">
        <div className="service-group-field">
          <div className="service-group-row">
            <span className="service-group-label">模型分组</span>
            <Select
              placeholder={loading ? '加载中…' : groups.length ? '请选择分组' : '暂无分组'}
              selectedKey={selectedGroup || null}
              onSelectionChange={(key) => { if (key !== null) onSelectGroup(String(key)); }}
              isDisabled={loading || !groups.length}
              className="service-group-select min-w-0"
            >
              <Select.Trigger aria-label={selected ? `模型分组：${selected.name}` : '选择模型分组'} aria-labelledby={null} className="service-group-trigger w-full min-w-0 rounded-xl">
                <Select.Value className="min-w-0 flex-1 text-left">
                  {() => selected ? <span className="block min-w-0 truncate text-[13px] font-medium" title={selected.name}>{selected.name}</span> : <span className="text-[13px] text-muted">{loading ? '加载中…' : groups.length ? '请选择分组' : '暂无分组'}</span>}
                </Select.Value>
                <Select.Indicator className="size-4 shrink-0 text-muted" />
              </Select.Trigger>
              <Select.Popover className="service-group-popover">
                <ListBox aria-label="可接入的模型分组">
                  {groups.map((group) => <ListBox.Item key={group.name} id={group.name} textValue={group.name} className="min-w-0 py-2.5">
                    <div className="min-w-0 flex-1"><span className="block truncate text-sm font-medium" title={group.name}>{group.name}</span><span className="mt-0.5 block text-xs text-muted">{STRATEGY_LABELS[group.strategy] || group.strategy} · {group.items?.length || 0} 个成员</span></div>
                    <ListBox.ItemIndicator />
                  </ListBox.Item>)}
                </ListBox>
              </Select.Popover>
            </Select>
            {selected && <span className="service-group-meta" title={selectedDescription}>{selectedDescription}</span>}
          </div>
          {!loading && missingRequestedGroup && <p role="alert" className="mt-3 text-xs leading-5 text-warning">链接中的分组「{missingRequestedGroup}」不存在，请重新选择。未替换为其他分组。</p>}
          {!loading && !groups.length && <p className="mt-3 text-xs leading-5 text-muted">添加<Link to="/channels" className="text-accent underline">上游渠道</Link>后，创建<Link to="/groups" className="text-accent underline">模型分组</Link>再接入。</p>}
          {selected && !selected.items?.length && <p className="mt-3 text-xs leading-5 text-warning">分组尚无成员，请先添加上游模型。</p>}
        </div>
        <p className="service-examples-note">示例只生成可复制内容，不会写入客户端文件。</p>
      </div>

      {tool === 'api' ? <div className="space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="flex flex-wrap gap-1" role="group" aria-label="API 协议">
            {examples.map((example) => <Button key={example.id} size="sm" variant={example.id === apiProtocol ? 'secondary' : 'tertiary'} aria-pressed={example.id === apiProtocol} onPress={() => setAPIProtocol(example.id)}>{example.label}</Button>)}
          </div>
          <label className="flex cursor-pointer items-center gap-2 text-xs text-muted"><input className="service-stream-check" type="checkbox" checked={stream} onChange={(event) => setStream(event.target.checked)} />流式响应</label>
        </div>
        <CodeSample title={`POST ${protocolExample.path}`} code={protocolExample.code} preview={examplePreview[protocolIndex].code} disabled={!ready} />
        <p className="text-xs leading-6 text-muted">输出预算 128 token。复制不会调用上游；手动执行可能产生费用。预览默认隐藏令牌，复制包含真实令牌。</p>
        <details className="service-disclosure border-t border-separator pt-4 text-sm">
          <summary>验证接入与模型列表</summary>
          <div className="mt-4 space-y-3 text-xs leading-6 text-muted"><CopyValue label="模型列表 · GET，需相同 Bearer 令牌" value={`${guide.openAIBaseUrl}/models`} /><p>执行示例后，到<Link to={logsPath} className="text-accent hover:underline">调用记录</Link>查看分组、命中上游与结果。模型列表只验证基础连接，一次生成成功只证明当前请求路径。</p><p>思考模型的预算可能包含思考 token。思考强度和输出预算在调用方设置，无需在分组重复配置。</p></div>
        </details>
      </div> : <div className="space-y-4">
        <div className="flex flex-wrap items-start justify-between gap-3"><div><h3 className="text-base font-medium">{current.label}</h3><p className="mt-1 text-xs leading-6 text-muted">{current.description}</p></div>{current.docs && <span className="text-xs"><DocLink href={current.docs}>官方文档</DocLink></span>}</div>
        <CodeSample title={isClaude || isCodex ? `${fileName} · 合并示例` : current.sampleTitle} code={isClaude || isCodex ? guide[tool] : guide[current.sampleKey]} preview={isClaude || isCodex ? preview[tool] : preview[current.sampleKey]} disabled={!ready} />
        <p className="text-xs leading-6 text-muted">手动合并到现有配置，保留 MCP、权限及其他服务商。复制包含真实令牌，示例不会写入客户端文件；需要直接写入 Claude Code 配置时，使用左侧「客户端配置」。</p>
        <details key={tool} className="service-disclosure border-t border-separator pt-4 text-sm">
          <summary>详细配置步骤与常见问题</summary>
          <div className="service-manual mt-6 space-y-7">
            <GuideStep number={1} title="开始之前">
              <p>请先完成以下准备：</p>
              <ul className="list-disc space-y-1 pl-5 text-muted">
                <li>保持 Portunus 运行，在<Link to="/channels" className="text-accent hover:underline">上游渠道</Link>中添加上游服务，并确认模型已同步。</li>
                <li>在<Link to="/groups" className="text-accent hover:underline">模型分组</Link>中创建分组并添加模型，在上方连接配置中生成 Portunus 访问令牌。</li>
                {current.command ? <li>已安装 {current.label}。在终端执行 <code>{current.command} --version</code> 确认安装；尚未安装可参考 <DocLink href={current.docs}>{current.label} 官方安装说明</DocLink>。</li> : <li>{current.requirement}{current.docs && <> <DocLink href={current.docs}>{current.label} 官方配置文档</DocLink></>}</li>}
              </ul>
            </GuideStep>

            <GuideStep number={2} title="确认分组与连接参数">
              <p>当前分组为 <code>{selectedGroup || '尚未选择'}</code>。需要更换时，在上方「模型分组」中操作，以下配置随选择更新。</p>
            </GuideStep>

            <GuideStep number={3} title="手动配置客户端">
              {isClaude || isCodex ? <>
                <p>用文本编辑器打开下面的配置文件。目录或文件不存在时，请手动创建。</p>
                <div className="space-y-1 rounded-lg bg-surface-secondary/60 px-4 py-3 text-xs">
                  <p><span className="mr-3 text-muted">macOS / Linux</span><code>~/{directory}/{fileName}</code></p>
                  <p><span className="mr-3 text-muted">Windows</span><code>%USERPROFILE%\{directory}\{fileName}</code></p>
                </div>
                <p>{isClaude ? <>将示例中的字段合并到文件的 <code>env</code> 对象。文件为空时，可以直接粘贴完整示例。</> : <>将下面两个顶层字段放在所有 <code>[表头]</code> 之前，再添加 <code>[model_providers.portunus]</code> 表。已有同名字段或表时，请修改原项。</>}</p>
                <Note>已有配置时只合并相关字段，保留 MCP、权限和其他设置。{isClaude ? 'JSON 不支持注释或末尾多余的逗号；已有 env 对象时，请把字段合并进去，避免出现两个 env。' : 'TOML 中同名字段和同名表不能重复；本示例在 provider 内配置令牌，不需要修改 auth.json。'}</Note>
              </> : <>
                <ol className="list-decimal space-y-2 pl-5">{current.setup.map((step) => <li key={step}>{step}</li>)}</ol>
                {tool === 'opencode' && <details className="rounded-lg border border-separator">
                  <summary className="cursor-pointer px-4 py-2 font-medium">使用 OpenCode 2.x？</summary>
                  <div className="space-y-3 p-3"><p>在项目的 opencode.jsonc 中合并以下结构。2.x 使用 providers / package / settings；不要和上面的 1.x 配置混用。</p>
                    <CodeSample title="opencode.jsonc · OpenCode 2.x 合并示例" code={guide.opencodeV2} preview={preview.opencodeV2} disabled={!ready} />
                    <DocLink href="https://opencode.ai/v2/docs/providers">OpenCode 2.x 官方配置说明</DocLink>
                  </div>
                </details>}
                <Note>{current.note}</Note>
              </>}
              <details className="rounded-lg border border-separator">
                <summary className="cursor-pointer px-4 py-2 text-sm font-medium">这些字段分别代表什么？</summary>
                <div className="px-3 pb-3"><Parameters rows={parameterRows} /></div>
              </details>
            </GuideStep>

            <GuideStep number={4} title="启动客户端并验证接入">
              {current.command ? <>
                <p>保存配置，关闭已有的 {current.label} 会话，在项目目录重新打开终端并运行：</p>
                <CodeSample title="终端" code={current.command} />
              </> : <p>{current.launch}</p>}
              <p>发送一条简单消息，例如「你好，请简短介绍自己」，然后打开 Portunus 的<Link to={logsPath} className="text-accent hover:underline">调用记录</Link>页面。</p>
              <ul className="list-disc space-y-1 pl-5 text-muted">
                <li>出现新的请求记录，且分组为 <code>{selectedGroup || '所选分组'}</code>：请求已到达 Portunus。</li>
                <li>请求成功并收到客户端回复：说明本次网关、路由和上游调用已连通。</li>
                <li>没有新记录：优先检查客户端使用的地址、令牌，以及配置是否已生效。</li>
              </ul>
            </GuideStep>

            <section id="guide-faq" className="scroll-mt-5 border-t border-separator pt-6">
              <h2 className="mb-4 text-base font-semibold">常见问题</h2>
              <div className="divide-y divide-separator text-sm leading-7">
                <details className="py-3"><summary className="cursor-pointer font-medium">连接被拒绝，或提示无法连接到服务？</summary><p className="pt-2 text-muted">确认 Portunus 正在运行，且客户端和网关在同一台电脑上。当前网关地址为 <code>{guide.baseUrl}</code>。远程容器、SSH 会话或其他设备中的 127.0.0.1 指向它们自身，需要使用可访问的网关地址；局域网访问可在本页服务访问范围中开启。</p></details>
                <details className="py-3"><summary className="cursor-pointer font-medium">返回 401，或提示令牌无效？</summary><p className="pt-2 text-muted">客户端应填写 Portunus 令牌。若在服务接入中重新生成过令牌，请重新复制并手动更新客户端配置；渠道中填写的上游 API Key 不能代替网关令牌。</p></details>
                <details className="py-3"><summary className="cursor-pointer font-medium">提示模型不存在，或分组没有可用模型？</summary><p className="pt-2 text-muted">确认模型名与分组名称完全一致，分组中已添加模型，所选路由策略配置完整，并检查上游渠道是否可用。可在调用记录中查看具体错误。</p></details>
                <details className="py-3"><summary className="cursor-pointer font-medium">已经保存配置，为什么仍连接原来的服务商？</summary><p className="pt-2 text-muted">关闭旧会话后重新启动客户端。检查终端环境变量、项目级配置或启动参数是否覆盖了刚修改的设置；Codex 还需确认顶层 model_provider 已选为 portunus。</p></details>
              </div>
            </section>
          </div>
        </details>
      </div>}
    </div>
  );
}
