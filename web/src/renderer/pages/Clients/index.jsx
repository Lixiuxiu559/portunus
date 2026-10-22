import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, toast } from '@heroui/react';
import { RefreshCw } from 'lucide-react';
import { listGroups } from '../../api/group';
import { getAPIKey, regenerateAPIKey } from '../../api/apikey';
import { buildConnectionGuide } from '../../utils/connectionGuide';
import { selectServiceGroup } from '../../utils/serviceAccess';
import ConfirmModal from '../../components/ConfirmModal';
import ConnectionCard from './ConnectionCard';
import TreeNav from './TreeNav';
import ExamplesPane from './ExamplesPane';
import ClaudeConfigPanel from './ClaudeConfigPanel';
import { normalizeSearch, parseSelection, selectionSearch } from './sectionState';
import '../service-access.css';

const STRATEGY_LABELS = { manual: '手动选择', round_robin: '轮询', failover: '故障转移' };
// 工作区顶部对齐视口顶部时留一点余量，和滚动目标共用同一个值
const WORKSPACE_TOP_GAP = 8;

export default function Clients() {
  const [searchParams, setSearchParams] = useSearchParams();
  const selection = useMemo(() => parseSelection(searchParams), [searchParams]);
  const requestedGroup = searchParams.get('group');

  const [networkMode, setNetworkMode] = useState(null);
  const [savingMode, setSavingMode] = useState(false);
  const [regenerating, setRegenerating] = useState(false);
  const [groups, setGroups] = useState([]);
  const [selectedGroup, setSelectedGroup] = useState('');
  const [apiKey, setApiKey] = useState('');
  const [showKey, setShowKey] = useState(false);
  const [loading, setLoading] = useState(true);
  // 已有数据时的刷新不进入加载态：保留当前配置展示，只在按钮上给反馈
  const [refreshing, setRefreshing] = useState(false);
  // 刷新成功后递增，连接配置按这个 key 逐字重放一次，给出「刚重新读取」的反馈
  const [revealKey, setRevealKey] = useState(0);
  const [loadError, setLoadError] = useState(false);
  // 客户端配置面板上报的未保存状态；树内切换前用它决定是否确认
  const [configDirty, setConfigDirty] = useState(false);
  const [pendingEntry, setPendingEntry] = useState(null);
  const scrollRef = useRef(null);
  const workspaceRef = useRef(null);
  const lastSelection = useRef(`${selection.section}/${selection.tool}`);
  // 桌面端取实际 sidecar 地址；浏览器开发环境直连开发后端，不把 Vite 端口交给客户端。
  const serverUrl = window.api?.getServerUrl?.() || 'http://127.0.0.1:3060';

  const load = async (isRefresh = false) => {
    // 首次加载或没有可展示的数据时才遮蔽内容；刷新失败保留旧配置
    const blocking = !isRefresh || (!groups.length && !apiKey);
    if (blocking) setLoading(true); else setRefreshing(true);
    const [groupResult, keyResult] = await Promise.allSettled([listGroups(), getAPIKey()]);
    const failed = groupResult.status === 'rejected' || keyResult.status === 'rejected';
    // 刷新失败保留旧配置，只有首次加载失败才进入错误态
    if (!failed || blocking) setLoadError(failed);
    if (groupResult.status === 'fulfilled') {
      const next = groupResult.value || [];
      setGroups(next);
      setSelectedGroup((old) => selectServiceGroup(next, requestedGroup, old));
    } else if (blocking) {
      setGroups([]);
      setSelectedGroup('');
    }
    if (keyResult.status === 'fulfilled') setApiKey(keyResult.value?.key || '');
    else if (blocking) setApiKey('');
    setLoading(false);
    setRefreshing(false);
    // 刷新失败保留旧值，不动画；成功时重放一次逐字显示
    if (isRefresh && !failed) setRevealKey((key) => key + 1);
  };

  useEffect(() => { load(); }, []);
  useEffect(() => {
    setSelectedGroup((old) => selectServiceGroup(groups, requestedGroup, old));
  }, [groups, requestedGroup]);
  useEffect(() => {
    window.api?.getNetworkMode?.().then((mode) => setNetworkMode(mode)).catch(() => toast.danger('访问范围加载失败'));
  }, []);

  // URL 规范化：非法 section/tool 回退，默认值不写进地址栏
  useEffect(() => {
    const normalized = normalizeSearch(searchParams);
    if (normalized.toString() !== searchParams.toString()) {
      setSearchParams(normalized, { replace: true });
    }
  }, [searchParams, setSearchParams]);

  // 切换树节点时把工作区平滑滚回视口；只在已经滚过工作区顶部时调整，避免首屏被拉走
  useEffect(() => {
    const key = `${selection.section}/${selection.tool}`;
    if (lastSelection.current === key) return;
    lastSelection.current = key;
    const scroller = scrollRef.current;
    const workspace = workspaceRef.current;
    if (!scroller || !workspace) return;
    const offset = workspace.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
    if (scroller.scrollTop <= offset) return;
    // offset 是「工作区顶部距滚动容器顶部」的剩余距离，绝对目标是当前 scrollTop + offset
    const target = Math.max(0, scroller.scrollTop + offset - WORKSPACE_TOP_GAP);
    const reduceMotion = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches;
    scroller.scrollTo({ top: target, behavior: reduceMotion ? 'auto' : 'smooth' });
  }, [selection.section, selection.tool]);

  // 工作区卡至少铺满一屏：右栏换内容时页面高度不会突然塌缩，
  // 浏览器也就不会把滚动位置夹回页面顶部；内容变短时最多停在「工作区顶部对齐视口」的位置。
  useLayoutEffect(() => {
    const scroller = scrollRef.current;
    if (!scroller) return;
    const sync = () => {
      const minHeight = Math.max(0, scroller.clientHeight - WORKSPACE_TOP_GAP);
      scroller.style.setProperty('--service-workspace-min', `${minHeight}px`);
    };
    sync();
    const observer = new ResizeObserver(sync);
    observer.observe(scroller);
    return () => observer.disconnect();
  }, []);

  const saveNetworkMode = async (mode) => {
    if (mode === networkMode || !window.api?.setNetworkMode) return;
    setSavingMode(true);
    try {
      const result = await window.api.setNetworkMode(mode);
      setNetworkMode(result?.networkMode ?? mode);
      toast.success(result?.restarted ? '访问范围已保存，服务已自动重启' : '访问范围已保存');
    } catch (error) { toast.danger(`保存失败：${error?.message || ''}`); }
    finally { setSavingMode(false); }
  };

  const generateKey = async () => {
    setRegenerating(true);
    try {
      const key = await regenerateAPIKey();
      setApiKey(key?.key || '');
      toast.success('令牌已生成，请更新所有调用方的配置');
    } catch { /* 请求拦截器显示错误 */ }
    finally { setRegenerating(false); }
  };

  const chooseGroup = (name) => {
    const next = new URLSearchParams(searchParams);
    next.set('group', name);
    setSearchParams(next, { replace: true });
  };

  const navigateSelection = (next) => {
    if (next.section === selection.section && next.tool === selection.tool) return;
    // 从客户端配置切走时，先确认丢弃未保存改动；面板卸载后会自动注销 navGuard
    if (configDirty && selection.section === 'config' && next.section !== 'config') {
      setPendingEntry(next);
      return;
    }
    setSearchParams(selectionSearch(searchParams, next));
  };

  const confirmLeaveConfig = () => {
    const next = pendingEntry;
    setPendingEntry(null);
    if (next) setSearchParams(selectionSearch(searchParams, next));
  };

  const guide = buildConnectionGuide({ serverUrl, apiKey, model: selectedGroup });
  const preview = buildConnectionGuide({ serverUrl, apiKey: showKey ? apiKey : '', model: selectedGroup });
  const ready = !loading && !loadError && !!apiKey && !!selectedGroup;
  const selected = groups.find((g) => g.name === selectedGroup);
  const selectedDescription = selected ? `${STRATEGY_LABELS[selected.strategy] || selected.strategy} · ${selected.items?.length || 0} 个成员` : '';
  const missingRequestedGroup = requestedGroup !== null && !groups.some((g) => g.name === requestedGroup) ? requestedGroup : null;
  const logsPath = selectedGroup ? `/logs?group=${encodeURIComponent(selectedGroup)}` : '/logs';
  const onDirtyChange = useCallback((dirty) => setConfigDirty(dirty), []);

  return (
    <div className="service-page flex flex-col flex-1 min-h-0 min-w-0">
      <h1 className="sr-only">服务接入</h1>
      <div className="service-toolbar">
        <p className="service-toolbar-hint">选择模型分组，为你的应用提供一个稳定入口。</p>
        <Button size="md" variant="secondary" onPress={() => load(true)} isPending={loading || refreshing} aria-label="刷新连接信息"><RefreshCw className="size-4" />刷新连接</Button>
      </div>

      <div ref={scrollRef} className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
        <div className="service-access-grid">
          <ConnectionCard
            guide={guide}
            apiKey={apiKey}
            showKey={showKey}
            onToggleKey={() => setShowKey((v) => !v)}
            loading={loading}
            refreshing={refreshing}
            revealKey={revealKey}
            loadError={loadError}
            regenerating={regenerating}
            onResetToken={generateKey}
            networkMode={networkMode}
            savingMode={savingMode}
            onSaveNetworkMode={saveNetworkMode}
          />

          <section ref={workspaceRef} className="service-workspace rounded-2xl bg-surface/70" aria-label="接入内容">
            <TreeNav searchParams={searchParams} selection={selection} onSelect={navigateSelection} />
            <div className="service-pane">
              {selection.section === 'examples' ? (
                <ExamplesPane
                  groups={groups}
                  selectedGroup={selectedGroup}
                  onSelectGroup={chooseGroup}
                  selected={selected}
                  selectedDescription={selectedDescription}
                  missingRequestedGroup={missingRequestedGroup}
                  loading={loading}
                  tool={selection.tool}
                  guide={guide}
                  preview={preview}
                  ready={ready}
                  apiKey={apiKey}
                  serverUrl={serverUrl}
                  showKey={showKey}
                  logsPath={logsPath}
                />
              ) : (
                <ClaudeConfigPanel groups={groups} apiKey={apiKey} serverUrl={serverUrl} onDirtyChange={onDirtyChange} />
              )}
            </div>
          </section>
        </div>
      </div>

      <ConfirmModal
        isOpen={pendingEntry !== null}
        onOpenChange={(open) => { if (!open) setPendingEntry(null); }}
        title="切换前丢弃未保存的客户端配置？"
        description="Claude Code 配置有未保存改动，切换后会丢失这些修改。"
        confirmText="丢弃并切换"
        onConfirm={confirmLeaveConfig}
      />
    </div>
  );
}
