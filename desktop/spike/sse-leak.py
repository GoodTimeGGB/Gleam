import json, os, re, shutil, subprocess, time, sys
from playwright.sync_api import sync_playwright
DESK=os.path.dirname(os.path.dirname(os.path.abspath(__file__))); ELECTRON=f"{DESK}/node_modules/electron/dist/electron"; ROOT="/tmp/gspike"
for d in ("ud-leak", "data-leak"):
    shutil.rmtree(f"{ROOT}/{d}", ignore_errors=True); os.makedirs(f"{ROOT}/{d}")
os.makedirs(f"{ROOT}/ws", exist_ok=True); os.makedirs(os.environ.get("SPIKE_OUT", "/tmp/gspike/out"), exist_ok=True)
env=dict(os.environ, DISPLAY=os.environ.get("SPIKE_DISPLAY", ":77"), GLEAM_DESKTOP_USER_DATA=f"{ROOT}/ud-leak", GLEAM_SIDECAR_ARGS=f"--mock-llm --data-dir {ROOT}/data-leak --workspace {ROOT}/ws")
p=subprocess.Popen([ELECTRON,".","--remote-debugging-port=9335"],cwd=DESK,env=env,stdout=open(f"{ROOT}/leak.log","w"),stderr=subprocess.STDOUT)
for _ in range(100):
    s=open(f"{ROOT}/leak.log").read()
    if "did-finish-load" in s: break
    time.sleep(0.2)
port=int(re.search(r"addr=[\d.]+:(\d+)",s).group(1))
def est():
    out=subprocess.run(["ss","-Htn","state","established",f"( sport = :{port} )"],capture_output=True,text=True).stdout
    return len([l for l in out.splitlines() if l.strip()])
res=[]
with sync_playwright() as pw:
    b=pw.chromium.connect_over_cdp("http://127.0.0.1:9335")
    pg=[x for c in b.contexts for x in c.pages if x.url.startswith("app://")][0]
    pg.wait_for_timeout(1500)
    for rnd in range(10):
        r=pg.evaluate("""async () => {
          const open = (n) => Promise.all(Array.from({length:n}, () => new Promise(res => {
            const es = new EventSource('/api/events'); const t = setTimeout(() => res({es, ok:false}), 4000);
            es.onopen = () => { clearTimeout(t); res({es, ok:true}); }; })));
          const list = await open(3);
          const opened = list.filter(x => x.ok).length;
          list.forEach(x => x.es.close());
          await new Promise(r => setTimeout(r, 800));
          const t0 = performance.now();
          const st = await Promise.race([fetch('/api/info').then(r => r.status), new Promise(r => setTimeout(() => r('timeout'), 4000))]);
          return {opened, info: st, ms: Math.round(performance.now() - t0)};
        }""")
        r["established_after"]=est(); r["round"]=rnd+1; res.append(r); print(r, flush=True)
    # reload page twice (old page's EventSource must be released upstream)
    for i in range(3):
        pg.reload(timeout=10000); pg.wait_for_selector("#goal-input"); pg.wait_for_timeout(800); print("reload", i, "ok", est(), flush=True)
    st=pg.evaluate("() => Promise.race([fetch('/api/info').then(r => r.status), new Promise(r => setTimeout(() => r('timeout'), 4000))])")
    res.append({"after_3_reloads_info": st, "established": est()})
    b.close()
p.terminate(); p.wait(10)
print(json.dumps(res, indent=1)); json.dump(res, open(os.path.join(os.environ.get('SPIKE_OUT', '/tmp/gspike/out'), 'sse-release-results.json'),'w'), indent=1)
