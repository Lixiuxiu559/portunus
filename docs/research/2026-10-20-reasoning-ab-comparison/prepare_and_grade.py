"""Public synthetic business cases, independent exhaustive/simulation oracles; no credentials."""
import itertools,json,pathlib,heapq,collections
ROOT=pathlib.Path(__file__).parent
JOBS={
'A':{'duration':4,'resources':['ingest','operator'],'after':[]},
'B':{'duration':3,'resources':['ingest'],'after':[]},
'C':{'duration':5,'resources':['compute'],'after':['A']},
'D':{'duration':2,'resources':['compute','operator'],'after':['A']},
'E':{'duration':4,'resources':['transfer','operator'],'after':['B']},
'F':{'duration':3,'resources':['compute'],'after':['C','E']},
'G':{'duration':2,'resources':['ingest','operator'],'after':['D']},
'H':{'duration':3,'resources':['transfer'],'after':['F','G']},
}

def schedule_oracle():
    best=10**9;best_starts=None;orders=0
    # All precedence-respecting serial priority lists; earliest feasible insertion
    # produces the active schedules containing an optimum for this regular objective.
    def search(starts,ends,occupied):
        nonlocal best,best_starts,orders
        if len(starts)==len(JOBS):
            orders+=1;finish=max(ends.values())
            if finish<best:best=finish;best_starts=dict(starts)
            return
        for name,j in JOBS.items():
            if name in starts or any(p not in ends for p in j['after']):continue
            t=max([ends[p] for p in j['after']]+[0]);d=j['duration']
            while True:
                conflicts=[b for r in j['resources'] for a,b in occupied.get(r,[]) if t<b and a<t+d]
                if not conflicts:break
                t=min(conflicts)
            new={r:list(v) for r,v in occupied.items()}
            for r in j['resources']:new.setdefault(r,[]).append((t,t+d))
            search({**starts,name:t},{**ends,name:t+d},new)
    search({},{},{})
    return {'makespan':best,'starts':best_starts,'orders_checked':orders}

CACHE_SOURCE='''from collections import OrderedDict
import heapq
class Cache:
    def __init__(self, capacity):
        self.capacity = capacity
        self.items = OrderedDict()  # key -> (value, expiry)
        self.expiries = []
    def expire(self, now):
        while self.expiries and self.expiries[0][0] < now:
            _, key = heapq.heappop(self.expiries)
            self.items.pop(key, None)
    def put(self, now, key, value, ttl):
        self.expire(now)
        if len(self.items) >= self.capacity:
            self.items.popitem(last=False)
        self.items[key] = (value, now + ttl)
        self.items.move_to_end(key)
        heapq.heappush(self.expiries, (now + ttl, key))
    def get(self, now, key):
        self.expire(now)
        if key not in self.items:
            return None
        self.items.move_to_end(key)
        return self.items[key][0]
'''
OPS=[
['put',0,'a','A1',5],['put',0,'b','B1',12],['put',1,'c','C1',20],
['get',2,'a'],['put',3,'a','A2',15],['get',4,'b'],['get',5,'a'],
['put',5,'d','D1',8],['get',6,'a'],['get',6,'c'],['get',12,'b'],
['put',12,'d','D2',10],['get',13,'d'],['get',14,'d'],
['put',14,'e','E1',7],['get',18,'a'],['get',21,'e'],['get',22,'d'],
['put',23,'x','X1',4],['put',24,'y','Y1',8],['put',25,'x','X2',10],
['get',27,'x'],['get',28,'x'],['get',32,'y'],['get',35,'x']]

# Deliberately execute only the fixed source literal above, never model-generated code.
def cache_oracle():
    scope={};exec(CACHE_SOURCE,scope);buggy=scope['Cache'](3)
    actual=[];correct=[];items=collections.OrderedDict()
    for op,now,*args in OPS:
        if op=='put':buggy.put(now,*args)
        else:actual.append(buggy.get(now,*args))
        for k,(_,expiry) in list(items.items()):
            if expiry<=now:del items[k]
        if op=='put':
            key,value,ttl=args
            if key not in items and len(items)>=3:items.popitem(last=False)
            items[key]=(value,now+ttl);items.move_to_end(key)
        else:
            key=args[0]
            if key not in items:correct.append(None)
            else:correct.append(items[key][0]);items.move_to_end(key)
    return {'actual_reads':actual,'correct_reads':correct,'bugs':['expiry_boundary','stale_heap_record','eviction_on_update']}

def prepare():
    schedule='''You are planning a production data-delivery pipeline. Find a schedule with the globally minimum makespan. Time starts at 0, starts/durations are nonnegative integer minutes, jobs are non-preemptive. Each listed resource has capacity 1. A job holds ALL its resources for its full duration. Every predecessor must finish before the job starts. Intervals are half-open [start,end), so back-to-back jobs can share a boundary. There are no other constraints. Return ONLY JSON {"makespan": integer, "starts": {"A": integer, ..., "H": integer}} with all 8 jobs exactly once. Do not return code or explanatory prose. Job specification:\n'''+json.dumps(JOBS,sort_keys=True)
    cache='''Review a real-world style TTL/LRU cache implementation. Capacity is 3, operations run sequentially in the listed order (including equal timestamps). Values are strings and TTLs are positive. For the CORRECT specification: a key is expired when now >= its expiry; a successful get makes it MRU; a put inserts or updates a key, renews expiry and makes it MRU; only inserting a NEW key into a full live cache evicts the LRU. Renewed keys must not be removed by older expiry records. For ACTUAL behavior, simulate the provided code exactly, even when buggy. Return ONLY JSON with actual_reads (results for all get operations, JSON null for missing), correct_reads (same operations under the correct specification), and bugs. bugs must select all and only genuine defects from these identifiers: expiry_boundary, stale_heap_record, eviction_on_update, get_should_not_touch_lru, heap_should_be_max_heap, ttl_should_be_absolute. Do not return code or explanatory prose.\nCODE:\n'''+CACHE_SOURCE+'\nOPERATIONS:\n'+json.dumps(OPS)
    cases=[{'id':'pipeline_schedule','source_protocol':'anthropic','prompt':schedule},{'id':'ttl_lru_debug','source_protocol':'openai_responses','prompt':cache}]
    (ROOT/'cases.json').write_text(json.dumps(cases,ensure_ascii=False,indent=2))
    (ROOT/'oracle.json').write_text(json.dumps({'pipeline_schedule':schedule_oracle(),'ttl_lru_debug':cache_oracle()},indent=2))
    print((ROOT/'oracle.json').read_text())

def grade(case,answer,oracle):
    try:
        answer=answer.strip()
        if answer.startswith('```'):answer=answer.split('\n',1)[1].rsplit('```',1)[0]
        got=json.loads(answer)
    except Exception:return {'correct':False,'reason':'invalid/missing JSON'}
    if case=='ttl_lru_debug':
        checks={k:got.get(k)==v for k,v in oracle.items() if k!='bugs'}
        checks['bugs']=isinstance(got.get('bugs'),list) and sorted(got['bugs'])==sorted(oracle['bugs'])
        return {'correct':all(checks.values()),'checks':checks}
    starts=got.get('starts',{})
    if set(starts)!=set(JOBS) or any(type(v)!=int or v<0 for v in starts.values()):return {'correct':False,'reason':'invalid job starts'}
    ends={k:starts[k]+j['duration'] for k,j in JOBS.items()}
    feasible=all(ends[p]<=starts[k] for k,j in JOBS.items() for p in j['after'])
    for a,b in itertools.combinations(JOBS,2):
        if set(JOBS[a]['resources'])&set(JOBS[b]['resources']) and starts[a]<ends[b] and starts[b]<ends[a]:feasible=False
    makespan=max(ends.values());reported=got.get('makespan')==makespan
    return {'correct':feasible and reported and makespan==oracle['makespan'],'feasible':feasible,'reported_makespan_correct':reported,'makespan':makespan,'optimal_makespan':oracle['makespan']}

if __name__=='__main__':
    import sys
    if len(sys.argv)>1 and sys.argv[1]=='grade':
        oracle=json.loads((ROOT/'oracle.json').read_text());results=[]
        for line in (ROOT/'results.jsonl').read_text().splitlines():
            r=json.loads(line);r['grading']=grade(r['task'],r.get('answer',''),oracle[r['task']]);results.append(r)
        (ROOT/'graded_results.json').write_text(json.dumps(results,ensure_ascii=False,indent=2));print(json.dumps([{k:r.get(k) for k in ['task','route','effort','status','duration_ms','reasoning_chars','finish_reason','grading']} for r in results],ensure_ascii=False,indent=2))
    else:prepare()
