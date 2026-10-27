import { useState } from 'react';
import { Button, FieldError, Input, Label, TextField } from '@heroui/react';
import { ChevronDown } from 'lucide-react';
import AppComboBox from '../../components/AppComboBox';
import FieldHelp from './FieldHelp';

export default function CallTestPanel({ state, existing = [] }) {
  const [model, setModel] = useState('');
  const [stream, setStream] = useState(false);
  const [limit, setLimit] = useState('32');
  const { pending, results } = state;
  const change = (setter) => (value) => { state.clearResult('test'); setter(value); };
  const result = results.test;
  const validLimit = Number.isInteger(Number(limit)) && Number(limit) >= 1 && Number(limit) <= 128;
  const models = [...new Set([...state.selected, ...existing.map((item) => item.name)])];
  return <details className="channel-form-block channel-test-block" aria-label="主动调用验证">
    <summary><span>调用验证</span><ChevronDown className="size-4" /></summary>
    <div className="channel-block-content">
      <div className="channel-model-subheading"><span className="channel-test-warning">可能消耗上游额度</span><FieldHelp title="调用验证"><p>点击验证按钮后，会向当前草稿连接发送一次指定模型请求。输出上限不代表总消耗上限。</p><p>每次只验证一个模型，不自动重试或轮测。未保存配置不影响正式调用，验证结果只代表本次请求。</p></FieldHelp></div>
      <div className="channel-fields-grid">
        <AppComboBox label="测试模型" value={model} onChange={change(setModel)} options={models} placeholder="上游模型名" />
        <TextField value={limit} onChange={change(setLimit)} isInvalid={!validLimit}><Label>最大输出 Token</Label><Input type="number" min="1" max="128" step="1" /><FieldError>请输入 1–128 的整数</FieldError></TextField>
      </div>
      <label className="channel-toggle-row"><span>流式验证</span><input type="checkbox" checked={stream} onChange={(event) => change(setStream)(event.target.checked)} /></label>
      <div className="channel-inline-actions">
        {pending.test && <Button type="button" size="sm" variant="secondary" onPress={() => state.clearResult('test')}>取消验证</Button>}
        <Button type="button" size="sm" variant="secondary" isPending={pending.test} isDisabled={!state.canConnect || !model.trim() || !validLimit} onPress={() => state.test({ model: model.trim(), stream, max_output_tokens: Number(limit) })}>发送验证请求</Button>
      </div>
      {result?.error && <p role="alert" className="channel-form-error">验证失败：{result.error}</p>}
      {result && !result.error && <div role="status" className={result.success ? 'channel-form-result' : 'channel-form-error'}><p>{result.success ? '本次验证通过' : '本次验证失败'}：{result.message}</p><p>模型 {result.model} · {result.stream ? '流式' : '非流式'} · 输出上限 {result.max_output_tokens} · 阶段 {result.stage} · HTTP {result.status || '—'} · {result.duration_ms} ms</p></div>}
    </div>
  </details>;
}
