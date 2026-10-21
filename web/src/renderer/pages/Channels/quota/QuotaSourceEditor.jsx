import { useEffect, useRef } from 'react';
import { Button } from '@heroui/react';
import CustomQuotaEditor from './CustomQuotaEditor';
import useQuotaDraft from './useQuotaDraft';
import { QuotaSourceState } from './QuotaSummary';
import { canKeepQuotaToken, duplicateQuotaSource } from './quotaModel';

export default function QuotaSourceEditor({ channelId, source, adapters, sources, onSaved, onCommitted, onCancel }) {
  const state = useQuotaDraft({ channelId, source, adapters, sources, onSaved, onCommitted });
  const { draft, patch, pending } = state;
  const adapter = adapters.find((item) => item.id === draft.adapter_id);
  const keepToken = canKeepQuotaToken(source, draft);
  const duplicate = duplicateQuotaSource(sources, draft, source?.id);
  const form = useRef(null);
  const scroll = useRef(null);
  useEffect(() => {
    if (state.preview && scroll.current) {
      scroll.current.scrollTo({ top: scroll.current.scrollHeight, behavior: window.matchMedia('(prefers-reduced-motion: reduce)').matches ? 'auto' : 'smooth' });
    }
  }, [state.preview]);
  const run = (mode) => { if (form.current.reportValidity()) state.run(mode); };
  return <form ref={form} className="quota-editor" onInvalidCapture={(event) => { const details = event.target.closest('details'); if (details) details.open = true; }} onSubmit={(event) => { event.preventDefault(); run('save'); }}>
    <div className="quota-editor-scroll" ref={scroll}>
    <fieldset disabled={!!pending && pending !== 'preview'} className="quota-editor-fields">
      <section className="quota-form-section">
      <div className="quota-section-title"><span>01</span><h3>基本信息</h3></div>
      <div className="quota-form-grid">
      <label>查询类型<select required value={draft.adapter_id} onChange={(event) => state.chooseAdapter(event.target.value)}>
        {adapters.map((item) => <option key={item.id} value={item.id}>{item.name}</option>)}
      </select></label>
      </div>
      {adapter?.description && <p className="text-xs text-muted">{adapter.description}</p>}
      {duplicate && <p role="status" className="text-xs text-muted">与「{duplicate.name}」使用相同查询类型和地址，可能重复展示同一账户额度。</p>}
      <div className="quota-form-grid">
      <label>来源名称<input required value={draft.name} onChange={(event) => patch({ name: event.target.value })} /></label>
      <label>查询根地址<input required type="url" value={draft.query_base_url} onChange={(event) => patch({ query_base_url: event.target.value, ...(draft.adapter_id === 'custom.balance' ? { token: '', clearToken: false } : {}) })} /></label>
      </div>
      <p className="quota-help">凭据将发送到：{draft.query_base_url || '请填写查询地址'}。查询地址独立于渠道推理地址。</p>
      </section>
      <section className="quota-form-section">
      <div className="quota-section-title"><span>02</span><h3>查询凭据</h3></div>
      <label>查询凭据<select value={draft.credential_mode} onChange={(event) => state.chooseCredential(event.target.value)}>
        {(adapter?.credential_modes || []).map((mode) => <option key={mode} value={mode}>{mode === 'inherit_channel_key' ? '继承当前渠道 Key' : mode === 'none' ? '无鉴权' : '专用查询 Token'}</option>)}
      </select></label>
      {draft.adapter_id === 'custom.balance' && source?.has_token && !keepToken && <p className="text-xs text-muted">查询目标已变更，请重新填写专用 Token。</p>}
      {draft.credential_mode === 'dedicated' && <>
        <label>专用 Token<input type="password" autoComplete="new-password" value={draft.token} disabled={draft.clearToken}
          placeholder={keepToken ? '已保存；留空保留现有 Token' : '填写专用 Token'} onChange={(event) => patch({ token: event.target.value })} /></label>
        {keepToken && <label className="quota-check"><input type="checkbox" checked={draft.clearToken} onChange={(event) => patch({ clearToken: event.target.checked, token: '' })} />清除已保存的专用 Token</label>}
      </>}
      </section>
      {draft.adapter_id === 'custom.balance' && <CustomQuotaEditor draft={draft} patch={patch} methods={adapter?.request_methods} />}
      {!!adapter?.fields?.length && <section className="quota-form-section">
      <div className="quota-section-title"><span>03</span><h3>查询参数</h3></div>
      <div className="quota-form-grid">
      {(adapter?.fields || []).map((field) => <label key={field.key}>{field.label}
        <input required={field.required} value={draft.options[field.key] ?? ''} onChange={(event) => patch({ options: { ...draft.options, [field.key]: event.target.value } })} />
        {field.help && <span className="text-xs text-muted">{field.help}</span>}
      </label>)}
      </div></section>}
      <section className="quota-form-section">
      <div className="quota-section-title"><span>{draft.adapter_id === 'custom.balance' ? '05' : adapter?.fields?.length ? '04' : '03'}</span><h3>刷新选项</h3></div>
      <div className="quota-form-grid quota-refresh-fields">
      <label className="quota-check"><input type="checkbox" checked={draft.enabled} onChange={(event) => patch({ enabled: event.target.checked })} />启用查询</label>
      <label>自动刷新间隔（分钟，0 为关闭，1–60）<input type="number" min="0" max="60" step="1" required value={draft.auto_refresh_minutes}
        onChange={(event) => patch({ auto_refresh_minutes: event.target.value })} /></label>
      </div>
      <p className="quota-help">新来源默认关闭。“测试草稿”不保存；“保存并查询”启用来源并只查询一次，定时刷新设为关闭。</p>
      </section>
    </fieldset>
    {state.preview && <div className="quota-preview"><h4 className="text-sm font-medium mb-2">草稿测试结果（未保存）</h4><QuotaSourceState view={state.preview} details /></div>}
    </div>
    <div className="quota-editor-footer">
    {state.error && <p role="alert" className="quota-editor-error text-sm text-danger">{state.error}</p>}
      <Button type="button" variant="secondary" size="sm" onPress={onCancel}>取消</Button>
      <Button type="button" variant="secondary" size="sm" isDisabled={!!pending || !adapter} isPending={pending === 'preview'} onPress={() => run('preview')}>测试草稿</Button>
      <Button type="submit" variant="secondary" size="sm" isDisabled={!!pending || !adapter} isPending={pending === 'save'}>保存</Button>
      <Button type="button" variant="primary" size="sm" isDisabled={!!pending || !adapter} isPending={pending === 'query'} onPress={() => run('query')}>保存并查询</Button>
    </div>
  </form>;
}
