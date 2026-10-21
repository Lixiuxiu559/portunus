// Local balance presentation fixture. No real credentials, database or upstream.
const http=require('node:http'),fs=require('node:fs'),path=require('node:path');
const root=path.resolve(__dirname,'../../../web/dist/renderer');
const channels=[{id:1,name:'个人-deepseek',type:'openai',provider_id:'deepseek',base_url:'https://api.deepseek.com'},{id:2,name:'团队账户',type:'openai',provider_id:'deepseek',base_url:'https://api.deepseek.com'},{id:3,name:'备用账户',type:'openai',provider_id:'openrouter',base_url:'https://openrouter.ai/api/v1'},{id:4,name:'尚未配置',type:'anthropic',base_url:'https://api.anthropic.com/v1'}];
const events=[];let mode='normal',delayedGET=false;
const snapshot=(value,code='CNY')=>({items:[{id:'cash',label:'账户余额',kind:'balance',scope:'account',unit:{kind:'money',code},remaining:value,limit_state:'unknown'}]});
const sources=[{id:1,channel_id:1,name:'DeepSeek账户余额',adapter_id:'deepseek.balance',query_base_url:'https://api.deepseek.com',config_revision:1,enabled:true,credential_mode:'inherit_channel_key',has_token:true,options:{},auto_refresh_minutes:0,state:{status:'stale',in_progress:false,last_success_at:new Date(Date.now()-12*60000).toISOString(),snapshot:snapshot('321.45')}},{id:2,channel_id:2,name:'团队账户余额',adapter_id:'deepseek.balance',query_base_url:'https://api.deepseek.com',config_revision:1,enabled:true,credential_mode:'dedicated',has_token:true,options:{},auto_refresh_minutes:0,state:{status:'unavailable',in_progress:false,failure:{category:'authentication',message:'上游额度查询凭据无效'}}},{id:3,channel_id:3,name:'备用账户余额',adapter_id:'openrouter.account_balance',query_base_url:'https://openrouter.ai/api/v1',config_revision:1,enabled:true,credential_mode:'dedicated',has_token:true,options:{},auto_refresh_minutes:0,state:{status:'fresh',in_progress:false,last_success_at:new Date().toISOString(),snapshot:snapshot('0','USD')}}];
const send=(res,data,status=200)=>{if(res.destroyed)return;res.writeHead(status,{'content-type':'application/json','cache-control':'no-store'});res.end(JSON.stringify(data));};
function finish(s){s.state={status:'fresh',in_progress:false,last_success_at:new Date().toISOString(),snapshot:snapshot('322.45')};}
const server=http.createServer(async(req,res)=>{
 const u=new URL(req.url,'http://localhost');let data={};
 if(req.method==='POST'){let b='';for await(const c of req)b+=c;try{data=JSON.parse(b||'{}');}catch{return send(res,{},400);}}
 if(u.pathname==='/__events')return send(res,events);
 if(u.pathname==='/__scenario'){mode=data.mode||'normal';delayedGET=!!data.delayGET;return send(res,{mode,delayedGET});}
 if(u.pathname.startsWith('/api/')){
 events.push({method:req.method,path:u.pathname,at:Date.now()});
 if(u.pathname==='/api/channels')return send(res,channels);
 if(u.pathname==='/api/quota/summaries'){
  const body=JSON.parse(JSON.stringify(channels.map(c=>({channel_id:c.id,sources:sources.filter(s=>s.channel_id===c.id)}))));
  if(delayedGET){delayedGET=false;setTimeout(()=>send(res,body),2300);return;}return send(res,body);
 }
 if(u.pathname==='/api/quota/adapters')return send(res,[{id:'deepseek.balance',name:'DeepSeek账户余额',default_base_url:'https://api.deepseek.com',provider_ids:['deepseek'],credential_modes:['inherit_channel_key','dedicated'],fields:[]}]);
 let m=u.pathname.match(/^\/api\/channels\/(\d+)\/quota-sources$/);if(m)return send(res,sources.filter(s=>s.channel_id==m[1]));
 m=u.pathname.match(/^\/api\/quota-sources\/(\d+)\/refresh$/);
 if(m){const s=sources.find(s=>s.id==m[1]);if(!s)return send(res,{},404);
  if(mode==='limited')return send(res,{error:'模拟限流'},429);
  if(mode==='polling'){s.state={...s.state,in_progress:true};setTimeout(()=>finish(s),700);return send(res,s,202);}
  if(mode==='failure'){s.state={...s.state,status:'stale',in_progress:false,failure:{category:'temporary',message:'模拟上游暂时不可用，保留上次查询值'}};return send(res,s);}
  setTimeout(()=>{finish(s);send(res,s);},700);return;
 }
 return send(res,[]);
 }
 const filename=path.resolve(root,'.'+decodeURIComponent(u.pathname==='/'?'/index.html':u.pathname));if(!filename.startsWith(root+path.sep))return send(res,{},403);
 fs.readFile(filename,(err,bytes)=>{if(err)return send(res,{},404);res.writeHead(200,{'cache-control':'no-store','content-type':({'.html':'text/html','.js':'application/javascript','.css':'text/css','.png':'image/png','.woff2':'font/woff2'})[path.extname(filename)]||'application/octet-stream'});res.end(bytes);});
});server.listen(18769,'127.0.0.1',()=>console.log('balance fixture pid='+process.pid+' url=http://127.0.0.1:18769'));
