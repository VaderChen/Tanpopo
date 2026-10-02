const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const test = require("node:test");

function page() {
  const html = fs.readFileSync(path.join(__dirname, "../website/settings.html"), "utf8");
  const elements = new Map([...html.matchAll(/id="([^"]+)"/g)].map((m) => [m[1], {
    hidden: false, disabled: false, listeners: {}, textContent: "", value: 0,
    classList: { add() {} }, addEventListener(name, callback) { this.listeners[name] = callback; },
    removeAttribute(name) { delete this[name]; },
    click() { assert.equal(this.disabled, false); return this.listeners.click(); }
  }]));
  const byId = (id) => { assert.ok(elements.has(id), `HTML 缺少 ${id}`); return elements.get(id); };
  const calls = [], timers = [], stored = new Map();
  let status = { state: "idle", automatic_available: true, upload_available: false }, failure = false, reloads = 0;
  const window = {
    LlamaLoader: { byId, t: (s) => s, formatBytes: (n) => `${n} B`, api: async (url, options) => {
      calls.push({ url, options });
      if (failure) throw Error("network disconnected");
      if (url.endsWith("/start")) status = { ...status, state: "checking" };
      return status;
    } },
    setTimeout: (callback) => { timers.push(callback); return timers.length; }, clearTimeout() {}
  };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "../website/assets/app-update.js"), "utf8"), {
    window, Date, sessionStorage: { getItem: (k) => stored.get(k), setItem: (k, v) => stored.set(k, v), removeItem: (k) => stored.delete(k) },
    location: { reload() { reloads++; } }
  });
  return { feature: window.TanpopoUpdates, byId, calls, timers, stored, reloads: () => reloads,
    status(value) { status = { ...status, ...value }; }, networkFailure(value) { failure = value; } };
}

test("一鍵更新、自動下載進度、斷線等待與完成後重新載入", async () => {
  const ui = page();
  await ui.feature.init();
  assert.equal(ui.byId("appInstallButton").disabled, true);
  ui.feature.setVersion({ update_available: true });
  assert.equal(ui.byId("appInstallButton").disabled, false);
  await ui.byId("appInstallButton").click();
  assert.equal(ui.byId("appInstallButton").disabled, true);
  const request = ui.calls.find((c) => c.url.endsWith("/start"));
  assert.equal(request.options.method, "POST");
  assert.equal(request.options.headers["X-Tanpopo-Update"], "1");
  ui.status({ state: "downloading", downloaded_bytes: 40, total_bytes: 100 });
  await ui.feature.refresh();
  assert.equal(ui.byId("appUpdateProgress").value, 40);
  assert.match(ui.byId("appInstallStatus").textContent, /40%/);
  ui.status({ state: "verifying" });
  await ui.feature.refresh();
  assert.equal(ui.byId("appUpdateProgress").value, undefined);
  ui.networkFailure(true);
  await ui.feature.refresh();
  assert.match(ui.byId("appInstallStatus").textContent, /等待服務/);
  assert.equal(ui.byId("appInstallButton").disabled, true);
  ui.networkFailure(false);
  ui.status({ state: "completed" });
  await ui.feature.refresh();
  assert.equal(ui.stored.size, 0);
  assert.equal(ui.byId("appUpdateProgress").hidden, true);
  ui.timers.at(-1)();
  assert.equal(ui.reloads(), 1);
  await ui.feature.refresh();
  assert.equal(ui.reloads(), 1, "已完成狀態不能產生重新載入循環");
});

test("更新失敗保留訊息並可重試，開發版及無新版不能啟動更新", async () => {
  const ui = page();
  await ui.feature.init();
  ui.feature.setVersion({ update_available: true });
  await ui.byId("appInstallButton").click();
  ui.status({ state: "failed", message: "SHA-256 不符" });
  await ui.feature.refresh();
  assert.match(ui.byId("appInstallStatus").textContent, /SHA-256/);
  assert.equal(ui.byId("appInstallButton").disabled, false);
  await ui.byId("appInstallButton").click();
  assert.equal(ui.calls.filter((c) => c.url.endsWith("/start")).length, 2);
  ui.status({ state: "unavailable", automatic_available: false });
  await ui.feature.refresh();
  assert.equal(ui.byId("appInstallButton").disabled, true);
  assert.match(ui.byId("appInstallStatus").textContent, /已安裝的發行版本/);
});

test("直接開啟已完成更新的頁面不重複下載或重啟", async () => {
  const ui = page();
  ui.status({ state: "completed" });
  await ui.feature.init();
  ui.feature.setVersion({ update_available: false });
  assert.equal(ui.timers.length, 0);
  assert.equal(ui.calls.some((c) => c.url.endsWith("/start")), false);
  assert.equal(ui.byId("appInstallButton").disabled, true);
});
