import json
from playwright.sync_api import sync_playwright


BASE = "http://127.0.0.1:8891"


with sync_playwright() as playwright:
    browser = playwright.chromium.launch(headless=True)
    page = browser.new_page(viewport={"width": 1280, "height": 800})
    submitted = []

    def api(route):
        path = route.request.url.split(BASE, 1)[-1]
        if path == "/api/goals" and route.request.method == "POST":
            submitted.append(json.loads(route.request.post_data or "{}"))
            route.fulfill(json={"task_id": "keyboard-test", "status": "running"})
        elif path.startswith("/api/skills"):
            route.fulfill(json={"skills": [{"name": "周报整理", "description": "整理周报"}]})
        elif path.startswith("/api/workspace"):
            route.fulfill(json={"workspace": "D:/workspace", "recents": []})
        elif path.startswith("/api/tools/call"):
            route.fulfill(json={"output": []})
        elif path.startswith("/api/memory"):
            route.fulfill(json={"hits": []})
        elif path.startswith("/api/info"):
            route.fulfill(json={"name": "gleam", "model": "mock", "tools": 21})
        elif path.startswith("/api/roles"):
            route.fulfill(json={"roles": [{"id": "general", "name": "通用助手"}]})
        elif path.startswith("/api/settings"):
            route.fulfill(json={"safety": {"mode": "plan_first"}})
        elif path.startswith("/api/approvals"):
            route.fulfill(json={"approvals": []})
        elif path.startswith("/api/heartbeat"):
            route.fulfill(json={"ok": True})
        else:
            route.continue_()

    page.route("**/api/**", api)
    page.goto(BASE)
    page.wait_for_load_state("networkidle")

    field = page.locator("#goal-input")
    field.fill("@")
    page.wait_for_selector(".mention-item")
    field.press("Enter")
    assert len(submitted) == 0, "Enter selecting a mention must not submit the goal"
    assert page.locator(".ref-chip").count() == 1, "selected mention should become a reference chip"

    field.fill("整理本周工作")
    field.press("Enter")
    page.wait_for_timeout(100)
    assert len(submitted) == 1, "plain Enter should submit exactly once"
    assert submitted[0]["goal"] == "整理本周工作"
    browser.close()

print("composer keyboard flow: ok")
