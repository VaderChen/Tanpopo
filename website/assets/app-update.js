(() => {
  const { api, byId, t, formatBytes } = window.LlamaLoader;
  const activeStates = new Set(["checking", "downloading", "verifying", "preparing", "stopping", "installing", "restarting"]);
  const messages = {
    checking: "正在取得官方更新套件…",
    downloading: "正在自動下載更新…",
    verifying: "下載完成，正在驗證更新套件…",
    preparing: "正在準備安裝更新…",
    stopping: "即將關閉程式並安裝更新…",
    installing: "程式已關閉，正在安裝更新…",
    restarting: "安裝完成，正在重新啟動程式…",
    completed: "更新完成，程式已重新啟動。"
  };
  const pendingKey = "tanpopo.appUpdatePending";
  let state = {}, version = {}, busy = false, timer = 0, disconnectedAt = 0, reloadPending = false;

  function setPending(value) {
    try { if (value) sessionStorage.setItem(pendingKey, "1"); else sessionStorage.removeItem(pendingKey); } catch (_) {}
  }

  function controls() {
    const button = byId("appInstallButton");
    button.disabled = busy || !state.automatic_available || !version.update_available || Boolean(version.check_error);
    button.textContent = t(busy ? "更新進行中…" : "更新並重新啟動");
    byId("checkUpdateButton").disabled = busy;
    const upload = byId("linuxZIPUpdateButton");
    upload.hidden = !state.upload_available || Boolean(state.automatic_available);
    upload.disabled = busy;
  }

  function render(next) {
    state = next;
    const status = byId("appInstallStatus"), progress = byId("appUpdateProgress");
    const phase = String(state.state || "idle");
    const wasBusy = busy;
    busy = activeStates.has(phase);
    controls();
    progress.hidden = !busy;
    status.className = "about-update-summary";
    status.hidden = phase === "idle" && state.automatic_available;
    if (!state.automatic_available && !state.upload_available) {
      status.hidden = false;
      status.textContent = t("自動更新僅適用已安裝的發行版本。");
      return;
    }
    if (phase === "failed") {
      setPending(false);
      status.classList.add("error");
      status.textContent = `${t("更新失敗")}：${t(state.message || "")}`;
      return;
    }
    if (phase === "completed") {
      status.textContent = t(messages.completed);
      setPending(false);
      if (wasBusy && !reloadPending) {
        reloadPending = true;
        window.setTimeout(() => location.reload(), 1500);
      }
      return;
    }
    if (busy) {
      status.classList.add("updating");
      status.textContent = t(messages[phase] || state.message || "正在準備安裝更新…");
      if (phase === "downloading" && Number(state.total_bytes) > 0) {
        const percent = Math.max(0, Math.min(100, Number(state.downloaded_bytes || 0) * 100 / Number(state.total_bytes)));
        progress.value = percent;
        status.textContent += ` ${percent.toFixed(0)}% · ${formatBytes(state.downloaded_bytes || 0)} / ${formatBytes(state.total_bytes)}`;
      } else {
        progress.removeAttribute("value");
      }
    }
  }

  function schedule() {
    window.clearTimeout(timer);
    timer = window.setTimeout(refresh, 1500);
  }

  async function refresh() {
    try {
      const next = await api("/api/app-update/status");
      disconnectedAt = 0;
      render(next);
      if (busy) schedule();
    } catch (error) {
      const status = byId("appInstallStatus");
      status.hidden = false;
      if (!busy) {
        status.className = "about-update-summary error";
        status.textContent = `${t("更新狀態讀取失敗")}：${error.message}`;
        return;
      }
      if (!disconnectedAt) disconnectedAt = Date.now();
      status.className = "about-update-summary updating";
      status.textContent = t("正在等待服務完成更新並重新啟動…");
      if (Date.now() - disconnectedAt < 20 * 60 * 1000) schedule();
      else {
        status.className = "about-update-summary error";
        status.textContent = t("尚未收到重新啟動確認，請重新整理頁面或查看更新日誌。");
      }
    }
  }

  async function start(file) {
    if (busy) return;
    busy = true;
    setPending(true);
    controls();
    byId("appInstallStatus").hidden = false;
    byId("appInstallStatus").textContent = t("正在取得官方更新套件…");
    try {
      let next;
      if (file) {
        const form = new FormData();
        form.append("update_zip", file, file.name);
        const response = await fetch("/api/app-update/upload", { method: "POST", credentials: "same-origin", body: form });
        next = await response.json();
        if (!response.ok) throw new Error(next?.error?.message || t("更新失敗"));
        next.upload_available = true;
      } else {
        next = await api("/api/app-update/start", { method: "POST", headers: { "X-Tanpopo-Update": "1" } });
      }
      render(next);
      schedule();
    } catch (error) {
      render({ ...state, state: "failed", message: error.message });
    }
  }

  function init() {
    try { busy = sessionStorage.getItem(pendingKey) === "1"; } catch (_) {}
    byId("appInstallButton").addEventListener("click", () => start());
    byId("linuxZIPUpdateButton").addEventListener("click", () => byId("linuxZIPUpdateInput").click());
    byId("linuxZIPUpdateInput").addEventListener("change", (event) => {
      const file = event.currentTarget.files?.[0];
      event.currentTarget.value = "";
      if (file) start(file);
    });
    controls();
    return refresh();
  }

  window.TanpopoUpdates = { init, refresh, setVersion(value) { version = value; controls(); } };
})();
