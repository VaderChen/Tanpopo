(() => {
  const { api, byId, showMessage } = window.LlamaLoader;
  let cluster = null;
  let selection = {};
  let activity = "";
  let refreshing = false;
  let initialized = false;
  let epoch = 0;
  let poll = null;
  let connectionError = "";
  const selected = new Set();
  const rows = new Map();
  const dialog = byId("clusterDialog");
  const phases = { preparing: "交握配對中", prepared: "等待主節點", armed: "等待啟動", starting: "啟動節點中", loading: "載入模型", running: "推論就緒" };

  function peerIssue(peer) {
    if (!peer.capabilities?.ring_available || !peer.capabilities?.managed_parent_stdin || (peer.capabilities?.max_ring_nodes || 0) < 2) return "需更新 Runtime";
    if (peer.capabilities.version !== cluster?.local?.capabilities?.version) return "Runtime 版本不同";
    return "";
  }

  function modelIssue() {
    if (selection.calibrating) return "請先等待效能校準完成。";
    if (selection.runtime !== "mlx-server" || !selection.model || String(selection.model.path).startsWith("gguf:") || !selection.command) {
      return "請先在上方選擇 MLX 文字模型與啟動參數。";
    }
    // 使用模型清單提供的架構，不從資料夾名稱猜測；啟動時仍由 Server 完整核對。
    const capability = cluster?.local?.capabilities;
    if (!capability?.generic_linear_sharding || !capability.text_model_types?.length) {
      return "請更新原生 Runtime，以取得通用 TCP Ring 的模型支援資訊。";
    }
    if (selection.model.architecture && !capability.text_model_types.includes(selection.model.architecture)) {
      return "目前 Runtime 尚未支援所選模型的文字分散推論；請更新 Runtime 或更換模型。";
    }
    const profile = selection.command;
    if (profile.runtime_variant || profile.draft_model || (profile.extra_args || []).some((arg) => /^--(distributed-|mtp-|dflash-|mmproj|model-type)/.test(arg))) {
      return "請使用標準 MLX 啟動參數，移除 Draft 與手動分散式設定。";
    }
    return "";
  }

  function nodeLimit() { return Math.min(8, cluster?.local?.capabilities?.max_ring_nodes || 2); }

  function startIssue(peers) {
    if (connectionError) return connectionError;
    const issues = [];
    const busyNames = [];
    if (selection.status?.running || cluster?.local?.busy) busyNames.push("本機");
    for (const peer of peers) {
      if (selected.has(peer.id) && peer.busy) busyNames.push(peer.name || peer.ip);
    }
    if (busyNames.length) issues.push(`${busyNames.join("、")} 使用中；請先在各台 Server 停止目前服務。`);
    const model = modelIssue();
    if (model) issues.push(model);
    if (peers.some((peer) => selected.has(peer.id) && peer.capabilities.max_ring_nodes < selected.size + 1)) {
      issues.push("部分節點不支援此群組大小，請更新 Runtime。");
    }
    return issues.join("\n");
  }

  function createRow(peer) {
    const label = document.createElement("label");
    label.className = "cluster-peer-row";
    const checkbox = document.createElement("input");
    checkbox.type = "checkbox";
    checkbox.value = peer.id;
    const icon = document.createElement("span");
    icon.className = "cluster-peer-icon";
    icon.textContent = "↔";
    icon.setAttribute("aria-hidden", "true");
    const copy = document.createElement("span");
    copy.className = "cluster-peer-copy";
    const name = document.createElement("strong");
    const address = document.createElement("span");
    const hint = document.createElement("span");
    copy.append(name, address, hint);
    const badge = document.createElement("span");
    badge.className = "cluster-node-tag";
    label.append(checkbox, icon, copy, badge);
    checkbox.addEventListener("change", () => {
      if (checkbox.checked) selected.add(peer.id); else selected.delete(peer.id);
      byId("clusterDialogError").hidden = true;
      render();
    });
    return { label, checkbox, name, address, hint, badge };
  }

  function renderPeers(peers) {
    const ids = new Set(peers.map((peer) => peer.id));
    for (const [id, row] of rows) {
      if (!ids.has(id) && activity !== "starting") { row.label.remove(); rows.delete(id); selected.delete(id); }
    }
    for (const peer of peers) {
      let row = rows.get(peer.id);
      if (!row) {
        row = createRow(peer);
        rows.set(peer.id, row);
        byId("clusterPeerList").append(row.label);
      }
      const issue = peerIssue(peer);
      if (issue && activity !== "starting") selected.delete(peer.id);
      row.checkbox.checked = selected.has(peer.id);
      row.checkbox.disabled = !!activity || !!cluster?.session || !!issue || (!row.checkbox.checked && selected.size >= nodeLimit() - 1);
      row.name.textContent = peer.name || "未命名 Server";
      row.address.textContent = `${peer.ip}:${peer.port} · ${peer.platform || "Mac"}`;
      row.hint.hidden = !peer.busy || !!issue;
      row.hint.textContent = "可先選取；配對前請在此節點停止目前服務。";
      row.badge.textContent = issue || (peer.busy ? "使用中" : row.checkbox.checked ? "已選取" : "可連線");
      row.label.dataset.state = issue ? "unavailable" : row.checkbox.checked ? "selected" : "available";
      row.checkbox.setAttribute("aria-label", `${peer.name || "Server"}，${peer.ip}，${issue || (peer.busy ? "使用中，可先選取" : "可連線")}`);
    }
  }

  function render() {
    const peers = cluster?.peers || [];
    const active = cluster?.session;
    // 對端邀請這台 Server 成為 worker 時，回到卡片顯示正確角色與載入狀態。
    if (dialog.open && active?.role === "worker" && !activity) { selected.clear(); dialog.close(); }
    renderPeers(peers);
    const phase = phases[active?.phase] || active?.phase;
    byId("clusterStatus").textContent = connectionError ? "無法取得狀態" : active
      ? `${active.role === "worker" ? "工作節點" : "主節點"} · ${phase}`
      : cluster?.enabled ? (peers.length ? `找到 ${peers.length} 個節點` : "探索已開啟") : "尚未啟用";
    byId("clusterStatus").dataset.state = connectionError || cluster?.last_error ? "error"
      : active?.phase === "running" || (cluster?.enabled && peers.length) ? "ready" : cluster?.enabled ? "searching" : "idle";
    byId("clusterConnectionTitle").textContent = active ? `${active.members.length} 台 Mac 已組成叢集` : "尚未加入叢集";
    byId("clusterConnectionSubtitle").textContent = active ? active.members.map((member) => member.name).join(" · ")
      : cluster?.enabled ? "探索已開啟，這台 Server 也能被區網中的節點找到。" : "搜尋區網節點，勾選後即可配對與啟用。";
    byId("clusterSearch").disabled = !!activity || !!active;
    byId("clusterStop").hidden = !active;
    byId("clusterStop").disabled = !!activity || !active;
    byId("clusterDisable").hidden = !cluster?.enabled;
    for (const id of ["clusterApply", "clusterDisable", "clusterInterface", "clusterPort"]) byId(id).disabled = !!activity || !!active;
    const detail = connectionError || cluster?.last_error;
    byId("clusterDetail").textContent = detail || (active
      ? `${active.model} · ${active.role === "worker" ? "已加入運算，請至主節點進行對話。" : active.phase === "running" ? "叢集已就緒，可以開始對話。" : "正在準備模型，載入完成後即可對話。"}`
      : "不需輸入金鑰。各台 Server 開啟探索後，即可在清單中選取。");
    byId("clusterDetail").dataset.state = detail ? "error" : "normal";

    byId("clusterLocalName").textContent = cluster?.local?.name || "這台 Mac";
    byId("clusterDiscoveryState").textContent = connectionError ? "探索暫時無法更新" : activity === "starting" ? (phase || "正在核對模型與節點…")
      : `探索${cluster?.enabled ? "中" : "準備中"} · 已找到 ${peers.length} 個節點`;
    byId("clusterEmpty").hidden = peers.length > 0;
    byId("clusterSelectedModel").textContent = selection.model?.path || "尚未選擇模型";
    const eligible = peers.filter((peer) => !peerIssue(peer));
    const allSelected = eligible.length > 0 && eligible.slice(0, nodeLimit() - 1).every((peer) => selected.has(peer.id));
    byId("clusterSelectAll").textContent = allSelected ? "清除選取" : "全選節點";
    byId("clusterSelectAll").disabled = !!activity || !!active || !eligible.length;
    byId("clusterRescan").disabled = !!activity || !!active || refreshing;
    byId("clusterSelectionCount").textContent = `已選 ${selected.size} 個節點${selected.size ? ` · 共 ${selected.size + 1} 台 Mac` : ""}`;
    const issue = startIssue(peers);
    byId("clusterSelectionHint").textContent = activity === "starting" ? "正在交握與啟動，請稍候…"
      : issue || (!selected.size ? "請至少選擇一個節點。" : "啟用後，從這台 Mac 進行對話。");
    byId("clusterStart").disabled = !!activity || !!active || !cluster?.enabled || !!issue || !selected.size;
    byId("clusterStart").textContent = activity === "starting" ? "交握配對中…" : "配對並啟用";
    for (const id of ["clusterDialogClose", "clusterCancel"]) byId(id).disabled = !!activity;
    dialog.setAttribute("aria-busy", String(!!activity));
  }

  async function refresh() {
    if (refreshing) return;
    refreshing = true;
    const current = epoch;
    try {
      const result = await api("/api/cluster/status");
      if (current !== epoch) return;
      cluster = result;
      connectionError = "";
      if (!initialized) {
        byId("clusterPort").value = cluster.discovery_port;
        byId("clusterInterface").value = cluster.interface;
        initialized = true;
      }
    } catch (error) { if (current === epoch) connectionError = error.message; }
    finally { refreshing = false; render(); }
  }

  async function action(name, operation) {
    if (activity) return;
    activity = name;
    epoch++;
    byId("clusterDialogError").hidden = true;
    render();
    try { await operation(); }
    catch (error) {
      byId("clusterDialogError").textContent = error.message;
      byId("clusterDialogError").hidden = false;
      if (!dialog.open) showMessage(error.message, "error");
    } finally { epoch++; activity = ""; await refresh(); render(); }
  }

  async function configure(enabled) {
    const port = Number(byId("clusterPort").value);
    if (!Number.isInteger(port) || port < 1024 || port > 65535) throw new Error("UDP 探索埠必須介於 1024–65535。");
    const result = await api("/api/cluster/config", { method: "PUT", body: JSON.stringify({
      enabled, discovery_port: port, interface: byId("clusterInterface").value.trim()
    }) });
    cluster = result.status;
    connectionError = "";
  }

  byId("clusterSearch").addEventListener("click", () => {
    byId("clusterDialogError").hidden = true;
    if (!dialog.open) dialog.showModal();
    if (!poll) poll = setInterval(refresh, 2000);
    return action("searching", async () => {
      if (!cluster?.enabled) await configure(true);
    });
  });
  byId("clusterRescan").addEventListener("click", () => action("searching", async () => {
    if (!cluster?.enabled) await configure(true);
  }));
  byId("clusterApply").addEventListener("click", () => action("configuring", async () => {
    await configure(!!cluster?.enabled);
    showMessage("已儲存網路設定");
  }));
  byId("clusterDisable").addEventListener("click", () => action("configuring", async () => {
    await configure(false);
    selected.clear();
    showMessage("已關閉探索");
  }));
  byId("clusterSelectAll").addEventListener("click", () => {
    const eligible = (cluster?.peers || []).filter((peer) => !peerIssue(peer)).slice(0, nodeLimit() - 1);
    if (eligible.every((peer) => selected.has(peer.id))) selected.clear(); else { selected.clear(); eligible.forEach((peer) => selected.add(peer.id)); }
    render();
  });
  byId("clusterStart").addEventListener("click", () => action("starting", async () => {
    const issue = startIssue(cluster?.peers || []);
    if (issue) throw new Error(issue);
    // 送出前固定選取快照，探索清單更新不改變這次要加入的成員。
    const ids = Array.from(selected);
    cluster = await api("/api/cluster/start", { method: "POST", body: JSON.stringify({
      peer_ids: ids, model: selection.model.path, startup_command_id: selection.command.id,
      kv_cache_quantization_enabled: byId("kvCacheQuantizationToggle").checked
    }) });
    selected.clear();
    dialog.close();
    showMessage(`${ids.length + 1} 台節點配對完成，正在載入模型`);
  }));
  byId("clusterStop").addEventListener("click", () => action("stopping", async () => {
    cluster = await api("/api/cluster/stop", { method: "POST", body: "{}" });
    showMessage("已停止整組叢集");
  }));
  for (const id of ["clusterDialogClose", "clusterCancel"]) byId(id).addEventListener("click", () => dialog.close());
  dialog.addEventListener("cancel", (event) => { if (activity) event.preventDefault(); });
  dialog.addEventListener("close", () => { clearInterval(poll); poll = null; });
  window.TanpopoCluster = { refresh, update(value) { selection = value; render(); } };
})();
