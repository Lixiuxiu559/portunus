/* 仅用于离线原型走查。所有对象、文件、密码和结果均为模拟；无网络与文件读写。 */
(() => {
  'use strict';
  const $ = (id) => document.getElementById(id);
  const escape = (value) => String(value).replace(/[&<>"']/g, (c) => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const icon = (name, extra = '') => `<svg class="icon ${extra}" aria-hidden="true"><use href="#i-${name}"/></svg>`;
  const fileName = 'portunus-config-2026-11-09.portunus';
  const longFileName = 'portunus-config-2026-11-09-来自我的工作电脑-全部渠道与模型分组-用于迁移到另一台电脑的完整配置副本.portunus';
  const source = {
    channels: [
      { key:'openai', name:'OpenAI', models:['gpt-4.1','gpt-4.1-mini','o3'] },
      { key:'anthropic', name:'Anthropic', models:['claude-sonnet-4-5','claude-haiku-4-5'] },
      { key:'team', name:'团队网关 · 华东备用节点', models:['deepseek-chat','deepseek-reasoner','gemini-2.5-pro'] },
    ],
    groups: [
      { name:'coding', strategy:'manual', members:[{channel:'anthropic',model:'claude-sonnet-4-5',active:true},{channel:'openai',model:'gpt-4.1'}] },
      { name:'daily', strategy:'failover', members:[{channel:'openai',model:'gpt-4.1-mini'},{channel:'team',model:'deepseek-chat'},{channel:'anthropic',model:'claude-haiku-4-5'}] },
      { name:'reasoning', strategy:'manual', members:[{channel:'team',model:'deepseek-reasoner',active:true},{channel:'openai',model:'o3'}] },
    ],
  };
  const state = {
    scene:'settings', file:false, password:'', exportPassword:'', exportConfirm:'',
    errors:{}, detailTab:'channels', plan:null, lastResult:null, long:false,
    targetChannels:new Set(['OpenAI','Anthropic']), targetGroups:new Set(['coding','daily']),
    modelCount:4, exportCounts:null, occupiedName:'', sourceEmpty:false, timer:null, toastTimer:null, opener:'open-import',
  };
  const busyScenes = new Set(['exporting','decrypting','importing']);
  const importScenes = new Set(['import','decrypting','wrong-password']);
  const exportScenes = new Set(['export','exporting']);
  const previewScenes = new Set(['preview','no-conflict','changed','long','importing']);
  const errorScenes = new Set(['corrupt','version','invalid','rollback','export-error']);
  const currentFile = () => state.long ? longFileName : fileName;
  const localCounts = () => ({channels:state.targetChannels.size,models:state.modelCount,groups:state.targetGroups.size});
  const packCounts = () => ({channels:3,models:8,groups:3});

  function counts(c) {
    return `<div class="count-strip" aria-label="配置数量">${[['channels','channel','渠道'],['models','model','模型'],['groups','group','分组']].map(([key,i,label])=>`<div class="count-cell">${icon(i)}<span><span class="count-number">${c[key]}</span><span class="count-label">${label}</span></span></div>`).join('')}</div>`;
  }
  function notice(type, title, text) {
    return `<div class="notice ${type}">${icon(type==='danger'||type==='warning'?'alert':type==='success'?'check-circle':'info')}<div>${title?`<strong>${escape(title)}</strong>`:''}<p>${escape(text)}</p></div></div>`;
  }
  function steps(current) {
    return `<ol class="steps" aria-label="导入步骤">${['选择文件','预览配置','完成'].map((label,i)=>`<li class="${i===current?'current':i<current?'complete':''}" ${i===current?'aria-current="step"':''}><span class="step-no">${i<current?'✓':i+1}</span>${label}</li>`).join('')}</ol>`;
  }
  function selectedFile(change = false, meta = true) {
    return `<div class="selected-file"><div class="file-symbol">${icon('file')}</div><div class="file-copy"><p class="file-name">${escape(currentFile())}</p><div class="file-meta">${icon('lock')}${meta?'密码保护的配置文件':'已选择文件'}</div></div>${change?'<button type="button" class="text-button file-change" data-action="change-file">更换文件</button>':''}</div>`;
  }
  function passwordField(id,label,value,error,placeholder) {
    return `<div class="field"><label for="${id}">${label}</label><div class="password-wrap"><input id="${id}" class="input" type="password" autocomplete="off" spellcheck="false" value="${escape(value)}" placeholder="${placeholder}" ${error?'aria-invalid="true"':''} aria-describedby="${id}-error"><button type="button" class="icon-button" data-show-password="${id}" aria-label="显示${label}">${icon('eye')}</button></div><p class="field-error" id="${id}-error" ${error?'':'hidden'}>${escape(error||'')}</p></div>`;
  }
  function makeUnique(name, occupied) {
    let candidate = name;
    let n = 1;
    while (occupied.has(candidate)) { candidate = name + (n===1?'（导入）':`（导入 ${n}）`); n++; }
    occupied.add(candidate);
    return candidate;
  }
  function makePlan() {
    const channels = new Set(state.targetChannels), groups = new Set(state.targetGroups);
    return {
      channels:source.channels.map(c=>({...c,originalName:c.name,name:makeUnique(c.name,channels)})),
      groups:source.groups.map(g=>({...g,originalName:g.name,name:makeUnique(g.name,groups)})),
    };
  }
  function renamedItems(plan) {
    return [...plan.channels.map(c=>({...c,kind:'渠道'})),...plan.groups.map(g=>({...g,kind:'分组'}))].filter(x=>x.name!==x.originalName);
  }
  function renameTable(plan, completed = false) {
    const rows = renamedItems(plan);
    if (!rows.length) return notice('success','无需改名',completed?'已按文件中的原名称新增全部配置。':'文件中的渠道和分组名称均可直接使用，仍会作为新增配置导入。');
    return `<div class="rename-title">${icon('alert')}${rows.length} 项同名配置${completed?'已':'将'}自动改名</div><div class="table-wrap"><table class="rename-table"><thead><tr><th>类型</th><th>原名称</th><th aria-label="改为"></th><th>导入后名称</th></tr></thead><tbody>${rows.map(x=>`<tr><td>${x.kind}</td><td>${escape(x.originalName)}</td><td class="arrow-cell">${icon('arrow')}</td><td class="renamed-name">${escape(x.name)}</td></tr>`).join('')}</tbody></table></div>`;
  }
  function channelName(key, plan) { return plan.channels.find(c=>c.key===key)?.name || key; }
  function memberRows(group,plan) {
    return group.members.map((m,i)=>`<div class="member"><span class="order">${i+1}.</span><span class="mono">${escape(m.model)}</span>${m.active?'<span class="badge accent">当前</span>':''}<span class="channel-name">${escape(channelName(m.channel,plan))}</span></div>`).join('');
  }
  function detailContent(plan) {
    if (state.detailTab==='channels') return `<div class="detail-list">${plan.channels.map(c=>`<div class="detail-row"><div class="detail-row-copy"><div class="detail-name">${escape(c.name)}</div><div class="detail-sub">${c.name!==c.originalName?`原名称：${escape(c.originalName)} · `:''}连接配置与上游凭据一并恢复</div></div><span class="detail-side">${c.models.length} 个模型</span></div>`).join('')}</div>`;
    if (state.detailTab==='models') return `<div class="table-wrap"><table class="model-table"><thead><tr><th>模型名称</th><th>所属导入渠道</th></tr></thead><tbody>${plan.channels.flatMap(c=>c.models.map(m=>`<tr><td>${escape(m)}</td><td>${escape(c.name)}</td></tr>`)).join('')}</tbody></table></div>`;
    return `<div class="detail-list">${plan.groups.map(g=>`<div class="group-detail"><div class="group-detail-heading">${escape(g.name)}<span class="badge">${g.strategy==='manual'?'手动指定':'故障转移'}</span></div><div class="member-list">${memberRows(g,plan)}</div></div>`).join('')}</div>`;
  }
  function header(title, subtitle) {
    $('dialog-header').innerHTML = `<h2 id="dialog-title">${escape(title)}</h2><p class="modal-subtitle" id="dialog-subtitle">${escape(subtitle)}</p>`;
  }
  function footer(html) { $('dialog-footer').innerHTML = html; }
  function button(text,action,primary=false,extra='') { return `<button type="button" class="button ${primary?'primary':'secondary'}" data-action="${action}" ${extra}>${text}</button>`; }
  function processNote(text) { return `<div class="process-note" role="status"><span class="spinner" aria-hidden="true"></span>${text}</div>`; }

  function renderExport() {
    header('导出配置','导出本机全部渠道、模型和分组，迁移到另一台电脑。');
    $('dialog-body').innerHTML = (state.scene==='exporting'?processNote('正在加密并导出配置…'):'') + counts(state.exportCounts||localCounts()) + `<p class="scope-line">${icon('lock')}<span>包含上游访问凭据、模型归属与分组路由设置。文件使用你设置的密码保护。</span></p><form id="export-form" class="form-stack" novalidate>${passwordField('export-password','文件密码',state.exportPassword,state.errors.password,'设置文件密码')}${passwordField('export-confirm','确认密码',state.exportConfirm,state.errors.confirm,'再次输入文件密码')}<p class="helper">请妥善保存密码，导入时需要使用同一密码。</p></form><div class="mt-16">${notice('','','服务接入 Key、监听端口、客户端设置和调用记录不包含在文件中。')}</div>`;
    footer(button('取消','close') + `<button type="submit" form="export-form" class="button primary">${state.scene==='exporting'?'<span class="spinner"></span>正在导出…':'选择位置并导出'}</button>`);
    $('export-form').addEventListener('submit',submitExport);
    $('export-password').addEventListener('input',e=>state.exportPassword=e.target.value);
    $('export-confirm').addEventListener('input',e=>state.exportConfirm=e.target.value);
  }
  function renderImport() {
    header('导入配置','选择导出文件并输入密码，先预览，再确认导入。');
    const wrong = state.scene==='wrong-password';
    const file = state.file ? selectedFile(true,false) : `<button type="button" class="file-drop" data-action="select-file">${icon('upload')}<strong>点击选择配置文件</strong><span class="helper">选择从 Portunus 导出的密码保护文件</span></button>`;
    $('dialog-body').innerHTML = steps(0) + (state.scene==='decrypting'?processNote('正在解密并检查配置…'):'') + `<form id="import-form" class="form-stack" novalidate>${file}${passwordField('import-password','文件密码',state.password,state.errors.password||(wrong?'无法解密，请检查密码或文件。':''),'输入导出时设置的密码')}<p class="helper">现有配置会保留；同名渠道和分组将自动改名。</p></form>`;
    footer(button('取消','close') + `<button type="submit" form="import-form" class="button primary" ${!state.file?'disabled':''}>${state.scene==='decrypting'?'<span class="spinner"></span>正在校验…':'解密并预览'}</button>`);
    $('import-form').addEventListener('submit',submitImport);
    $('import-password').addEventListener('input',e=>state.password=e.target.value);
  }
  function renderPreview() {
    const plan = state.plan || (state.plan=makePlan());
    header('预览导入配置','以下内容将作为新增配置导入，不覆盖本机现有内容。');
    let prefix = '';
    if (state.scene==='changed') prefix = notice('warning','名称已发生变化，请重新核对',`${state.occupiedName||'OpenAI（导入）'} 已被其他配置占用。下方已更新为新的可用名称，需要再次确认。`) + '<div class="mt-16"></div>';
    if (state.scene==='importing') prefix = processNote('正在导入全部配置，请稍候…');
    $('dialog-body').innerHTML = steps(1) + prefix + selectedFile() + `<div class="preview-meta"><span>导出应用 <span class="mono">v0.5.3</span> · 当前 <span class="mono">v0.5.4</span></span><span>文件格式 <span class="mono">1</span><span class="badge success">${icon('check','small')}受支持</span></span></div>` + counts(packCounts()) + renameTable(plan) + `<div class="detail-tabs" role="tablist" aria-label="导入配置详情">${[['channels','渠道',3],['models','模型',8],['groups','分组',3]].map(([key,label,n])=>`<button class="detail-tab" type="button" id="tab-${key}" role="tab" aria-selected="${state.detailTab===key}" aria-controls="detail-content" tabindex="${state.detailTab===key?'0':'-1'}" data-tab="${key}">${label} <span class="muted">${n}</span></button>`).join('')}</div><div id="detail-content" role="tabpanel" aria-labelledby="tab-${state.detailTab}">${detailContent(plan)}</div>`;
    footer(`<span class="footer-note">保留模型关联、当前激活项和备用顺序</span>` + button('返回','back-import') + button(state.scene==='importing'?'<span class="spinner"></span>正在导入…':state.scene==='changed'?'确认更新并导入':'确认导入','confirm-import',true));
  }
  function renderExported() {
    header('导出完成','可将文件带到另一台电脑，在 Portunus 中导入。');
    $('dialog-body').innerHTML = `<div class="result-heading"><span class="result-symbol">${icon('check-circle')}</span><div><h3>配置已导出</h3><p>全部配置已保存到所选位置。</p></div></div>` + selectedFile() + `<h3 class="section-title">文件包含</h3>` + counts(state.exportCounts||localCounts()) + notice('','','文件包含上游访问凭据，请妥善保管，并保留导入密码。');
    footer(button('完成','close',true));
  }
  function commitMockPlan() {
    const plan = state.plan || makePlan();
    plan.channels.forEach(c=>state.targetChannels.add(c.name));
    plan.groups.forEach(g=>state.targetGroups.add(g.name));
    state.modelCount += 8;
    state.lastResult = JSON.parse(JSON.stringify(plan));
    updateLocalCounts();
  }
  function renderSuccess() {
    if (!state.lastResult) commitMockPlan();
    header('导入完成','全部配置已恢复，本机现有配置保持不变。');
    $('dialog-body').innerHTML = steps(2) + `<div class="result-heading"><span class="result-symbol">${icon('check-circle')}</span><div><h3>配置已成功导入</h3><p>已新增 3 个渠道、8 个模型和 3 个分组。</p></div></div>` + counts(packCounts()) + `<h3 class="section-title">本次导入的名称调整</h3>` + renameTable(state.lastResult,true) + `<div class="mt-16">${notice('','','客户端请使用本机的地址和服务接入 Key；调用分组时使用上方显示的最终名称。')}</div><p class="helper mt-16">本次未自动测试上游连接。如需检查，可在渠道页使用现有验证功能。</p>`;
    footer(button('完成','close') + button('查看导入分组','view-groups',true));
  }
  function renderError() {
    const scene=state.scene;
    let title,sub,heading,message,detail,primary,action;
    if (scene==='version') {
      title='无法导入此文件';sub='文件格式不受当前 Portunus 支持。';heading='文件格式版本不一致';message='此文件的格式为 2，本应用当前仅支持格式 1。';detail='应用版本不同不一定影响导入，但文件格式必须一致。请使用支持格式 2 的 Portunus，或重新选择格式 1 的文件。';primary='更换文件';action='new-file';
    } else if (scene==='corrupt') {
      title='无法读取配置文件';sub='本次未导入任何配置。';heading='文件可能已损坏或不完整';message='无法读取完整配置，请重新选择文件或在来源电脑重新导出。';detail='本机原有的渠道、模型和分组没有变化。';primary='更换文件';action='new-file';
    } else if (scene==='invalid') {
      title='配置校验未通过';sub='发现无效配置，已取消整次导入。';heading='分组成员关联缺失';message='分组“daily”的成员“missing-model”找不到对应模型。';detail='请在来源电脑检查该分组及所属模型，再重新导出。其他配置也未导入，本机原有内容保持不变。';primary='更换文件';action='new-file';
    } else if (scene==='export-error') {
      title='导出未完成';sub='文件未成功保存，请重新选择保存位置。';heading='无法保存配置文件';message='所选位置无法保存文件，请检查位置是否可写后重试。';detail='本机配置没有变化；本次没有生成可用于迁移的完整文件。';primary='重新导出';action='retry-export';
    } else {
      title='导入未完成';sub='已取消本次全部导入，本机原有配置不受影响。';heading='写入配置时发生错误';message='配置写入未完成，本次新增内容已全部回滚。';detail='没有留下部分渠道、模型或分组。你可以返回预览，检查后重试。';primary='返回预览';action='retry-preview';
    }
    header(title,sub);
    $('dialog-body').innerHTML = (scene==='export-error'?'':steps(1)) + `<div class="result-heading"><span class="result-symbol ${scene==='version'?'warning':'danger'}">${icon('alert')}</span><div><h3>${heading}</h3><p>${message}</p></div></div>` + (scene==='rollback'?counts({channels:0,models:0,groups:0}):selectedFile()) + `<div class="error-detail"><p>${detail}</p></div>`;
    footer(button('关闭','close')+button(primary,action,true));
  }

  function updateLocalCounts() {
    const c=localCounts();
    $('local-counts').textContent=`本机现有：${c.channels} 个渠道 · ${c.models} 个模型 · ${c.groups} 个分组`;
    $('open-export').disabled=state.sourceEmpty;
    $('open-export').title=state.sourceEmpty?'空库导出规则尚待确认（仅演示占位）':'';
  }
  function updateNavigation(page) {
    document.querySelectorAll('[data-nav]').forEach(b=>{const selected=b.dataset.nav===page;b.classList.toggle('selected',selected);if(selected)b.setAttribute('aria-current','page');else b.removeAttribute('aria-current');});
  }
  function showSettings() {
    $('settings-page').hidden=false;$('groups-page').hidden=true;updateNavigation('settings');
  }
  function showGroups() {
    closeModal();
    const plan=state.lastResult||state.plan||makePlan();
    $('settings-page').hidden=true;$('groups-page').hidden=false;updateNavigation('groups');
    $('groups-page').innerHTML=`<div class="groups-toolbar"><span class="readonly-note">本次导入的 ${plan.groups.length} 个分组</span><button class="button secondary" data-action="back-settings">${icon('back')}返回配置迁移</button></div><div class="group-grid">${plan.groups.map(g=>`<section class="card group-card"><div class="group-card-heading"><h2>${escape(g.name)}</h2><span class="badge">${g.strategy==='manual'?'手动指定':'故障转移'}</span></div><div class="group-member-stack">${g.members.map((m,i)=>`<div class="group-member-row"><span class="muted">${i+1}.</span><div><div class="mono">${escape(m.model)} ${m.active?'<span class="badge accent">当前</span>':''}</div><div class="channel-name">${escape(channelName(m.channel,plan))}</div></div></div>`).join('')}</div></section>`).join('')}</div>`;
    $('scene-select').value='success';
  }
  function closeModal() {
    if(busyScenes.has(state.scene))return;
    $('modal-layer').hidden=true;$('app').inert=false;state.scene='settings';$('scene-select').value='settings';$('advance-step').hidden=true;
    $('demo-message').textContent='示例密码：demo1234';
    $(state.opener).focus({preventScroll:true});
  }
  function showScene(scene,{preset=false,focus=true}={}) {
    clearTimeout(state.timer);
    clearTimeout(state.toastTimer);$('toast').hidden=true;
    if (state.sourceEmpty && scene!=='empty') resetData();
    if (preset) {
      state.errors={};
      if(scene==='export'||scene==='exporting'||scene==='exported'||scene==='export-error') {state.exportCounts=localCounts();state.exportPassword='';state.exportConfirm='';}
      if(scene==='preview'||scene==='long'||scene==='importing'||scene==='changed') {state.file=true;state.plan=makePlan();}
      if(scene==='long')state.long=true;else state.long=false;
      if(scene==='no-conflict') {state.targetChannels=new Set(['本机渠道 A','本机渠道 B']);state.targetGroups=new Set(['本机分组 A','本机分组 B']);state.modelCount=4;state.file=true;state.plan=makePlan();updateLocalCounts();}
      if(scene==='changed') {
        const first=state.plan.channels[0].name;state.occupiedName=first;state.targetChannels.add(first);state.plan=makePlan();updateLocalCounts();
      }
      if(importScenes.has(scene)){state.file=scene!=='import';state.password=scene==='wrong-password'?'incorrect':'';}
      if(scene==='empty'){resetData();state.sourceEmpty=true;state.targetChannels.clear();state.targetGroups.clear();state.modelCount=0;updateLocalCounts();}
    }
    state.scene=scene;
    showSettings();updateLocalCounts();
    $('scene-select').value=scene==='groups'?'success':scene;
    $('advance-step').hidden=!busyScenes.has(scene);
    $('demo-message').textContent=scene==='empty'?'空库导出规则待确认':busyScenes.has(scene)?'可用“完成此步骤”推进':'示例密码：demo1234';
    if(scene==='settings'||scene==='empty') {$('modal-layer').hidden=true;$('app').inert=false;return;}
    $('modal-layer').hidden=false;$('app').inert=true;
    $('dialog').classList.toggle('wide',previewScenes.has(scene)||scene==='success'||scene==='rollback');
    if(exportScenes.has(scene))renderExport();
    else if(importScenes.has(scene))renderImport();
    else if(previewScenes.has(scene))renderPreview();
    else if(scene==='success')renderSuccess();
    else if(scene==='exported')renderExported();
    else if(errorScenes.has(scene))renderError();
    $('close-modal').disabled=busyScenes.has(scene);
    if(busyScenes.has(scene))$('dialog').querySelectorAll('button,input').forEach(e=>e.disabled=true);
    if(focus)requestAnimationFrame(()=>{$('dialog').focus({preventScroll:true});});
    $('dialog-body').scrollTop=0;
  }
  function submitExport(event) {
    event.preventDefault();if(busyScenes.has(state.scene))return;
    state.exportPassword=$('export-password').value;state.exportConfirm=$('export-confirm').value;
    state.errors={};
    if(!state.exportPassword)state.errors.password='请设置文件密码。';
    if(!state.exportConfirm)state.errors.confirm='请再次输入密码。';else if(state.exportPassword!==state.exportConfirm)state.errors.confirm='两次输入的密码不一致。';
    if(Object.keys(state.errors).length){showScene('export');$(state.errors.password?'export-password':'export-confirm').focus();return;}
    showScene('exporting');state.timer=setTimeout(()=>showScene('exported'),1000);
  }
  function submitImport(event) {
    event.preventDefault();if(busyScenes.has(state.scene)||!state.file)return;
    state.password=$('import-password').value;state.errors={};
    if(!state.password){state.errors.password='请输入文件密码。';showScene('import');$('import-password').focus();return;}
    const valid=state.password==='demo1234';showScene('decrypting');
    state.timer=setTimeout(()=>{if(valid){state.plan=makePlan();state.detailTab='channels';showScene('preview');}else showScene('wrong-password');},1000);
  }
  function beginImport() {
    if(busyScenes.has(state.scene))return;
    const latest=makePlan();
    if(JSON.stringify(latest.channels.map(c=>c.name))!==JSON.stringify(state.plan.channels.map(c=>c.name))||JSON.stringify(latest.groups.map(g=>g.name))!==JSON.stringify(state.plan.groups.map(g=>g.name))){state.plan=latest;state.occupiedName='预览中的名称';showScene('changed');return;}
    showScene('importing');state.timer=setTimeout(()=>{commitMockPlan();showScene('success');},1100);
  }
  function toast(text) {
    clearTimeout(state.toastTimer);$('toast').hidden=false;$('toast').textContent=text;state.toastTimer=setTimeout(()=>$('toast').hidden=true,2600);
  }
  function resetData() {
    state.targetChannels=new Set(['OpenAI','Anthropic']);state.targetGroups=new Set(['coding','daily']);state.modelCount=4;
    state.sourceEmpty=false;state.lastResult=null;state.plan=null;state.long=false;state.occupiedName='';state.errors={};state.file=false;state.password='';state.exportPassword='';state.exportConfirm='';state.detailTab='channels';
  }
  function advance() {
    clearTimeout(state.timer);
    if(state.scene==='exporting')showScene('exported');
    else if(state.scene==='decrypting'){state.plan=makePlan();showScene('preview');}
    else if(state.scene==='importing'){commitMockPlan();showScene('success');}
  }

  document.addEventListener('click',event=>{
    const showPassword=event.target.closest('[data-show-password]');
    if(showPassword){const input=$(showPassword.dataset.showPassword);input.type=input.type==='password'?'text':'password';showPassword.setAttribute('aria-label',(input.type==='password'?'显示':'隐藏')+input.closest('.field').querySelector('label').textContent);return;}
    const tab=event.target.closest('[data-tab]');
    if(tab&&!busyScenes.has(state.scene)){const scroll=$('dialog-body').scrollTop;state.detailTab=tab.dataset.tab;renderPreview();$('dialog-body').scrollTop=scroll;$('tab-'+state.detailTab).focus({preventScroll:true});return;}
    const b=event.target.closest('[data-action]');
    if(!b||b.disabled)return;
    switch(b.dataset.action){
      case 'close':closeModal();break;
      case 'select-file':state.file=true;state.errors={};renderImport();$('import-password').focus();$('demo-message').textContent='已模拟选择文件 · 密码 demo1234';break;
      case 'change-file':state.file=false;state.password='';state.errors={};state.plan=null;showScene('import');break;
      case 'new-file':state.file=false;state.password='';state.errors={};state.plan=null;showScene('import');break;
      case 'back-import':state.errors={};showScene('import');break;
      case 'confirm-import':beginImport();break;
      case 'retry-export':state.errors={};showScene('export');break;
      case 'retry-preview':state.plan=makePlan();showScene('preview');break;
      case 'view-groups':showGroups();break;
      case 'back-settings':showScene('settings');break;
    }
  });
  document.querySelectorAll('[data-context]').forEach(b=>{b.title='只读上下文：本原型仅演示配置迁移';b.addEventListener('click',()=>toast('本原型仅演示配置迁移，此处为现有页面的只读上下文。'));});
  document.querySelectorAll('[data-nav]').forEach(b=>b.addEventListener('click',()=>{if(b.dataset.nav==='settings')showScene('settings');else if(b.dataset.nav==='groups'&&state.lastResult)showGroups();else toast('本原型仅演示配置迁移；完成导入后可查看模拟分组。');}));
  $('open-export').addEventListener('click',()=>{state.opener='open-export';state.errors={};state.exportPassword='';state.exportConfirm='';state.exportCounts=localCounts();showScene('export');});
  $('open-import').addEventListener('click',()=>{state.opener='open-import';state.file=false;state.password='';state.errors={};state.plan=null;state.long=false;showScene('import');});
  $('close-modal').addEventListener('click',closeModal);
  $('modal-layer').addEventListener('click',e=>{if(e.target===$('modal-layer'))closeModal();});
  $('scene-select').addEventListener('change',e=>showScene(e.target.value,{preset:true}));
  $('reset-demo').addEventListener('click',()=>{clearTimeout(state.timer);resetData();showScene('settings');toast('演示已重置，所有变化仅发生在模拟数据中。');});
  $('advance-step').addEventListener('click',advance);
  $('theme-toggle').addEventListener('click',()=>{const dark=document.documentElement.classList.toggle('dark');document.documentElement.classList.toggle('light',!dark);$('theme-toggle').innerHTML=icon(dark?'moon':'sun');$('theme-toggle').setAttribute('aria-label',dark?'切换到浅色':'切换到深色');$('theme-toggle').title=dark?'切换到浅色':'切换到深色';});
  document.addEventListener('keydown',e=>{
    if($('modal-layer').hidden)return;
    if(e.key==='Escape'){e.preventDefault();closeModal();}
    if(e.target.matches('[role="tab"]')&&['ArrowLeft','ArrowRight'].includes(e.key)){
      e.preventDefault();const keys=['channels','models','groups'];state.detailTab=keys[(keys.indexOf(state.detailTab)+(e.key==='ArrowRight'?1:2))%3];const scroll=$('dialog-body').scrollTop;renderPreview();$('dialog-body').scrollTop=scroll;$('tab-'+state.detailTab).focus({preventScroll:true});
    }
    if(e.key==='Tab'&&!e.target.closest('.demo-toolbar')){
      const focusable=[...$('dialog').querySelectorAll('button:not(:disabled),input:not(:disabled),[tabindex="0"]')].filter(x=>!x.hidden&&x.offsetParent!==null);
      if(!focusable.length){e.preventDefault();$('dialog').focus();return;}
      const first=focusable[0],last=focusable[focusable.length-1];
      if(e.shiftKey&&(document.activeElement===first||document.activeElement===$('dialog'))){e.preventDefault();last.focus();}
      else if(!e.shiftKey&&document.activeElement===last){e.preventDefault();first.focus();}
    }
  });
  const params=new URLSearchParams(location.search);
  if(params.get('theme')==='dark'){$('theme-toggle').click();}
  const scene=params.get('scene')||'settings';
  const validScenes=[...$('scene-select').options].map(o=>o.value);
  showScene(validScenes.includes(scene)?scene:'settings',{preset:true,focus:false});
})();
