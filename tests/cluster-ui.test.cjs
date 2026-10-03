const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const vm = require("node:vm");
const test = require("node:test");

// 使用真實 HTML 與事件處理器，驗證複選、啟用與 API 失敗後重試的流程。
function page() {
  const html = fs.readFileSync(path.join(__dirname, "../website/main.html"), "utf8");
  class Element {
    constructor() { this.value = ""; this.children = []; this.listeners = {}; this.disabled = false; this.hidden = false; this.dataset = {}; this.attributes = {}; }
    addEventListener(name, callback) { this.listeners[name] = callback; }
    setAttribute(name, value) { this.attributes[name] = value; }
    append(...children) { children.forEach((child) => { child.parent = this; this.children.push(child); }); }
    remove() { this.parent.children = this.parent.children.filter((child) => child !== this); }
    click() { assert.equal(this.disabled, false, "按鈕必須可操作"); return this.listeners.click(); }
    showModal() { this.open = true; }
    close() { this.open = false; this.listeners.close?.(); }
    change(checked) { assert.equal(this.disabled, false); this.checked = checked; this.listeners.change(); }
  }
  const elements = new Map([...html.matchAll(/id="([^"]+)"/g)].map((match) => [match[1], new Element()]));
  const byId = (id) => { assert.ok(elements.has(id), `HTML 缺少 ${id}`); return elements.get(id); };
  const capability = { version: "v1", ring_available: true, managed_parent_stdin: true, max_ring_nodes: 8,
    generic_linear_sharding: true, text_model_types: ["llama", "qwen3", "qwen3_5"] };
  const calls = [], messages = [];
  let failure = "";
  let status = { enabled: false, discovery_port: 10083, interface: "", local: { name: "本機", capabilities: capability, model_sync_version: 2 }, peers: [
    { id: "peer-a", name: "<script>不能執行</script>", ip: "192.168.1.2", port: 10082, capabilities: capability, model_sync_version: 2 },
    { id: "peer-b", name: "Mac B", ip: "192.168.1.3", port: 10082, capabilities: capability, model_sync_version: 2 }
  ] };
  const api = async (url, options = {}) => {
    const body = options.body ? JSON.parse(options.body) : null;
    calls.push({ url, body });
    if (url.endsWith("/start")) {
      if (failure) throw new Error(failure);
      status = { ...status, session: { members: [{ name: "本機" }, ...status.peers], role: "coordinator", phase: "loading", model: body.model } };
    }
    if (url.endsWith("/stop")) status = { ...status, session: null };
    if (url.endsWith("/config")) { status = { ...status, ...body }; return { status }; }
    return status;
  };
  const window = { LlamaLoader: { api, byId, showMessage: (value) => messages.push(value) } };
  vm.runInNewContext(fs.readFileSync(path.join(__dirname, "../website/assets/cluster.js"), "utf8"), {
    window, document: { createElement: () => new Element() }, setInterval: () => 1, clearInterval() {}
  });
  return { byId, calls, messages, feature: window.TanpopoCluster, setStatus(value) { status = value; }, getStatus() { return status; },
    failStart(value) { failure = value; }, checkbox(index) { return byId("clusterPeerList").children[index].children[0]; } };
}

function selectModel(ui) {
  ui.feature.update({ runtime: "mlx-server", model: { path: "Qwen3/model", architecture: "qwen3" }, command: { id: "profile" }, status: { running: false } });
}

test("搜尋免金鑰開啟對話框，複選後一次配對並啟用全部節點", async () => {
  const ui = page();
  await ui.feature.refresh();
  selectModel(ui);
  await ui.byId("clusterSearch").click();
  assert.equal(ui.byId("clusterDialog").open, true);
  assert.deepEqual(ui.calls.find((call) => call.url.endsWith("/config")).body, { enabled: true, discovery_port: 10083, interface: "" });
  assert.equal(ui.byId("clusterStart").disabled, true);
  assert.equal(ui.byId("clusterPeerList").children[0].children[2].children[0].textContent, "<script>不能執行</script>");
  ui.checkbox(0).change(true);
  ui.checkbox(1).change(true);
  assert.match(ui.byId("clusterSelectionCount").textContent, /共 3 台/);
  ui.byId("kvCacheQuantizationToggle").checked = true;
  await ui.byId("clusterStart").click();
  assert.deepEqual(ui.calls.find((call) => call.url.endsWith("/start")).body, {
    peer_ids: ["peer-a", "peer-b"], model: "Qwen3/model", startup_command_id: "profile", kv_cache_quantization_enabled: true, draft_model: ""
  });
  assert.equal(ui.byId("clusterDialog").open, false);
  assert.equal(ui.byId("clusterSearch").disabled, true);
  assert.match(ui.byId("clusterStatus").textContent, /載入模型/);
  await ui.byId("clusterStop").click();
  assert.equal(ui.byId("clusterStop").hidden, true);
  assert.equal(ui.byId("clusterSearch").disabled, false);
});

test("單機服務可由配對切換；版本不符與離線仍移除選取", async () => {
  const ui = page();
  await ui.feature.refresh();
  selectModel(ui);
  await ui.byId("clusterSearch").click();
  await ui.byId("clusterSelectAll").click();
  const checkbox = ui.checkbox(0);
  await ui.feature.refresh();
  assert.equal(ui.checkbox(0), checkbox, "輪詢不可替换聚焦的勾選框");
  assert.equal(checkbox.checked, true);
  const peers = ui.getStatus().peers;
  ui.setStatus({ ...ui.getStatus(), peers: [{ ...peers[0], busy: true }, { ...peers[1], capabilities: { ...peers[1].capabilities, version: "v2" } }] });
  await ui.feature.refresh();
  assert.equal(ui.checkbox(0).disabled, false);
  assert.equal(ui.checkbox(0).checked, true);
  assert.equal(ui.checkbox(1).disabled, true);
  assert.equal(ui.checkbox(1).checked, false);
  assert.equal(ui.byId("clusterStart").disabled, false);
  assert.match(ui.byId("clusterSelectionHint").textContent, /會切換為此模型/);
  ui.setStatus({ ...ui.getStatus(), peers: [{ ...peers[0], busy: false }] });
  await ui.feature.refresh();
  assert.equal(ui.checkbox(0), checkbox);
  assert.equal(checkbox.checked, true);
  assert.equal(ui.byId("clusterStart").disabled, false);
  ui.setStatus({ ...ui.getStatus(), peers: [] });
  await ui.feature.refresh();
  assert.equal(ui.byId("clusterEmpty").hidden, false);
  assert.equal(ui.byId("clusterPeerList").children.length, 0);
  assert.equal(ui.byId("clusterStart").disabled, true);
  assert.match(ui.byId("clusterSelectionCount").textContent, /已選 0/);
});

test("搜尋到的忙碌節點可直接勾選與全選，同時顯示本機、對端及模型的啟動條件", async () => {
  const ui = page();
  ui.setStatus({ ...ui.getStatus(), local: { ...ui.getStatus().local, busy: true },
    peers: ui.getStatus().peers.map(peer => ({ ...peer, busy: true })) });
  await ui.feature.refresh();
  ui.feature.update({ runtime: "mlx-server", model: { path: "models/current", architecture: "not_registered" },
    command: { id: "profile" }, status: { running: true } });
  await ui.byId("clusterSearch").click();
  ui.checkbox(1).change(true);
  assert.equal(ui.checkbox(1).checked, true);
  assert.match(ui.byId("clusterSelectionHint").textContent, /尚未支援所選模型/);
  assert.equal(ui.byId("clusterStart").disabled, true);
  assert.equal(ui.calls.some(call => call.url.endsWith("/start") || call.url.endsWith("/stop")), false);
  await ui.byId("clusterSelectAll").click();
  assert.equal(ui.checkbox(0).checked, true);
  assert.equal(ui.checkbox(1).checked, true);
  await ui.byId("clusterSelectAll").click();
  assert.equal(ui.checkbox(0).checked, false);
  assert.equal(ui.checkbox(1).checked, false);
  ui.checkbox(1).change(true);
  ui.setStatus({ ...ui.getStatus(), local: { ...ui.getStatus().local, busy: false },
    peers: ui.getStatus().peers.map(peer => ({ ...peer, busy: false })) });
  await ui.feature.refresh();
  ui.feature.update({ runtime: "mlx-server", model: { path: "models/current", architecture: "not_registered" },
    command: { id: "profile" }, status: { running: false } });
  assert.equal(ui.byId("clusterStart").disabled, true, "仍需檢查架構");
  ui.feature.update({ runtime: "mlx-server", model: { path: "models/current", architecture: "qwen3_5" },
    command: { id: "profile" }, status: { running: false } });
  assert.equal(ui.checkbox(1).checked, true);
  assert.equal(ui.byId("clusterStart").disabled, false, "相容性依架構判斷，不依模型目錄名稱");
  ui.setStatus({ ...ui.getStatus(), local: { ...ui.getStatus().local, capabilities: {
    ...ui.getStatus().local.capabilities, text_model_types: ["future_model"] } } });
  await ui.feature.refresh();
  assert.equal(ui.byId("clusterStart").disabled, true);
  ui.feature.update({ runtime: "mlx-server", model: { path: "models/current", architecture: "future_model" },
    command: { id: "profile" }, status: { running: false } });
  assert.equal(ui.byId("clusterStart").disabled, false, "新架構以 Runtime 公布的能力為準");
});

test("握手失敗留在對話框顯示原因，可保留選取後重試", async () => {
  const ui = page();
  await ui.feature.refresh();
  selectModel(ui);
  await ui.byId("clusterSearch").click();
  ui.checkbox(1).change(true);
  ui.failStart("Mac B：模型設定不一致");
  await ui.byId("clusterStart").click();
  assert.equal(ui.byId("clusterDialog").open, true);
  assert.equal(ui.byId("clusterDialogError").hidden, false);
  assert.match(ui.byId("clusterDialogError").textContent, /模型設定不一致/);
  assert.equal(ui.checkbox(1).checked, true);
  assert.equal(ui.byId("clusterStart").disabled, false);
  ui.failStart("");
  await ui.byId("clusterStart").click();
  assert.equal(ui.byId("clusterDialog").open, false);
});

test("被另一台 Server 邀請時關閉選取清單，顯示工作節點狀態", async () => {
  const ui = page();
  await ui.feature.refresh();
  await ui.byId("clusterSearch").click();
  ui.setStatus({ ...ui.getStatus(), session: { role: "worker", phase: "prepared", model: "Qwen3/model", members: [{ name: "對端" }, { name: "本機" }] } });
  await ui.feature.refresh();
  assert.equal(ui.byId("clusterDialog").open, false);
  assert.match(ui.byId("clusterStatus").textContent, /工作節點/);
  assert.equal(ui.byId("clusterStop").disabled, false);
});

test("模型下載顯示各節點進度且仍可停止，舊版 Server 不可被邀請", async () => {
	const ui = page();
	ui.setStatus({ ...ui.getStatus(), session: { role: "coordinator", phase: "synchronizing", model: "模型", members: [{ name: "本機" }, { name: "Mac B" }],
		preparation: [{ name: "Mac B", phase: "downloading", bytes_done: 1048576, bytes_total: 2097152 }] } });
	await ui.feature.refresh();
	assert.equal(ui.byId("clusterPreparation").hidden, false);
	assert.match(ui.byId("clusterPreparation").textContent, /Mac B：下載模型 · 50%/);
	assert.equal(ui.byId("clusterStop").disabled, false);
	await ui.byId("clusterStop").click();
	assert.equal(ui.byId("clusterPreparation").hidden, true);
	for (const version of [0, 1]) {
		ui.setStatus({ ...ui.getStatus(), peers: ui.getStatus().peers.map(peer => ({ ...peer, model_sync_version: version })) });
		await ui.feature.refresh();
		assert.equal(ui.checkbox(0).disabled, true);
		assert.equal(ui.byId("clusterPeerList").children[0].children[3].textContent, "需更新 Tanpopo");
	}
});
