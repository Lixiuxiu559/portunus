import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import { useSearchParams } from 'react-router-dom';
import { Button, toast } from '@heroui/react';
import { RefreshCw } from 'lucide-react';
import { listGroups } from '../../api/group';
import { listAPIKeys, createAPIKey, renameAPIKey, regenerateNamedAPIKey, setAPIKeyStatus, deleteAPIKey } from '../../api/apikey';
import { selectAPIKey } from '../../utils/apiKeys';
import { strategyLabels } from '../../utils/groupService';
import { buildConnectionGuide } from '../../utils/connectionGuide';
import { selectServiceGroup } from '../../utils/serviceAccess';
import ConfirmModal from '../../components/ConfirmModal';
import ConnectionCard from './ConnectionCard';
import APIKeyControls from './APIKeyControls';
import PageSkeleton from '../../components/PageSkeleton';
import TreeNav from './TreeNav';
import ExamplesPane from './ExamplesPane';
import ClaudeConfigPanel from './ClaudeConfigPanel';
import { normalizeSearch, parseSelection, selectionSearch } from './sectionState';
import { getCachedResponse } from '../../api/responseCache';
import useDelayedFlag from '../../hooks/useDelayedFlag';
import '../service-access.css';

// 工作区顶部对齐视口顶部时留一点余量，和滚动目标共用同一个值
const WORKSPACE_TOP_GAP = 8;

export default function Clients() {
  const [searchParams, setSearchParams] = useSearchParams();
  const selection = useMemo(() => parseSelection(searchParams), [searchParams]);
  const requestedGroup = searchParams.get('group');
  const managementView = searchParams.get('view') === 'keys' || (!searchParams.has('view') && !['section', 'tool', 'group'].some((key) => searchParams.has(key)));

  const [networkMode, setNetworkMode] = useState(null);
  const [savingMode, setSavingMode] = useState(false);
  const [regenerating, setRegenerating] = useState(false);
  const [cached] = useState(() => {
    const groups = getCachedResponse('/groups');
    const keys = getCachedResponse('/apikeys');
    return groups !== undefined && keys !== undefined ? { groups, keys } : null;
  });
  const [groups, setGroups] = useState(cached?.groups ?? []);
  const [selectedGroup, setSelectedGroup] = useState(() => selectServiceGroup(cached?.groups ?? [], requestedGroup, ''));
  const [apiKeys, setApiKeys] = useState(cached?.keys ?? []);
  const [selectedKeyID, setSelectedKeyID] = useState(() => selectAPIKey(cached?.keys ?? [])?.id ?? '');
  const selectedKey = selectAPIKey(apiKeys, selectedKeyID);
  const apiKey = selectedKey?.key ?? '';
  const [showKey, setShowKey] = useState(false);
  const [loading, setLoading] = useState(!cached);
  // 已有数据时的刷新不进入加载态：保留当前配置展示，只在按钮上给反馈
  const [refreshing, setRefreshing] = useState(false);
  const showRefreshing = useDelayedFlag(refreshing);
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
    const blocking = !cached && (!isRefresh || (!groups.length && !apiKey));
    if (blocking) setLoading(true); else setRefreshing(true);
    const [groupResult, keyResult] = await Promise.allSettled([listGroups(), listAPIKeys()]);
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
    if (keyResult.status === 'fulfilled') {
      const keys = Array.isArray(keyResult.value) ? keyResult.value : [];
      setApiKeys(keys);
      setShowKey(false);
      setSelectedKeyID((old) => selectAPIKey(keys, old)?.id ?? '');
    } else if (blocking) setApiKeys([]);
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
    if (!managementView && !normalized.has('view')) normalized.set('view', 'guides');
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

  const updateKey = async (operation, message) => {
    setRegenerating(true);
    try {
      const key = await operation();
      setApiKeys((keys) => keys.some((item) => item.id === key.id)
        ? keys.map((item) => item.id === key.id ? key : item)
        : [...keys, key]);
      setSelectedKeyID(key.id);
      setShowKey(false);
      toast.success(message);
    } finally { setRegenerating(false); }
  };

  const removeKey = async (id) => {
    setRegenerating(true);
    try {
      await deleteAPIKey(id);
      setApiKeys((keys) => keys.filter((item) => item.id !== id));
      setShowKey(false);
      toast.success('API Key 已删除，历史调用记录和用量保留');
    } finally { setRegenerating(false); }
  };

  const chooseKey = (id) => { setSelectedKeyID(id); setShowKey(false); };

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

  const changeView = (view) => {
    if (configDirty && view === 'keys') { setPendingEntry({ view }); return; }
    const next = new URLSearchParams(searchParams);
    next.set('view', view);
    setSearchParams(next, { replace: true });
  };

  const confirmLeaveConfig = () => {
    const next = pendingEntry;
    setPendingEntry(null);
    if (next?.view) {
      const params = new URLSearchParams(searchParams);
      params.set('view', next.view);
      setSearchParams(params, { replace: true });
    } else if (next) setSearchParams(selectionSearch(searchParams, next));
  };

  const guide = buildConnectionGuide({ serverUrl, apiKey, model: selectedGroup });
  const preview = buildConnectionGuide({ serverUrl, apiKey: showKey ? apiKey : '', model: selectedGroup });
  const ready = !loading && !loadError && !!apiKey && !!selectedGroup;
  const selected = groups.find((g) => g.name === selectedGroup);
  const selectedDescription = selected ? `${strategyLabels[selected.strategy] || selected.strategy} · ${selected.items?.length || 0} 个成员` : '';
  const missingRequestedGroup = requestedGroup !== null && !groups.some((g) => g.name === requestedGroup) ? requestedGroup : null;
  const logSearch = new URLSearchParams();
  if (selectedGroup) logSearch.set('group', selectedGroup);
  if (selectedKey) logSearch.set('api_key_id', selectedKey.id);
  const logsPath = `/logs${logSearch.size ? `?${logSearch}` : ''}`;
  const onDirtyChange = useCallback((dirty) => setConfigDirty(dirty), []);

  return (
    <div data-page-ready={!loading} className="service-page flex flex-col flex-1 min-h-0 min-w-0">
      <h1 className="sr-only">服务接入</h1>
      <div className="service-toolbar">
        <div className="service-view-tabs" role="group" aria-label="接入管理视图">
          <button type="button" aria-pressed={managementView} onClick={() => changeView('keys')}>API Key</button>
          <button type="button" aria-pressed={!managementView} onClick={() => changeView('guides')}>接入指南</button>
        </div>
        <Button size="md" variant="secondary" onPress={() => load(true)} isDisabled={loading || refreshing || regenerating} isPending={loading || showRefreshing} aria-label="刷新连接信息"><RefreshCw className="size-4" />{managementView ? '刷新列表' : '刷新连接'}</Button>
      </div>

      <div ref={scrollRef} className="flex-1 min-h-0 overflow-y-auto no-scrollbar">
        <div className="service-access-grid">
          {managementView ? (
            loading ? <PageSkeleton variant="content" /> : <>
              {loadError && <p role="alert" className="text-sm text-danger">连接信息加载失败，请刷新重试。</p>}
              <APIKeyControls apiKeys={apiKeys} busy={regenerating || refreshing || loadError}
                onCreate={(name) => updateKey(() => createAPIKey(name), 'API Key 已创建')}
                onRename={(id, name) => updateKey(() => renameAPIKey(id, name), '名称已保存')}
                onReset={(id) => updateKey(() => regenerateNamedAPIKey(id), '密钥已重置，请更新对应工具配置')}
                onStatus={(id, revoked) => updateKey(() => setAPIKeyStatus(id, revoked), revoked ? 'API Key 已禁用，可随时启用' : 'API Key 已启用')}
                onDelete={removeKey}
                onConnect={(id) => { chooseKey(id); changeView('guides'); }} />
            </>
          ) : <>
          <ConnectionCard
            guide={guide}
            apiKey={apiKey}
            showKey={showKey}
            onToggleKey={() => setShowKey((v) => !v)}
            loading={loading}
            refreshing={showRefreshing}
            revealKey={revealKey}
            loadError={loadError}
            apiKeys={apiKeys}
            selectedKey={selectedKey}
            onSelectKey={chooseKey}
            keyBusy={regenerating || refreshing}
            networkMode={networkMode}
            savingMode={savingMode}
            onSaveNetworkMode={saveNetworkMode}
          />

          <section ref={workspaceRef} className="service-workspace rounded-2xl bg-surface/70" aria-label="接入内容">
            <TreeNav searchParams={searchParams} selection={selection} onSelect={navigateSelection} />
            <div className="service-pane">
              {loading ? <PageSkeleton variant="content" /> : selection.section === 'examples' ? (
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
          </>}
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
