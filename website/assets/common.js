(() => {
  const byId = (id) => document.getElementById(id);

  async function api(path, options = {}) {
    const response = await fetch(path, {
      credentials: "same-origin",
      cache: "no-store",
      ...options,
      headers: {
        "Accept": "application/json",
        ...(options.body ? { "Content-Type": "application/json" } : {}),
        ...(options.headers || {})
      }
    });
    const text = await response.text();
    let payload = {};
    if (text) {
      try {
        payload = JSON.parse(text);
      } catch (_) {
        payload = {};
      }
    }
    if (response.status === 401) {
      location.replace("/login.html");
      throw new Error("登入狀態已失效");
    }
    if (!response.ok) {
      throw new Error(payload?.error?.message || `請求失敗（${response.status}）`);
    }
    return payload;
  }

  function showMessage(message, kind = "success") {
    const toast = byId("globalMessage");
    if (!toast) return;
    toast.textContent = message;
    toast.className = `toast page-toast ${kind}`;
    toast.hidden = false;
    window.clearTimeout(showMessage.timeout);
    showMessage.timeout = window.setTimeout(() => { toast.hidden = true; }, 4800);
  }

  function formatBytes(value) {
    if (!Number.isFinite(value) || value < 0) return "大小未知";
    const units = ["B", "KiB", "MiB", "GiB", "TiB"];
    let current = value;
    let index = 0;
    while (current >= 1024 && index < units.length - 1) {
      current /= 1024;
      index += 1;
    }
    return `${current.toFixed(index === 0 ? 0 : 1)} ${units[index]}`;
  }

  function formatTime(value) {
    if (!value || value.startsWith("0001-")) return "—";
    return new Intl.DateTimeFormat("zh-TW", {
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit"
    }).format(new Date(value));
  }

  async function logout() {
    try {
      await api("/api/logout", { method: "POST", body: JSON.stringify({}) });
    } finally {
      location.replace("/login.html");
    }
  }

  byId("logoutButton")?.addEventListener("click", logout);
  window.LlamaLoader = { api, byId, showMessage, formatBytes, formatTime };
})();
