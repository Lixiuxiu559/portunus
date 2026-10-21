import { customItem } from './customQuotaModel';

const unitKinds = [['money', '金额'], ['count', '次数 / 积分'], ['tokens', 'Token'], ['percent', '百分比']];
function UnitEditor({ value, onChange, prefix }) {
  return <div className="quota-form-grid"><label>{prefix}类型<select value={value.kind} onChange={(e) => onChange({ ...value, kind: e.target.value })}>{unitKinds.map(([id, name]) => <option key={id} value={id}>{name}</option>)}</select></label>
    <label>{prefix}代码<input required placeholder="如 CNY、USD、credits" value={value.code} onChange={(e) => onChange({ ...value, code: e.target.value })} /></label></div>;
}
function ConditionEditor({ title, value, text, onChange, onText }) {
  return <div className="flex flex-col gap-2"><label className="quota-check"><input type="checkbox" checked={!!value} onChange={(e) => onChange(e.target.checked ? { path: '', equals_json: 'true' } : undefined)} />{title}</label>
    {value && <div className="quota-form-grid"><label>条件字段路径<input required placeholder="/success" value={value.path} onChange={(e) => onChange({ ...value, path: e.target.value })} /></label>
      <label>等于（JSON 标量）<input required value={text} placeholder={'true 或 "success"'} onChange={(e) => onText(e.target.value)} /></label></div>}</div>;
}

export default function CustomQuotaEditor({ draft, patch, methods }) {
  const config = draft.options.custom;
  const texts = draft.customEditors;
  const change = (values) => patch({ options: { ...draft.options, custom: { ...config, ...values } } });
  const request = (values) => change({ request: { ...config.request, ...values } });
  const editors = (values) => patch({ customEditors: { ...texts, ...values } });
  const itemChange = (index, values) => change({ items: config.items.map((item, i) => i === index ? { ...item, ...values } : item) });
  const addItem = () => {
    let index = config.items.length + 1;
    while (config.items.some((item) => item.id === `balance_${index}`)) index++;
    const item = customItem(index);
    change({ items: [...config.items, item] });
  };
  return <section className="quota-custom" aria-label="自定义查询配置">
    <section className="quota-form-section">
    <div className="quota-section-title"><span>03</span><h3>请求配置</h3></div>
    <p className="text-xs text-muted">仅配置只读余额查询接口。POST 会按配置发送，不要填写充值、扣费或修改账户的接口。</p>
    <div className="quota-form-grid"><label>请求方法<select value={config.request.method} onChange={(e) => request({ method: e.target.value })}>{(methods || ['GET', 'POST']).map((method) => <option key={method}>{method}</option>)}</select></label>
      <label>站内请求路径<input required placeholder="/user/balance" value={config.request.path} onChange={(e) => request({ path: e.target.value })} /></label></div>
    <label>请求头（JSON 对象）<textarea rows="4" spellCheck="false" value={texts.headers} onChange={(e) => editors({ headers: e.target.value })} /></label>
    <p className="text-xs text-muted">专用 Token 填写在上方密码框，请求头使用 {'{{token}}'} 占位符。不要在请求头、路径、查询参数或请求体中填写真实密钥；Token 占位符只允许用于请求头。</p>
    {config.request.method === 'POST' && <label>POST 请求体（JSON，可留空）<textarea rows="3" spellCheck="false" value={config.request.body || ''} onChange={(e) => request({ body: e.target.value })} /></label>}
    </section>
    <section className="quota-form-section">
    <div className="quota-section-title"><span>04</span><h3>结果映射</h3></div>
    <p className="text-xs text-muted">字段使用 JSON Pointer：/data/balance；数组第一项为 /data/0/balance；字段名中的 / 写作 ~1，~ 写作 ~0。未提供的金额路径表示未知，数值 0 会保留为零。</p>
    <ConditionEditor title="检查业务成功条件（可选）" value={config.success} text={texts.success} onChange={(success) => change({ success })} onText={(success) => editors({ success })} />
    {config.items.map((item, index) => <div className="quota-custom-item" key={index}>
      <div className="flex items-center justify-between gap-2"><h4 className="font-medium"><span className="quota-item-index">{String(index + 1).padStart(2, '0')}</span>{item.label || '额度项'}</h4><button type="button" disabled={config.items.length === 1} onClick={() => change({ items: config.items.filter((_, i) => i !== index) })}>删除此项</button></div>
      <div className="quota-form-grid"><label>额度标识<input required value={item.id} onChange={(e) => {
        const id = e.target.value;
        patch({ options: { ...draft.options, custom: { ...config, items: config.items.map((row, i) => i === index ? { ...row, id } : row) } }, customEditors: { ...texts, unlimited: { ...texts.unlimited, [id]: texts.unlimited[item.id] ?? 'true' } } });
      }} /></label><label>显示名称<input required value={item.label} onChange={(e) => itemChange(index, { label: e.target.value })} /></label></div>
      <details className="quota-advanced"><summary>额度类型与归属</summary>
      <div className="quota-form-grid"><label>额度类型<select value={item.kind} onChange={(e) => itemChange(index, { kind: e.target.value })}><option value="balance">余额</option><option value="spending_limit">消费限额</option><option value="usage_window">周期用量</option></select></label>
        <label>归属范围<select value={item.scope} onChange={(e) => itemChange(index, { scope: e.target.value })}><option value="account">账户</option><option value="api_key">API Key</option><option value="plan">套餐</option></select></label></div>
      </details>
      <UnitEditor prefix="显示单位" value={item.unit} onChange={(unit) => itemChange(index, { unit })} />
      <div className="quota-amount-paths">
      {[[ 'remaining_path', '剩余额度路径'], ['used_path', '已用额度路径'], ['total_path', '总额度路径']].map(([key, label]) => <label key={key}>{label}<input placeholder="可留空，如 /data/balance" disabled={key === 'remaining_path' && item.remaining_from_total_used} value={item[key] || ''} onChange={(e) => itemChange(index, { [key]: e.target.value || undefined })} /></label>)}
      </div>
      <label className="quota-check"><input type="checkbox" checked={!!item.remaining_from_total_used} onChange={(e) => itemChange(index, { remaining_from_total_used: e.target.checked, ...(e.target.checked ? { remaining_path: undefined } : {}) })} />由总额度减已用额度计算剩余</label>
      <details className="quota-advanced" open={item.divisor ? true : undefined}><summary>数值换算</summary>
      <label>换算除数（可选，正十进制数）<input placeholder="如 500000；留空保留原始值" value={item.divisor || ''} onChange={(e) => itemChange(index, { divisor: e.target.value || undefined, original_unit: e.target.value ? item.original_unit || { kind: 'count', code: '' } : undefined })} /></label>
      {item.divisor && <UnitEditor prefix="原始单位" value={item.original_unit || { kind: 'count', code: '' }} onChange={(original_unit) => itemChange(index, { original_unit })} />}
      </details>
      <details className="quota-advanced" open={item.unlimited ? true : undefined}><summary>无限额度条件</summary><ConditionEditor title="满足条件时标记无限" value={item.unlimited} text={texts.unlimited[item.id] ?? 'true'} onChange={(unlimited) => itemChange(index, { unlimited })} onText={(text) => editors({ unlimited: { ...texts.unlimited, [item.id]: text } })} /></details>
    </div>)}
    <button type="button" className="quota-custom-add" disabled={config.items.length >= 20} onClick={addItem}>+ 添加额度项</button>
    </section>
  </section>;
}
