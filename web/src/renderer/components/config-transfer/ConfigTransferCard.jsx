import { useEffect, useRef, useState, useSyncExternalStore } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Button, Card, Input, Label, Modal, Spinner, TextField, Typography } from '@heroui/react';
import { CheckCircle, Download, Eye, EyeOff, FileLock2, LockKeyhole, Upload, X } from 'lucide-react';
import { configTransfer } from '../../api/configTransfer';
import { responseCache } from '../../api/responseCache';
import { listChannels, listGroups, listModels } from '../../api';
import { setNavBlock } from '../../utils/navGuard';
import IconButton from '../IconButton';
import { createTransferController } from './controller';
import { isTransferBusy, passwordError, ZERO_COUNTS } from './model';
import TransferDetails, { Counts, RenameList, ResultGroups } from './TransferDetails';
import './config-transfer.css';

const stageLabels = { input: '输入检查', body_read: '读取文件', snapshot: '读取配置', encrypt: '加密', decrypt: '解密',
  validate: '配置校验', plan: '名称计划', commit: '导入写入', recovery: '结果核实', file_read: '读取文件', file_save: '保存文件' };
const failureLabels = { SESSION_LOST: '上次会话已结束，配置没有提交。', IMPORT_FAILED: '配置写入失败，本次变更已全部回滚。',
  PREVIEW_CHANGED: '确认时名称已被占用，旧计划没有写入。', PREVIEW_EXPIRED: '预览已过期，本次没有写入。', USER_CANCELLED: '本次操作已取消。' };
const formatBytes = (bytes) => bytes >= 1048576 ? `${(bytes / 1048576).toFixed(1)} MiB` : `${Math.ceil(bytes / 1024)} KiB`;
const formatTime = (time) => time && !Number.isNaN(Date.parse(time)) ? new Date(time).toLocaleString('zh-CN') : '上次会话';

function PasswordField({ id, label, value, onChange, error, disabled }) {
  const [visible, setVisible] = useState(false);
  return <div className="transfer-password-field"><TextField id={id} type={visible ? 'text' : 'password'} value={value} onChange={onChange}
    autoComplete="off" validationBehavior="aria" isDisabled={disabled} isInvalid={!!error} aria-describedby={error ? `${id}-error` : undefined}>
    <Label>{label}</Label><div className="transfer-password-wrap"><Input className="transfer-password-input" spellCheck={false} />
      <IconButton className="transfer-password-eye" label={`${visible ? '隐藏' : '显示'}${label}`} disabled={disabled} onClick={() => setVisible(!visible)}>{visible ? <EyeOff size={16} /> : <Eye size={16} />}</IconButton></div>
  </TextField>{error && <p id={`${id}-error`} role="alert" className="transfer-field-error">{error}</p>}</div>;
}

function Steps({ step }) {
  return <ol className="transfer-steps" aria-label="导入步骤">{['选择文件', '预览配置', '完成'].map((label, index) =>
    <li key={label} aria-current={index === step ? 'step' : undefined} className={index === step ? 'is-current' : ''}><span>{index < step ? '✓' : index + 1}</span>{label}</li>)}</ol>;
}

function Limits({ summary }) {
  const limits = summary?.limits;
  if (!limits) return null;
  return <details className="transfer-limits"><summary>查看文件与数量限制</summary><p>文件格式 {summary.format_version}；文件不超过 {formatBytes(limits.file_bytes)}；密码为 1～{limits.password_bytes} 字节。</p>
    <p>最多 {limits.channels} 个渠道、{limits.models} 个模型、{limits.groups} 个分组、{limits.items} 个分组成员。预览保留 {Math.floor(limits.preview_ttl_seconds / 60)} 分钟。</p></details>;
}

export default function ConfigTransferCard() {
  const navigate = useNavigate();
  const location = useLocation();
  const [controller] = useState(() => createTransferController(configTransfer, { onCommitted: () => {
    responseCache.invalidate();
    return Promise.allSettled([listChannels(), listModels({ page: 1, page_size: 1 }), listGroups()]);
  } }));
  const state = useSyncExternalStore(controller.subscribe, controller.getSnapshot);
  const lifecycle = useRef(0);
  const opener = useRef(null);
  const [password, setPassword] = useState('');
  const [confirmation, setConfirmation] = useState('');
  const [errors, setErrors] = useState({});
  const busy = isTransferBusy(state.stage);

  useEffect(() => {
    if (location.state?.configTransfer) document.getElementById('config-transfer')?.scrollIntoView({ block: 'nearest' });
  }, [location.state]);

  useEffect(() => {
    ++lifecycle.current;
    void controller.loadSummary();
    void controller.loadRecoveries({ autoOpen: true });
    return () => {
      const version = ++lifecycle.current;
      // React StrictMode 的同步重挂不销毁仍在使用的 controller。
      queueMicrotask(() => { if (lifecycle.current === version) controller.dispose(); });
    };
  }, [controller]);
  useEffect(() => {
    setNavBlock('config-transfer', busy ? '配置迁移正在处理，请等待本次操作结果' : state.mode ? '配置迁移尚未完成' : null, { locked: busy });
    return () => setNavBlock('config-transfer', null);
  }, [busy, state.mode]);
  useEffect(() => {
    if (!state.mode || ['result', 'exported', 'preview'].includes(state.stage)) {
      setPassword(''); setConfirmation(''); setErrors({});
    }
  }, [state.mode, state.stage]);

  const open = (mode, event) => {
    opener.current = event?.currentTarget || document.activeElement;
    setPassword(''); setConfirmation(''); setErrors({});
    controller.open(mode);
  };
  const close = () => {
    if (!controller.close()) return;
    setPassword(''); setConfirmation(''); setErrors({});
    requestAnimationFrame(() => opener.current?.focus?.());
  };
  const choose = () => { setPassword(''); setConfirmation(''); setErrors({}); void controller.chooseFile(); };
  const editPassword = (value) => { setPassword(value); setErrors((old) => ({ ...old, password: '' })); };
  const editConfirmation = (value) => { setConfirmation(value); setErrors((old) => ({ ...old, confirmation: '' })); };
  const submit = (event) => {
    event.preventDefault();
    if (busy) return;
    const next = { password: passwordError(password, state.summary?.limits?.password_bytes || 1024) };
    if (state.mode === 'export') next.confirmation = password !== confirmation ? '两次输入的密码不一致' : !confirmation ? '请再次输入密码' : '';
    setErrors(next);
    if (Object.values(next).some(Boolean)) return;
    if (state.mode === 'export') void controller.exportConfig(password);
    else void controller.preview(password);
  };
  const viewGroups = () => {
    const importedGroupIds = (state.result?.groups || []).map((group) => String(group.id));
    close();
    navigate('/groups', { state: { importedGroupIds } });
  };
  const previewing = ['preview', 'committing'].includes(state.stage);
  const terminal = state.stage === 'result';
  const wide = previewing || terminal;
  const exported = state.stage === 'exported';
  const title = exported ? '导出完成' : state.mode === 'export' ? '导出配置' : terminal ? '导入结果' : previewing ? '导入预览' : ['checking', 'unconfirmed'].includes(state.stage) ? '核实导入结果' : '导入配置';

  return <>
    <Card className="settings-card gap-4 p-5" id="config-transfer">
      <Card.Header className="transfer-card-heading p-0"><Typography type="body-sm" className="settings-section-title text-muted">配置迁移</Typography>
        <div className="text-xs text-muted" aria-live="polite">{state.summaryStatus === 'loading' ? '正在读取本机配置…' : state.summaryStatus === 'ready' ?
          `本机现有：${state.summary.counts.channels} 个渠道 · ${state.summary.counts.models} 个模型 · ${state.summary.counts.groups} 个分组` : '本机配置摘要加载失败'}</div></Card.Header>
      <Card.Content className="p-0"><div className="settings-rows">
        {state.summaryStatus === 'error' && <div className="transfer-inline-error" role="alert"><span>{state.summaryError}</span><Button size="sm" variant="tertiary" onPress={controller.loadSummary}>重试</Button></div>}
        <div className="settings-row"><div className="settings-row-copy"><Typography className="settings-label">导出配置</Typography>
          <Typography type="body-sm" className="settings-hint">将全部渠道、模型和分组保存为密码保护的文件{state.summaryStatus === 'ready' && !state.summary.can_export ? '；当前没有可导出的配置' : ''}</Typography></div>
          <Button variant="secondary" size="sm" isDisabled={!!state.mode || state.summaryStatus !== 'ready' || !state.summary?.can_export || state.summary?.ready === false} onPress={(event) => open('export', event)}><Download size={16} />导出配置</Button></div>
        <div className="settings-row"><div className="settings-row-copy"><Typography className="settings-label">导入配置</Typography>
          <Typography type="body-sm" className="settings-hint">从文件恢复配置，保留本机现有内容，同名项自动改名</Typography></div>
          <Button variant="primary" size="sm" isDisabled={!!state.mode} onPress={(event) => open('import', event)}><Upload size={16} />导入配置</Button></div>
        <Limits summary={state.summary} />
        <div className="transfer-recovery"><div className="flex items-center justify-between gap-2"><span className="text-xs text-muted">{state.recoveries.length ? '上次尚未确认查看的导入结果' : '导入结果恢复'}</span>
          <Button size="sm" variant="tertiary" isDisabled={!!state.mode || state.recoveryStatus === 'loading'} onPress={() => controller.loadRecoveries()}>{state.recoveryStatus === 'loading' ? '读取中…' : '刷新结果'}</Button></div>
          {state.recoveryError && <p role="alert" className="transfer-field-error">{state.recoveryError}</p>}
          {state.recoveries.map((item) => <div className="transfer-recovery-row" key={item.operation_id}><span>{formatTime(item.confirmed_at || item.created_at)} · {item.state === 'committed' ? '已完成' : item.state === 'aborted' ? '未提交' : '待核实'}</span>
            <Button size="sm" variant="secondary" isDisabled={!!state.mode} onPress={() => controller.openRecovery(item)}>查看结果</Button></div>)}
          {state.recoveryCursor && <Button size="sm" variant="tertiary" isDisabled={!!state.mode || state.recoveryStatus === 'loading'} onPress={() => controller.loadRecoveries({ more: true })}>加载更多结果</Button>}
        </div>
      </div></Card.Content>
    </Card>

    {state.mode && <Modal.Backdrop isOpen isDismissable={!busy} isKeyboardDismissDisabled={busy} onOpenChange={(isOpen) => { if (!isOpen) close(); }}>
      <Modal.Container size={wide ? 'xl' : 'md'} className={wide ? 'transfer-container transfer-container--wide' : 'transfer-container'}><Modal.Dialog className="transfer-dialog">
        <Modal.Header><Modal.Heading>{title}</Modal.Heading><p className="transfer-subtitle">{state.mode === 'export' ? '导出本机全部渠道、模型和分组，迁移到另一台电脑。' : '全部配置作为新增导入；已有渠道、模型和分组保持原样。'}</p></Modal.Header>
        <button type="button" className="transfer-close" aria-label="关闭配置迁移" disabled={busy} onClick={close}><X size={18} /></button>
        <Modal.Body>
          {state.mode === 'import' && <Steps step={terminal ? 2 : previewing || ['checking', 'unconfirmed'].includes(state.stage) ? 1 : 0} />}
          {state.recoveredAt && <p className="transfer-recovered">上次操作：{formatTime(state.recoveredAt)} · 从本机操作回执恢复</p>}
          {busy && <div className="transfer-processing" role="status"><Spinner size="sm" />{state.stage === 'exporting' ? '正在选择保存位置、加密并导出…' : state.stage === 'selecting' ? '正在选择配置文件…' : state.stage === 'decrypting' ? '正在解密并完整校验…' : state.stage === 'committing' ? '正在导入配置，请等待结果…' : '正在核实本次导入结果，请勿重复导入…'}</div>}
          {state.error && <div className={`transfer-notice ${state.changed || state.stage === 'unconfirmed' ? 'transfer-notice--warning' : 'transfer-notice--danger'}`} role="alert"><strong>{state.error}</strong>
            {state.errorField && <p>位置：{state.errorField}</p>}{state.errorStage && <p>阶段：{stageLabels[state.errorStage] || state.errorStage}</p>}</div>}
          <fieldset disabled={busy} className="transfer-content">
          {state.mode === 'export' && !exported && <><Counts counts={state.summary?.counts} /><p className="transfer-scope"><LockKeyhole size={16} />包含上游访问凭据、模型归属和分组路由设置。文件使用你设置的密码保护。</p>
            <form id="transfer-export-form" noValidate onSubmit={submit} className="transfer-form"><PasswordField id="transfer-export-password" label="文件密码" value={password} onChange={editPassword} error={errors.password} disabled={busy} />
              <PasswordField id="transfer-confirm-password" label="确认密码" value={confirmation} onChange={editConfirmation} error={errors.confirmation} disabled={busy} />
              <p className="transfer-detail-sub">请妥善保管文件和密码，密码丢失后无法解密。</p></form></>}
          {exported && <><div className="transfer-result-heading"><CheckCircle className="text-success" size={28} /><div><strong>加密配置文件已保存</strong><p>{state.exported.file_name}</p></div></div>
            <Counts counts={state.exported.counts} /><p className="transfer-detail-sub">{formatBytes(state.exported.bytes)} · 文件格式 {state.exported.format_version} · 受文件密码保护</p></>}
          {state.mode === 'import' && ['import', 'selecting', 'decrypting'].includes(state.stage) && <>
            {state.file ? <div className="transfer-selected-file"><FileLock2 size={26} /><div><strong>{state.file.file_name}</strong><p>{formatBytes(state.file.bytes)} · 已选择文件，尚未校验</p></div><Button size="sm" variant="tertiary" isDisabled={busy} onPress={choose}>更换文件</Button></div>
              : <button type="button" className="transfer-file-picker" disabled={busy} onClick={choose}><Upload size={26} /><strong>选择配置文件</strong><span>选择从 Portunus 导出的 .portunus 文件</span></button>}
            <form id="transfer-import-form" noValidate className="transfer-form" onSubmit={submit}><PasswordField id="transfer-import-password" label="文件密码" value={password} onChange={editPassword} error={errors.password} disabled={busy} />
              <p className="transfer-detail-sub">支持文件格式 1，来源应用版本可以不同。先离线检查完整配置，通过后展示预览，确认前不写入业务配置。</p></form><Limits summary={state.summary} />
          </>}
          {previewing && state.preview && <>{state.file && <div className="transfer-selected-file"><FileLock2 size={24} /><div><strong>{state.file.file_name}</strong><p>{formatBytes(state.file.bytes)} · 密码保护的配置文件</p></div></div>}<Counts counts={state.preview.counts} /><div className="transfer-preview-meta"><span>导出应用 v{state.preview.file_info.app_version}</span><span>文件格式 {state.preview.file_info.format_version} · 受支持</span><span>预览有效至 {formatTime(state.preview.expires_at)}</span></div>
            <div className="transfer-notice"><p>全部新增，保留已保存的连接配置；不会自动请求上游验证可用性。服务接入信息使用本机设置。</p></div>
            <RenameList key={`${state.preview.preview_id}-renames`} data={state.preview} /><TransferDetails key={state.preview.preview_id} preview={state.preview} disabled={busy} /></>}
          {terminal && <><div className="transfer-result-heading"><CheckCircle className={state.result.state === 'committed' ? 'text-success' : 'text-danger'} size={28} /><div><strong>{state.result.state === 'committed' ? '配置恢复完成' : '导入未完成，未新增任何配置'}</strong>
            <p>{state.result.state === 'committed' ? (state.result.replayed ? '已核实本次操作先前提交成功，没有重复新增。' : '全部配置和关联已恢复。') : '本次操作已确认未提交，原有配置保留。'}</p></div></div>
            <Counts counts={state.result.state === 'committed' ? state.result.counts : ZERO_COUNTS} />
            {state.result.state === 'committed' ? <><RenameList data={state.result} completed /><ResultGroups groups={state.result.groups} /><div className="transfer-notice"><strong>使用本机接入信息</strong><p>客户端使用本机地址、服务接入 Key 和最终分组名称。配置恢复完成不代表上游当前在线。</p></div></>
              : <div className="transfer-notice transfer-notice--danger"><strong>{failureLabels[state.result.failure_code] || '本次配置变更已全部回滚，可以检查后重新选择文件。'}</strong><p>原因：{state.result.failure_code} · 阶段：{stageLabels[state.result.stage] || state.result.stage}</p></div>}
          </>}
          {state.stage === 'unconfirmed' && <p className="transfer-detail-sub">本次操作号：{state.operationId}。结果尚不确定；关闭后仍可从配置迁移中的结果恢复入口核实。</p>}
          </fieldset>
        </Modal.Body>
        <Modal.Footer>
          {previewing && <span className="transfer-footer-note">新增 {state.preview.counts.channels} 个渠道 · {state.preview.counts.models} 个模型 · {state.preview.counts.groups} 个分组</span>}
          {state.stage === 'exported' || terminal ? <>{terminal && state.result.state === 'committed' && state.result.groups?.length > 0 && <Button variant="secondary" onPress={viewGroups}>查看导入分组</Button>}<Button variant="primary" onPress={close}>完成</Button></>
            : state.stage === 'unconfirmed' ? <><Button variant="secondary" onPress={close}>稍后核实</Button><Button variant="primary" onPress={controller.continueQuery}>继续查询</Button></>
            : state.stage === 'checking' ? <Button variant="secondary" isDisabled>正在核实…</Button>
            : previewing ? <><Button variant="secondary" isDisabled={busy} onPress={() => { setPassword(''); controller.backToFile(); }}>重新选文件</Button><Button variant="primary" isDisabled={busy} isPending={busy} onPress={controller.commit}>{busy ? '导入中…' : state.changed ? '确认更新后的导入' : '确认导入'}</Button></>
            : <><Button variant="secondary" isDisabled={busy} onPress={close}>取消</Button><Button type="submit" form={state.mode === 'export' ? 'transfer-export-form' : 'transfer-import-form'} variant="primary" isDisabled={busy || (state.mode === 'import' && !state.file)} isPending={busy}>{state.mode === 'export' ? '选择位置并导出' : '解密并预览'}</Button></>}
        </Modal.Footer>
      </Modal.Dialog></Modal.Container>
    </Modal.Backdrop>}
  </>;
}
