import { useId } from 'react';
import { Button, FieldError, Input, Label, TextField } from '@heroui/react';
import { ChevronDown } from 'lucide-react';
import ChannelIconPicker from '../../components/ChannelIconPicker';
import FieldHelp from './FieldHelp';
import { validateChannelCapacity } from './channelDraft';

function Choice({ label, value, options, onChange, disabled = false }) {
  const id = useId();
  return <div className="channel-select-field"><label htmlFor={id}>{label}</label><select id={id} aria-label={label} value={value || ''} disabled={disabled} onChange={(event) => onChange(event.target.value)}>{options.map((option) => <option key={option.id} value={option.id}>{option.name}</option>)}</select></div>;
}

// 添加与编辑共用同一字段布局和后端预设目录。
export default function ConnectionFields({ state, editing = false }) {
  const { draft, patch, presets, chooseProfile, pending, results } = state;
  const preset = presets.find((item) => item.id === draft.provider_id);
  const profile = preset?.profiles.find((item) => item.id === draft.profile_id);
  const providerOptions = presets.some((item) => item.id === draft.provider_id) ? presets
    : [{ id: draft.provider_id, name: draft.provider_id || '通用兼容服务' }, ...presets];
  const profiles = preset?.profiles || [];
  const profileOptions = profiles.some((item) => item.id === draft.profile_id) ? profiles
    : [{ id: draft.profile_id || 'current', name: `当前协议：${draft.type || '等待预设'}` }, ...profiles];
  const discovery = draft.connection_options.model_discovery;
  return <>
    <section className="channel-form-block" aria-label="渠道信息">
      <header className="channel-block-heading"><h3>渠道信息</h3></header>
      <div className="channel-block-content channel-identity-row">
        <ChannelIconPicker compact draft={draft} onChange={(icon) => patch({ icon })} onProcessingChange={state.setIconPending} />
        <TextField isRequired name="name" value={draft.name} onChange={(name) => patch({ name })} autoFocus autoComplete="off" validate={(value) => !value?.trim() ? '请填写渠道名称' : null}>
          <Label>渠道名称</Label><Input placeholder="例如：DeepSeek 工作账号" /><FieldError />
        </TextField>
      </div>
    </section>
    <section className="channel-form-block" aria-label="连接配置">
      <header className="channel-block-heading"><h3>连接配置</h3><FieldHelp title="连接配置">
        <p>厂商预设会填写协议和默认地址，模型权限以实际账号为准。</p>
        <p>API 地址填写服务商提供的 Base URL，保留所需的 /v1 等路径前缀，不要填写到具体操作接口。检查配置仅在本地解析端点，不会请求上游。</p>
        {editing && <p>API Key 留空会保留已保存的密钥。“使用预设地址”只修改当前草稿，保存后生效。</p>}
        {profile?.help && <p>{profile.help}</p>}
      </FieldHelp></header>
      <div className="channel-block-content">
        {state.presetError && <div role="alert" className="channel-form-error">预设加载失败：{state.presetError}<Button type="button" size="sm" variant="secondary" onPress={() => state.loadPresets()}>重试</Button></div>}
        <div className="channel-fields-grid">
          <Choice label="厂商预设" value={draft.provider_id} options={providerOptions} disabled={!presets.length} onChange={(id) => {
            const next = presets.find((item) => item.id === id);
            if (next) chooseProfile(next, next.profiles.find((item) => item.id === next.default_profile) || next.profiles[0]);
          }} />
          <Choice label="接口协议" value={draft.profile_id || 'current'} options={profileOptions} disabled={!profiles.length} onChange={(id) => chooseProfile(preset, profiles.find((item) => item.id === id))} />
          <TextField isRequired name="base_url" value={draft.base_url} onChange={(base_url) => patch({ base_url })} autoComplete="off" className="channel-field-full" validate={(value) => !value?.trim() ? '请填写 API 地址' : null}>
            <Label>API 地址</Label><Input placeholder="https://api.example.com/v1" /><FieldError />
          </TextField>
          <TextField isRequired={!editing} name="key" type="password" value={draft.key} onChange={(key) => patch({ key })} autoComplete="new-password" className="channel-field-full" validate={(value) => !editing && !value?.trim() ? '请填写 API Key' : null}>
            <Label>API Key</Label><Input placeholder={editing ? '留空保留当前密钥' : '填写服务商的 API Key'} /><FieldError />
          </TextField>
        </div>
        <div className="channel-connection-actions">
          <Button type="button" size="sm" variant="secondary" isPending={pending.resolve} isDisabled={!draft.type || !draft.base_url.trim()} onPress={state.resolve}>检查配置</Button>
          {editing && profile?.base_url && draft.base_url !== profile.base_url && <Button type="button" size="sm" variant="tertiary" onPress={() => chooseProfile(preset, profile, true)}>使用预设地址</Button>}
        </div>
        {results.resolve?.error && <p role="alert" className="channel-form-error">{results.resolve.error}</p>}
        {results.resolve?.chat_url && <p role="status" className="channel-form-result">配置有效 · {results.resolve.chat_url}</p>}
      </div>
    </section>
    <details className="channel-form-block channel-advanced-block">
      <summary><span>高级设置</span><ChevronDown className="size-4" /></summary>
      <div className="channel-block-content">
        <div className="channel-advanced-help"><FieldHelp title="高级设置">
          <p>收到 429 后，按模型仅冷却当前模型；整条渠道会冷却本渠道全部模型。</p>
          <p>并发上限 0 表示不限制。满载时故障转移使用备用，手动模式不自动切换；仅统计本进程生成请求，包含流式和兼容重试。</p>
          <p>自动同步会定时新增上游全部模型，包含本次未选择和以后发现的模型。手动发现模式不启用同步。</p>
          {editing && <p>思考内容兼容会为缺少历史思考内容的工具调用补齐兼容字段；若中转服务不接受此字段，可以关闭。</p>}
        </FieldHelp></div>
        <div className="channel-fields-grid">
          <Choice label="限流范围" value={draft.rate_limit_scope} options={[{ id: 'model', name: '按模型' }, { id: 'channel', name: '整条渠道' }]} onChange={(rate_limit_scope) => patch({ rate_limit_scope })} />
          <TextField isRequired name="max_concurrent_requests" value={draft.max_concurrent_requests} onChange={(max_concurrent_requests) => patch({ max_concurrent_requests })} validate={validateChannelCapacity}>
            <Label>并发上限</Label><Input inputMode="numeric" placeholder="0 表示不限制" /><FieldError />
          </TextField>
          <div className="channel-field-full"><Choice label="模型发现方式" value={discovery} options={[{ id: 'api', name: '从上游获取' }, { id: 'manual', name: '手动填写' }]} disabled={profile?.model_discovery === 'manual'} onChange={(model_discovery) => patch({ connection_options: { schema_version: 1, model_discovery }, auto_sync: model_discovery === 'manual' ? false : draft.auto_sync })} /></div>
        </div>
        <label className="channel-toggle-row"><span>自动同步模型</span><input type="checkbox" checked={draft.auto_sync} disabled={discovery === 'manual'} onChange={(event) => patch({ auto_sync: event.target.checked })} /></label>
        {editing && <label className="channel-toggle-row"><span>思考内容兼容</span><input type="checkbox" checked={draft.thinking_compat} onChange={(event) => patch({ thinking_compat: event.target.checked })} /></label>}
      </div>
    </details>
  </>;
}
