"""运行已构建的实际 main 二进制，临时独立数据库和本地上游，不调用外部模型。"""
import json, os, pathlib, subprocess, sys, tempfile, threading, time, urllib.request, uuid
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = pathlib.Path(__file__).resolve().parents[3]
OUT = pathlib.Path(__file__).resolve().parent
calls = []
class Upstream(BaseHTTPRequestHandler):
    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        target = 'A' if self.path.startswith('/a/') else 'B'
        calls.append({'target': target, 'request_id': self.headers.get('X-Request-Id'),
                      'budget_fields': [key for key in ('max_tokens','max_completion_tokens') if key in body]})
        n = sum(row['target'] == 'A' for row in calls)
        status = 200
        if target == 'A' and n == 1:
            status = 400
            data = {'error': {'message': "Unsupported parameter: 'max_tokens' is not supported with this model. Use 'max_completion_tokens' instead.", 'param':'max_tokens','code':'unsupported_parameter'}}
        elif target == 'A':
            status = 503
            data = {'error': {'message': 'synthetic overload'}}
        else:
            data = {'id':'local-probe','object':'chat.completion','model':'model-b',
                    'choices':[{'index':0,'message':{'role':'assistant','content':'OK'},'finish_reason':'stop'}],
                    'usage':{'prompt_tokens':7,'completion_tokens':1,'total_tokens':8}}
        self.send_response(status); self.send_header('Content-Type','application/json'); self.end_headers()
        self.wfile.write(json.dumps(data).encode())
    def log_message(self, *_): pass

def main():
    upstream = ThreadingHTTPServer(('127.0.0.1', 0), Upstream)
    threading.Thread(target=upstream.serve_forever, daemon=True).start()
    proc = None
    try:
        with tempfile.TemporaryDirectory(prefix='portunus-attempt-main-') as tmp:
            folder = pathlib.Path(tmp)
            config = {'server':{'host':'127.0.0.1','port':13473},
                      'database':{'type':'sqlite','path':str(folder/'main.db'),'log_path':str(folder/'logs.db')},
                      'proxy':{'retry_count':1,'circuit_failure_threshold':9}}
            (folder/'config.json').write_text(json.dumps(config))
            env = {key:value for key,value in os.environ.items() if not key.startswith('PORTUNUS_')}
            instance = 'attempt-probe-' + uuid.uuid4().hex
            env['PORTUNUS_INSTANCE_ID'] = instance
            with open(folder/'server.log','w') as log:
                proc = subprocess.Popen([sys.argv[1]], cwd=folder, env=env, stdout=log, stderr=subprocess.STDOUT)
                base = 'http://127.0.0.1:13473'
                def request(path, data=None, method=None, key=None):
                    headers = {'X-Portunus-Local':'1','Content-Type':'application/json'}
                    if key: headers['Authorization'] = 'Bearer '+key
                    req = urllib.request.Request(base+path, data=None if data is None else json.dumps(data).encode(), headers=headers, method=method)
                    with urllib.request.urlopen(req, timeout=15) as response:
                        return json.load(response), response.headers
                for _ in range(100):
                    try:
                        _, ping_headers = request('/api/ping')
                        if ping_headers.get('X-Portunus-Instance') != instance:
                            raise RuntimeError('unexpected server instance')
                        break
                    except Exception:
                        if proc.poll() is not None: raise RuntimeError('server exited')
                        time.sleep(.1)
                else: raise RuntimeError('server did not start')
                port = upstream.server_address[1]
                models = []
                for suffix in ('a','b'):
                    channel,_ = request('/api/channels', {'name':'probe-'+suffix,'type':'openai','base_url':f'http://127.0.0.1:{port}/{suffix}','key':'synthetic-upstream-key','auto_sync':False})
                    model,_ = request('/api/models', {'channel_id':channel['id'],'name':'model-'+suffix})
                    models.append(model)
                request('/api/groups', {'name':'probe','strategy':'failover','items':[{'model_id':m['id'],'priority':i} for i,m in enumerate(models)]})
                token,_ = request('/api/apikey')
                reply,headers = request('/v1/responses', {'model':'probe','input':'Synthetic probe','max_output_tokens':16},key=token['key'])
                rid = headers['X-Request-Id']
                attempts,_ = request('/api/logs/attempts?request_id='+rid)
                logs,_ = request('/api/logs?request_id='+rid)
                stats,_ = request('/api/logs/stat?request_id='+rid)
                rows = attempts['data']
                assert len(calls)==attempts['total']==4, (calls,attempts)
                assert [r['sequence'] for r in rows]==[1,2,3,4]
                assert [r['relay_attempt'] for r in rows]==[1,1,2,3]
                assert [r['reason'] for r in rows]==['initial','budget_compat','retry','failover']
                assert [r['status'] for r in rows]==[400,503,503,200]
                assert [r['success'] for r in rows]==[False,False,False,True]
                assert [r['budget_field'] for r in rows]==['max_tokens','max_completion_tokens','max_completion_tokens','max_tokens']
                assert all(r['request_id']==rid for r in rows+calls)
                assert logs['total']==3 and stats['total_requests']==3
                assert stats['input_tokens']==7 and stats['output_tokens']==1
                assert reply['status']=='completed' and 'OK' in json.dumps(reply)
                report={'calls':calls,'attempts':attempts,'logs':logs,'stats':stats,'reply':reply,
                        'assertions':'passed','independent_log_db':True}
                (OUT/'main-probe-result.json').write_text(json.dumps(report,ensure_ascii=False,indent=2)+'\n')
                print(json.dumps({'assertions':'passed','http_calls':4,'log_rows':3,'attempts':4,'input_tokens':7,'output_tokens':1}))
    finally:
        if proc is not None:
            proc.terminate()
            try: proc.wait(timeout=5)
            except subprocess.TimeoutExpired: proc.kill(); proc.wait()
        upstream.shutdown(); upstream.server_close()

if __name__ == '__main__': main()
