import { useId, useState } from 'react';
import { Button, FieldError, Input, Label, TextField } from '@heroui/react';

export default function CallTestPanel({ state, existing = [] }) {
  const [model, setModel] = useState('');
  const [stream, setStream] = useState(false);
  const [limit, setLimit] = useState('32');
  const listId = useId();
  const { pending, results } = state;
  const change = (setter) => (value) => { state.clearResult('test'); setter(value); };
  const result = results.test;
  const validLimit = Number.isInteger(Number(limit)) && Number(limit) >= 1 && Number(limit) <= 128;
  const models = [...new Set([...state.selected, ...existing.map((item) => item.name)])];
  return (
    <section className="flex flex-col gap-3 border-t border-separator pt-4" aria-label="主动调用验证">
      <span className="text-sm">主动调用验证</span>
      <p className="text-xs text-muted">点击后会向当前草稿连接发送一次指定模型请求，可能消耗上游额度。输出上限不代表总消耗上限；不自动重试或轮测模型。未保存配置不影响正式调用。</p>
      <div className="dialog-form-grid grid grid-cols-2 gap-3">
        <TextField value={model} onChange={change(setModel)}><Label>测试模型</Label><Input placeholder="填写单个上游模型名" list={listId} /></TextField>
        <datalist id={listId}>{models.map((name) => <option key={name} value={name} />)}</datalist>
        <TextField value={limit} onChange={change(setLimit)} isInvalid={!validLimit}>
          <Label>最大输出 token（1–128）</Label><Input type="number" min="1" max="128" step="1" /><FieldError>请输入 1–128 的整数</FieldError>
        </TextField>
      </div>
      <div className="flex flex-wrap items-center justify-between gap-3">
        <label className="flex items-center gap-2 text-sm"><input type="checkbox" checked={stream} onChange={(event) => change(setStream)(event.target.checked)} />流式验证</label>
        <div className="flex gap-2">
          {pending.test && <Button type="button" size="sm" variant="secondary" onPress={() => state.clearResult('test')}>取消验证</Button>}
          <Button type="button" size="sm" variant="secondary" isPending={pending.test} isDisabled={!state.canConnect || !model.trim() || !validLimit}
            onPress={() => state.test({ model: model.trim(), stream, max_output_tokens: Number(limit) })}>发送一次验证请求</Button>
        </div>
      </div>
      {result?.error && <p role="alert" className="text-xs text-danger">验证失败：{result.error}</p>}
      {result && !result.error && <div role="status" className={`text-xs ${result.success ? 'text-muted' : 'text-danger'}`}>
        <p>{result.success ? '本次调用验证通过' : '本次调用验证失败'}：{result.message}</p>
        <p className="break-all">模型 {result.model} · {result.stream ? '流式' : '非流式'} · 输出上限 {result.max_output_tokens} · 阶段 {result.stage} · HTTP {result.status || '—'} · {result.duration_ms} ms</p>
      </div>}
    </section>
  );
}
