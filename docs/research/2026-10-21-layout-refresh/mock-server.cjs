// Isolated UI fixture. No user database, provider credentials or external requests.
const http=require('node:http'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../../../web/dist/renderer');
const events=[];
const channels=[
 {id:1,name:'DeepSeek 官方服务',provider_id:'deepseek',type:'openai',base_url:'https://api.deepseek.com',key:'fixture-token',auto_sync:true},
 {id:2,name:'OpenRouter · 通用模型',provider_id:'openrouter',type:'openai',base_url:'https://openrouter.ai/api/v1',key:'fixture-token',auto_sync:true},
 {id:3,name:'团队中转站 · 研发与测试环境',provider_id:'generic',type:'openai_responses',base_url:'https://gateway.example.test/team/v1',key:'fixture-token',connection_options:{model_discovery:'manual'}},
 {id:4,name:'Claude 工作流',provider_id:'anthropic',type:'anthropic',base_url:'https://api.anthropic.com/v1',key:'fixture-token'},
];
const models=Array.from({length:38},(_,i)=>({id:i+1,channel_id:i%4+1,name:['deepseek-chat','openai/gpt-5.2','gpt-5.4-long-context-preview','claude-sonnet-4-6','deepseek-reasoner','anthropic/claude-opus-4-6'][i%6]+(i<6?'':`-${i+1}`)}));
const groups=['日常对话','代码助手','文档与长上下文','可靠性测试'].map((name,i)=>({id:i+1,name,strategy:['manual','failover','round_robin','manual'][i],active_item_id:i===3?0:models[i].id,items:models.slice(i,i+3).map((m,j)=>({id:m.id,model_id:m.id,model_name:m.name,channel_id:m.channel_id,priority:j}))}));
const logs=Array.from({length:10},(_,i)=>({id:i+1,created_at:new Date(Date.now()-i*60000).toISOString(),request_id:`req_fixture_${i+1}`,group_name:groups[i%4].name,model_name:models[i].name,channel_id:i%4+1,success:i!==3,client_protocol:i%2?'anthropic':'openai',input_token:480+i*70,output_token:320+i*20,cache_read_token:1200,duration_ms:1200+i*130,first_token_ms:460,stream:true,error:i===3?'模拟上游连接超时':'',response_status:i===3?504:200}));
const snap={items:[{id:'cny',label:'人民币钱包',kind:'balance',scope:'account',unit:{kind:'money',code:'CNY'},remaining:'128.60',limit_state:'unknown'},{id:'usd',label:'美元钱包',kind:'balance',scope:'account',unit:{kind:'money',code:'USD'},remaining:'0',limit_state:'unknown'}]};
let sources=[{id:1,channel_id:1,name:'DeepSeek 账户余额',adapter_id:'deepseek.balance',query_base_url:'https://api.deepseek.com',schema_version:1,config_revision:1,enabled:true,credential_mode:'inherit_channel_key',has_token:true,options:{},auto_refresh_minutes:0,state:{status:'fresh',in_progress:false,last_success_at:new Date().toISOString(),snapshot:snap}}];
const adapters=[{id:'deepseek.balance',name:'DeepSeek 账户余额',description:'查询官方账户余额，按币种分别展示。',provider_ids:['deepseek'],default_base_url:'https://api.deepseek.com',credential_modes:['inherit_channel_key','dedicated'],fields:[]},{id:'custom.balance',name:'自定义余额 / 额度',description:'声明一次 JSON 查询与字段映射，不执行脚本',provider_ids:[],default_base_url:'',credential_modes:['inherit_channel_key','dedicated','none'],request_methods:['GET','POST'],fields:[]},{id:'newapi.account',name:'NewAPI 账户额度',description:'使用独立账户 Token 查询原始额度。',provider_ids:['newapi'],default_base_url:'',credential_modes:['dedicated'],fields:[{key:'user_id',label:'用户 ID',required:true},{key:'quota_per_unit',label:'每展示单位对应额度'},{key:'display_unit',label:'展示单位'}]}];
function send(res,data,status=200){res.writeHead(status,{'content-type':'application/json'});res.end(JSON.stringify(data));}
const server=http.createServer(async(req,res)=>{
 try {
 const u=new URL(req.url,'http://localhost');
 if(u.pathname==='/__events')return send(res,events);
 if(u.pathname.startsWith('/api/')){
 let text='';for await(const part of req)text+=part;const data=text?JSON.parse(text):{};
 events.push({method:req.method,path:u.pathname,query:u.search,body:data});
 if(u.pathname==='/api/channels')return send(res,channels);
 if(u.pathname==='/api/models'){let list=models.filter(m=>(!u.searchParams.get('channel_id')||m.channel_id==u.searchParams.get('channel_id'))&&(!u.searchParams.get('name')||m.name.toLowerCase().includes(u.searchParams.get('name').toLowerCase())));const size=Number(u.searchParams.get('page_size')||24),page=Number(u.searchParams.get('page')||1);return send(res,{total:list.length,data:list.slice((page-1)*size,page*size)});}
 if(u.pathname==='/api/groups')return send(res,groups);
 if(u.pathname==='/api/settings')return send(res,{sync_interval:30,last_sync_at:Math.floor(Date.now()/1000)-120,log_retention_days:30});
 if(u.pathname==='/api/apikey')return send(res,{key:'sk-fixture-only-ui-example'});
 if(u.pathname==='/api/logs/stat')return send(res,{input_tokens:18940,output_tokens:6280,cache_read_tokens:7200,cache_write_tokens:0,total_requests:26,recent_requests:2});
 if(u.pathname==='/api/logs')return send(res,{total:logs.length,data:logs.filter(l=>(!u.searchParams.get('group_name')||l.group_name===u.searchParams.get('group_name'))&&(!u.searchParams.get('request_id')||l.request_id===u.searchParams.get('request_id')))});
 if(u.pathname==='/api/logs/requests')return send(res,{total:logs.length,data:logs.map(l=>({key:`request:${l.request_id}`,request_id:l.request_id,log_count:1,first_at:l.created_at,last_at:l.created_at,latest_log:l,channels:[l.channel_id]}))});
 if(u.pathname==='/api/logs/attempts')return send(res,{total:0,data:[]});
 if(u.pathname==='/api/quota/adapters')return send(res,adapters);
 if(u.pathname==='/api/quota/summaries')return send(res,channels.map(c=>({channel_id:c.id,sources:sources.filter(s=>s.channel_id===c.id)})));
 if(u.pathname==='/api/quota-sources/preview')return send(res,{status:'fresh',in_progress:false,snapshot:snap});
 let match=u.pathname.match(/^\/api\/channels\/(\d+)\/quota-sources$/);
 if(match){if(req.method==='GET')return send(res,sources.filter(s=>s.channel_id==match[1]));const s={...data,id:sources.length+1,channel_id:Number(match[1]),config_revision:1,has_token:!!data.credentials?.token,state:{status:data.enabled?'never':'disabled',in_progress:false}};delete s.credentials;sources.push(s);return send(res,s,201);}
 match=u.pathname.match(/^\/api\/quota-sources\/(\d+)(\/refresh)?$/);
 if(match){const s=sources.find(s=>s.id==match[1]);if(!s)return send(res,{error:'模拟来源不存在'},404);if(match[2]){s.state={status:'fresh',in_progress:false,snapshot:snap};return send(res,s);}if(req.method==='PUT'){Object.assign(s,data,{config_revision:s.config_revision+1,state:{status:data.enabled?'invalidated':'disabled',in_progress:false}});delete s.credentials;return send(res,s);}if(req.method==='DELETE'){sources=sources.filter(x=>x!==s);return send(res,{});}}
 if(u.pathname==='/api/upstream-presets')return send(res,[{id:'generic',name:'通用兼容服务',default_profile:'chat',profiles:[{id:'chat',name:'Chat Completions',type:'openai',base_url:'',adapter_id:'generic',model_discovery:'api',help:'填写 API 根地址'}]}]);
 if(u.pathname.endsWith('/sync'))return send(res,{added:0});
 return send(res,{});
 }
 const file=path.resolve(root,'.'+decodeURIComponent(u.pathname==='/'?'/index.html':u.pathname));
 if(!file.startsWith(root+path.sep))return send(res,{},403);
 const ext=path.extname(file);fs.readFile(file,(err,bytes)=>{if(err)return send(res,{},404);res.writeHead(200,{'content-type':({'.html':'text/html','.js':'application/javascript','.css':'text/css','.png':'image/png','.woff2':'font/woff2'})[ext]||'application/octet-stream'});res.end(bytes);});
 }catch{send(res,{error:'mock error'},500);}
});
server.listen(18767,'127.0.0.1',()=>console.log('isolated layout fixture http://127.0.0.1:18767'));
