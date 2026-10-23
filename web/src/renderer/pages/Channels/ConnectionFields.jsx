import ChannelIconPicker from '../../components/ChannelIconPicker';
import { validateChannelCapacity } from './channelDraft';

import { Button, FieldError, Input, Label, ListBox, Select, TextField } from '@heroui/react';

function Choice({ label, value, options, onChange, disabled = false }) {
  return (
    <Select aria-label={label} selectedKey={value || null} onSelectionChange={onChange} isDisabled={disabled}>
      <Label>{label}</Label>
      <Select.Trigger><Select.Value /><Select.Indicator /></Select.Trigger>
      <Select.Popover className="dialog-options"><ListBox>
        {options.map((option) => <ListBox.Item key={option.id} id={option.id} textValue={option.name}>
          {option.name}<ListBox.ItemIndicator />
        </ListBox.Item>)}
      </ListBox></Select.Popover>
    </Select>
  );
}

// 后端目录是厂商和协议档的唯一来源。创建与编辑不再各维护协议常量。
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
  return (
    <>
      {state.presetError && <div role="alert" className="text-sm text-danger">
        预设加载失败：{state.presetError}
        <Button type="button" size="sm" variant="secondary" onPress={() => state.loadPresets()}>重试</Button>
      </div>}
      <div className="dialog-form-grid grid grid-cols-2 gap-3">
        <ChannelIconPicker draft={draft} onChange={(icon) => patch({ icon })} onProcessingChange={state.setIconPending} />
        <TextField isRequired name="name" value={draft.name} onChange={(name) => patch({ name })} autoFocus autoComplete="off"
          validate={(value) => !value?.trim() ? '请填写渠道名称' : null}>
          <Label>渠道名称</Label><Input placeholder="例如：官方 API" /><FieldError />
        </TextField>
        <Choice label="厂商预设" value={draft.provider_id} options={providerOptions} disabled={!presets.length}
          onChange={(id) => {
            const next = presets.find((item) => item.id === id);
            if (next) chooseProfile(next, next.profiles.find((item) => item.id === next.default_profile) || next.profiles[0]);
          }} />
        <Choice label="协议配置档" value={draft.profile_id || 'current'} options={profileOptions} disabled={!profiles.length}
          onChange={(id) => chooseProfile(preset, profiles.find((item) => item.id === id))} />
        <TextField isRequired name="base_url" value={draft.base_url} onChange={(base_url) => patch({ base_url })} autoComplete="off"
          validate={(value) => !value?.trim() ? '请填写 API 根地址' : null}>
          <Label>API 根地址（Base URL）</Label><Input placeholder="包含所需的 /v1 等前缀" /><FieldError />
        </TextField>
        <TextField isRequired={!editing} name="key" type="password" value={draft.key} onChange={(key) => patch({ key })}
          autoComplete="new-password" validate={(value) => !editing && !value?.trim() ? '请填写 API Key' : null}>
          <Label>API Key</Label><Input placeholder={editing ? '留空保留已保存的密钥' : '填写上游密钥'} /><FieldError />
        </TextField>
        <Choice label="限流范围" value={draft.rate_limit_scope}
          options={[{ id: 'model', name: '按模型（默认）' }, { id: 'channel', name: '整条渠道' }]}
          onChange={(rate_limit_scope) => patch({ rate_limit_scope })} />
        <TextField isRequired name="max_concurrent_requests" value={draft.max_concurrent_requests}
          onChange={(max_concurrent_requests) => patch({ max_concurrent_requests })}
          validate={validateChannelCapacity}>
          <Label>渠道并发上限</Label><Input inputMode="numeric" placeholder="0 表示不限制" /><FieldError />
        </TextField>
        <Choice label="模型发现方式" value={discovery}
          options={[{ id: 'api', name: '从上游获取' }, { id: 'manual', name: '手动填写' }]}
          disabled={profile?.model_discovery === 'manual'} onChange={(model_discovery) => patch({
            connection_options: { schema_version: 1, model_discovery },
            auto_sync: model_discovery === 'manual' ? false : draft.auto_sync,
          })} />
      </div>
      <div className="flex flex-col gap-2 text-xs text-muted">
        <span>出站协议：{draft.type || '请先选择配置档'}。地址需包含服务要求的版本或路径前缀，Portunus 只追加操作路径。</span>
        <span>收到 429 后，按模型仅冷却当前模型；整条渠道会冷却此渠道的全部模型。</span>
        <span>并发上限 0 表示不限制；满载时故障转移使用备用，手动不自动切换。仅统计当前进程 /v1 生成中的调用，包含流式和兼容重试。</span>
        {profile?.help && <span>{profile.help}</span>}
        {profile?.base_url && draft.base_url !== profile.base_url && <div className="flex flex-wrap items-center gap-2">
          <span>当前地址已保留。点击后才替换为此配置档默认地址。</span>
          <Button type="button" size="sm" variant="secondary" onPress={() => chooseProfile(preset, profile, true)}>应用预设地址</Button>
        </div>}
        <div className="flex flex-wrap items-center gap-2">
          <Button type="button" size="sm" variant="secondary" isPending={pending.resolve} isDisabled={!draft.type || !draft.base_url.trim()}
            onPress={state.resolve}>检查配置与端点</Button>
          <span>仅本地解析，不发送上游请求。</span>
        </div>
        {results.resolve?.error && <span role="alert" className="text-danger">{results.resolve.error}</span>}
        {results.resolve?.chat_url && <div role="status" className="break-all">配置有效 · 操作端点：<code>{results.resolve.chat_url}</code></div>}
      </div>
      <label className="flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-0.5" checked={draft.auto_sync} disabled={discovery === 'manual'}
          onChange={(event) => patch({ auto_sync: event.target.checked })} />
        <span>自动同步模型<span className="block text-xs text-muted">定时新增发现的全部模型，包含本次未选择和以后发现的模型。手动发现模式不启用。</span></span>
      </label>
      <label className="flex items-start gap-2 text-sm">
        <input type="checkbox" className="mt-0.5" checked={draft.thinking_compat} onChange={(event) => patch({ thinking_compat: event.target.checked })} />
        <span>thinking 兼容垫片<span className="block text-xs text-muted">用于严格思考模型：assistant 历史缺少思考内容时注入占位，避免上游拒绝多轮请求。</span></span>
      </label>
    </>
  );
}
