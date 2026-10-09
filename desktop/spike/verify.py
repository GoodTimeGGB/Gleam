import json, os, re, signal, subprocess, sys, time, urllib.request
os.makedirs(os.environ.get("SPIKE_OUT", "/tmp/gspike/out"), exist_ok=True)
from playwright.sync_api import sync_playwright

DESK = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
ELECTRON = f"{DESK}/node_modules/electron/dist/electron"
SHOTS = os.environ.get("SPIKE_OUT", "/tmp/gspike/out")
ROOT = "/tmp/gspike"
R = {}  # results

def env(ud, data, port):
    e = dict(os.environ, DISPLAY=os.environ.get("SPIKE_DISPLAY", ":77"), GLEAM_DESKTOP_USER_DATA=ud,
             GLEAM_SIDECAR_ARGS=f"--mock-llm --data-dir {data} --workspace {ROOT}/ws",
             GLEAM_DESKTOP_WIDTH="1440", GLEAM_DESKTOP_HEIGHT="900")
    return e

def launch(tag, ud, data, port=9333, extra=()):
    log = open(f"{ROOT}/{tag}.log", "w")
    p = subprocess.Popen([ELECTRON, ".", f"--remote-debugging-port={port}", *extra], cwd=DESK,
                         env=env(ud, data, port), stdout=log, stderr=subprocess.STDOUT)
    return p

def wait_log(tag, pat, timeout=30):
    t = time.time()
    while time.time() - t < timeout:
        s = open(f"{ROOT}/{tag}.log").read()
        m = re.search(pat, s)
        if m: return m, s
        time.sleep(0.2)
    raise SystemExit(f"timeout waiting for {pat} in {tag}.log:\n" + open(f"{ROOT}/{tag}.log").read()[-3000:])

def sidecars():
    out = subprocess.run(["pgrep", "-f", "gleam desktop-sidecar"], capture_output=True, text=True).stdout.split()
    return [int(x) for x in out]

def electron_procs():
    out = subprocess.run(["pgrep", "-f", f"{ELECTRON}"], capture_output=True, text=True).stdout.split()
    return [int(x) for x in out]

def app_page(b):
    for c in b.contexts:
        for pg in c.pages:
            if pg.url.startswith("app://"): return pg
    raise SystemExit("no app page")

def est_conns(port):
    out = subprocess.run(["ss", "-Htn", "state", "established", f"( sport = :{port} )"], capture_output=True, text=True).stdout
    return len([l for l in out.splitlines() if l.strip()])

def settle(pg):
    pg.evaluate("document.fonts.ready.then(() => 1)")
    pg.wait_for_timeout(1500)

for d in ["ud", "data", "ws", "data-chrome"]:
    subprocess.run(["rm", "-rf", f"{ROOT}/{d}"]); os.makedirs(f"{ROOT}/{d}")
assert not sidecars(), "stale sidecar before start"

# ---------- launch 1: fresh profile ----------
t0 = time.time()
p1 = launch("run1", f"{ROOT}/ud", f"{ROOT}/data")
m, _ = wait_log("run1", r"sidecar ready pid=(\d+) addr=([\d.]+):(\d+)")
spid, sport = int(m.group(1)), int(m.group(3))
m2, logtxt = wait_log("run1", r"\[desktop \+(\d+)ms\] renderer did-finish-load")
R["launch1"] = {"electron_pid": p1.pid, "sidecar_pid": spid, "sidecar_port": sport,
                "ready_line_ms": int(re.search(r"\+(\d+)ms\] sidecar ready", logtxt).group(1)),
                "did_finish_load_ms": int(m2.group(1)), "wall_to_load_s": round(time.time() - t0, 2)}

with sync_playwright() as pw:
    b = pw.chromium.connect_over_cdp("http://127.0.0.1:9333")
    pg = app_page(b)
    console, failed, bad = [], [], []
    pg.on("console", lambda m: console.append(f"{m.type}: {m.text}"))
    pg.on("requestfailed", lambda r: failed.append(f"{r.url} {r.failure}") if "/api/events" not in r.url else None)
    pg.on("response", lambda r: bad.append(f"{r.status} {r.url}") if r.status >= 400 else None)
    pg.reload(); pg.wait_for_selector("#goal-input"); settle(pg); pg.wait_for_timeout(1500)
    R["renderer"] = pg.evaluate("""async () => ({require: typeof require, process: typeof process,
        bridgeKeys: Object.keys(window.gleamDesktop || {}), origin: location.origin,
        theme: document.documentElement.dataset.theme, info: await window.gleamDesktop.info()})""")
    pg.screenshot(path=f"{SHOTS}/electron-home.png")
    from PIL import ImageGrab
    ImageGrab.grab(xdisplay=os.environ.get("SPIKE_DISPLAY", ":77")).save(f"{SHOTS}/electron-native-window-xvfb.png")
    R["reload_console"] = console[:]; R["reload_failed"] = failed[:]; R["reload_http_errors"] = bad[:]

    # ---- security probes ----
    sec = {}
    sec["renderer_fetch_loopback"] = pg.evaluate(f"""() => fetch('http://127.0.0.1:{sport}/api/info').then(r => 'status ' + r.status, e => 'blocked: ' + e.message)""")
    sec["window_open_file"] = pg.evaluate("() => String(window.open('file:///etc/hostname'))")
    def curl(path, hdr=None):
        req = urllib.request.Request(f"http://127.0.0.1:{sport}{path}", headers=hdr or {})
        try: return urllib.request.urlopen(req, timeout=5).status
        except urllib.error.HTTPError as e: return e.code
    sec["direct_loopback_no_token"] = curl("/api/info")
    sec["direct_loopback_token_file"] = curl("/api/info", {"X-Gleam-Token": open(f"{ROOT}/data/webui.token").read().strip()})
    R["security"] = sec
    pg.screenshot(path=f"{SHOTS}/electron-home-after-probes.png")

    # ---- SSE: submit a goal through the UI ----
    base_conns = est_conns(sport)
    pg.evaluate("""() => { window.__ev = []; window.__es = new EventSource('/api/events');
        window.__es.addEventListener('progress', e => window.__ev.push({t: performance.now(), d: JSON.parse(e.data)})); }""")
    pg.wait_for_timeout(800)
    conns_with_probe = est_conns(sport)
    pg.fill("#goal-input", "打个招呼")
    t_submit = pg.evaluate("performance.now()")
    pg.press("#goal-input", "Enter")
    ok = False
    for _ in range(60):
        if pg.evaluate("() => document.body.innerText.includes('（Mock 模式）收到目标：打个招呼')"):
            ok = True; break
        pg.wait_for_timeout(500)
    evs = pg.evaluate("window.__ev")
    pg.wait_for_timeout(1500)
    settle(pg)
    pg.screenshot(path=f"{SHOTS}/electron-goal-mock.png")
    R["sse"] = {"events": len(evs), "reply_rendered_in_ui": ok,
                "phases": [e["d"].get("phase") for e in evs], "last_progress": evs[-1]["d"].get("progress") if evs else None,
                "first_event_ms_after_submit": round(evs[0]["t"] - t_submit, 1) if evs else None,
                "sample": [{k: e["d"].get(k) for k in ("type", "status", "phase", "step_id", "message") if k in e["d"]} for e in evs[:3] + evs[-3:]],
                "keys_first": sorted(evs[0]["d"].keys()) if evs else None}
    pg.evaluate("() => window.__es.close()"); pg.wait_for_timeout(1000)
    R["sse_conns"] = {"before_probe": base_conns, "with_probe": conns_with_probe, "after_probe_closed": est_conns(sport)}

    # ---- theme toggle ----
    pg.evaluate("() => document.querySelector('.theme-mode-btn[data-mode=dark]').click()")
    pg.wait_for_timeout(2500)
    R["theme_set"] = pg.evaluate("() => ({theme: document.documentElement.dataset.theme, prefs: localStorage.getItem('gleam-ui')})")
    pg.screenshot(path=f"{SHOTS}/electron-dark-before-restart.png")
    # top-level navigation away from app://gleam must be refused (probe last: Playwright waits on the aborted nav)
    pg.evaluate("() => { setTimeout(() => { location.href = 'https://example.com/'; }, 0); }")
    time.sleep(2)
    b.close()
    b = pw.chromium.connect_over_cdp("http://127.0.0.1:9333")
    pg2 = app_page(b)
    R["security"]["url_after_top_nav_attempt"] = pg2.evaluate("location.href")
    R["security"]["nav_log"] = [l for l in open(f"{ROOT}/run1.log").read().splitlines() if "[security]" in l]
    b.close()

# ---- graceful quit (SIGTERM to the Electron main process) ----
t = time.time(); p1.send_signal(signal.SIGTERM)
try: p1.wait(15)
except subprocess.TimeoutExpired: p1.kill()
R["graceful_quit"] = {"electron_exit_code": p1.returncode, "seconds": round(time.time() - t, 2),
                      "log": [l for l in open(f"{ROOT}/run1.log").read().splitlines() if "stopp" in l or "退出信号" in l],
                      "sidecar_alive_after": spid in sidecars()}
time.sleep(1)

# ---------- launch 2: same profile -> prefs persist ----------
p2 = launch("run2", f"{ROOT}/ud", f"{ROOT}/data")
m, _ = wait_log("run2", r"sidecar ready pid=(\d+) addr=[\d.]+:(\d+)")
spid2, sport2 = int(m.group(1)), int(m.group(2))
wait_log("run2", r"renderer did-finish-load")
with sync_playwright() as pw:
    b = pw.chromium.connect_over_cdp("http://127.0.0.1:9333")
    pg = app_page(b); settle(pg)
    R["after_restart"] = {"sidecar_port": sport2, "port_changed": sport2 != sport,
                          **pg.evaluate("() => ({theme: document.documentElement.dataset.theme, prefs: localStorage.getItem('gleam-ui'), origin: location.origin})")}
    pg.screenshot(path=f"{SHOTS}/electron-dark-after-restart.png")

    # ---- single instance ----
    before = sidecars()
    t = time.time()
    p3 = launch("run3-second", f"{ROOT}/ud", f"{ROOT}/data", port=9334)
    try: p3.wait(20)
    except subprocess.TimeoutExpired: p3.kill()
    time.sleep(1)
    R["single_instance"] = {"second_exit_code": p3.returncode, "second_seconds": round(time.time() - t, 2),
                            "second_log": [l for l in open(f"{ROOT}/run3-second.log").read().splitlines() if "[desktop" in l],
                            "first_log_focus": [l for l in open(f"{ROOT}/run2.log").read().splitlines() if "second-instance" in l],
                            "sidecars_before": len(before), "sidecars_after": len(sidecars()),
                            "first_window_focused": pg.evaluate("document.hasFocus()")}
    b.close()

# ---------- kill -9 the Electron main process: no Go orphan ----------
t = time.time(); os.kill(p2.pid, signal.SIGKILL); p2.wait()
gone_at = None
while time.time() - t < 10:
    if spid2 not in sidecars(): gone_at = round(time.time() - t, 2); break
    time.sleep(0.1)
time.sleep(5)
R["kill9"] = {"sidecar_gone_after_s": gone_at, "sidecars_5s_later": sidecars(), "electron_procs_5s_later": electron_procs(),
              "sidecar_log": [l for l in open(f"{ROOT}/run2.log").read().splitlines() if "退出信号" in l]}

# ---------- baseline: same commit's UI in Chrome via `gleam webui` ----------
gbin = f"{DESK}/.sidecar/linux-x64/gleam"
wp = subprocess.Popen([gbin, "webui", "--mock-llm", "--data-dir", f"{ROOT}/data-chrome", "--workspace", f"{ROOT}/ws", "--addr", "127.0.0.1:18797"],
                      cwd=os.path.dirname(DESK), env=dict(os.environ, GLEAM_WEBUI_TOKEN=""), stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
time.sleep(2)
with sync_playwright() as pw:
    b = pw.chromium.launch(executable_path="/usr/bin/google-chrome", headless=True)
    pg = b.new_page(viewport={"width": 1440, "height": 900})
    pg.goto("http://127.0.0.1:18797/"); pg.wait_for_selector("#goal-input"); settle(pg); pg.wait_for_timeout(1500)
    pg.screenshot(path=f"{SHOTS}/chrome-webui-home.png")
    R["chrome_version"] = b.version
    b.close()
wp.terminate(); wp.wait()

# ---------- pixel diff: Electron vs Chrome, same commit, same viewport, fresh data ----------
from PIL import Image, ImageChops
a = Image.open(f"{SHOTS}/electron-home.png").convert("RGB"); c = Image.open(f"{SHOTS}/chrome-webui-home.png").convert("RGB")
R["pixel_diff"] = {"sizes": [a.size, c.size]}
if a.size == c.size:
    d = ImageChops.difference(a, c)
    px = sum(1 for p in d.getdata() if max(p) > 0)
    px16 = sum(1 for p in d.getdata() if max(p) > 16)
    R["pixel_diff"].update({"bbox": d.getbbox(), "differing_pixels": px, "differing_pixels_gt16": px16,
                            "total": a.size[0] * a.size[1], "pct": round(100 * px / (a.size[0] * a.size[1]), 3)})
    if d.getbbox():
        m = d.point(lambda v: 255 if v > 0 else 0).convert("L")
        Image.composite(Image.new("RGB", a.size, (255, 0, 0)), a.point(lambda v: v // 3 + 170), m).save(f"{SHOTS}/diff-electron-vs-chrome.png")

print(json.dumps(R, ensure_ascii=False, indent=1))
json.dump(R, open(f"{SHOTS}/verify-results.json", "w"), ensure_ascii=False, indent=1)
