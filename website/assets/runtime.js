(() => {
  const { api, byId, showMessage, formatBytes, formatTime } = window.LlamaLoader;
  const LLAMA_RUNTIME = "llama-server";
  const MLX_RUNTIME = "mlx-server";
  const state = { settings: null, models: [], mainModels: [], mmprojModels: [], commands: [], runtime: null };

  function selectedCommand() {
    return state.commands.find((command) => command.id === byId("commandSelect").value) || null;
  }

  function selectedRuntime() {
    return byId("runtimeSelect").value || LLAMA_RUNTIME;
  }

  function isMMProjModel(model) {
    return /(^|[\/_.-])mmproj([\/_.-]|$)/i.test(model.path);
  }

  async function loadSettings() {
    state.settings = await api("/api/settings");
  }

  async function loadModels(preserveSelection = true) {
    const runtimeName = selectedRuntime();
    const modelSelect = byId("modelSelect");
    const previousModel = preserveSelection ? modelSelect.value : "";
    const previousMMProj = preserveSelection ? byId("mmprojSelect").value : "";
    const payload = await api(`/api/models?runtime=${encodeURIComponent(runtimeName)}`);
    state.models = payload.models || [];
    state.mmprojModels = runtimeName === LLAMA_RUNTIME ? state.models.filter(isMMProjModel) : [];
    state.mainModels = runtimeName === LLAMA_RUNTIME
      ? state.models.filter((model) => !isMMProjModel(model))
      : state.models;

    modelSelect.replaceChildren();
    if (!state.mainModels.length) {
      const emptyOption = document.createElement("option");
      emptyOption.value = "";
      emptyOption.textContent = runtimeName === MLX_RUNTIME
        ? "尚無支援的 MLX 模型"
        : "尚無支援的 GGUF 模型";
      modelSelect.append(emptyOption);
    }
    state.mainModels.forEach((model) => {
      const option = document.createElement("option");
      option.value = model.path;
      option.textContent = model.path;
      modelSelect.append(option);
    });
    if (previousModel && state.mainModels.some((model) => model.path === previousModel)) {
      modelSelect.value = previousModel;
    }

    const mmprojSelect = byId("mmprojSelect");
    mmprojSelect.replaceChildren();
    const emptyOption = document.createElement("option");
    emptyOption.value = "";
    emptyOption.textContent = "不使用 mmproj";
    mmprojSelect.append(emptyOption);
    state.mmprojModels.forEach((model) => {
      const option = document.createElement("option");
      option.value = model.path;
      option.textContent = model.path;
      mmprojSelect.append(option);
    });
    if (previousMMProj && state.mmprojModels.some((model) => model.path === previousMMProj)) {
      mmprojSelect.value = previousMMProj;
    }

    const isMLX = runtimeName === MLX_RUNTIME;
    byId("modelFieldLabel").textContent = isMLX
      ? "選擇 mlx-server 支援的 MLX 模型"
      : "選擇 llama-server 支援的 GGUF 模型";
    byId("mmprojField").hidden = isMLX;
    byId("mmprojMeta").hidden = isMLX;
    renderModelMeta();
    renderMMProjMeta();
    renderRuntime(state.runtime);
  }

  async function loadCommands(preserveSelection = true) {
    const select = byId("commandSelect");
    const previous = preserveSelection ? select.value : "";
    const payload = await api("/api/startup-commands");
    state.commands = payload.commands || [];
    if (state.runtime?.running) {
      byId("runtimeSelect").value = state.runtime.runtime || LLAMA_RUNTIME;
    }
    renderCommandOptions(previous);
    renderRuntime(state.runtime);
  }

  function renderCommandOptions(preferredID = "") {
    const select = byId("commandSelect");
    const runtimeName = selectedRuntime();
    const commands = state.commands.filter((command) => (command.runtime || LLAMA_RUNTIME) === runtimeName);
    select.replaceChildren();
    commands.forEach((command) => {
      const option = document.createElement("option");
      option.value = command.id;
      option.textContent = command.name;
      select.append(option);
    });
    const runningID = state.runtime?.running ? state.runtime.startup_command_id : "";
    const preferred = runningID || preferredID;
    if (preferred && commands.some((command) => command.id === preferred)) {
      select.value = preferred;
    }
  }

  function renderModelMeta() {
    const value = byId("modelSelect").value;
    const model = state.mainModels.find((item) => item.path === value);
    if (model) {
      byId("modelMeta").textContent = `${formatBytes(model.size)} · 修改於 ${formatTime(model.modified_at)}`;
      return;
    }
    if (selectedRuntime() === MLX_RUNTIME) {
      byId("modelMeta").textContent = `MLX 模型目錄：${state.settings?.mlx_model_directory || "尚未設定"}`;
      return;
    }
    byId("modelMeta").textContent = `GGUF 模型目錄：${state.settings?.model_directory || "尚未設定"}`;
  }

  function renderMMProjMeta() {
    const mmproj = state.mmprojModels.find((item) => item.path === byId("mmprojSelect").value);
    byId("mmprojMeta").textContent = mmproj
      ? `${formatBytes(mmproj.size)} · 修改於 ${formatTime(mmproj.modified_at)}`
      : (state.mmprojModels.length ? "未掛載 mmproj" : "模型目錄內尚無檔名含 mmproj 的 GGUF");
  }

  async function loadRuntime() {
    state.runtime = await api("/api/runtime/status");
    renderRuntime(state.runtime);
  }

  function renderRuntime(status) {
    if (!status) return;
    const running = Boolean(status.running);
    const runtimeName = running ? (status.runtime || LLAMA_RUNTIME) : selectedRuntime();
    const runtimeLabel = runtimeName === MLX_RUNTIME ? "mlx-server" : "llama-server";
    byId("statusDot").classList.toggle("online", running);
    byId("statusLabel").textContent = running ? `${runtimeLabel} 執行中` : `${runtimeLabel} 已停止`;
    byId("statusDetail").textContent = running
      ? `啟動時間 ${formatTime(status.started_at)} · ${status.startup_command_name || "未命名參數"}`
      : (status.last_error ? `上次錯誤：${status.last_error}` : "選擇模型後即可啟動");
    byId("runningRuntime").textContent = running ? runtimeLabel : "—";
    byId("runningModel").textContent = status.model || "—";
    byId("runningMMProj").textContent = status.mmproj || "—";
    byId("runningMMProjRow").hidden = runtimeName === MLX_RUNTIME;
    byId("runningDraft").textContent = status.draft_model || "—";
    byId("runningDraftRow").hidden = !running || !status.draft_model;
    byId("runningPID").textContent = status.pid || "—";
    byId("logTitle").textContent = `${runtimeLabel} 日誌`;
    const link = byId("serverURL");
    link.textContent = status.url || "—";
    link.href = status.url || "#";
    if (running && state.commands.some((command) => command.id === status.startup_command_id)) {
      byId("runtimeSelect").value = status.runtime || LLAMA_RUNTIME;
      renderCommandOptions(status.startup_command_id);
      byId("commandSelect").value = status.startup_command_id;
    }
    if (running) {
      byId("modelSelect").value = status.model || "";
      if (state.mmprojModels.some((model) => model.path === status.mmproj)) {
        byId("mmprojSelect").value = status.mmproj;
      }
      renderModelMeta();
      renderMMProjMeta();
    }
    const modelReady = byId("modelSelect").value !== "";
    const commandReady = byId("commandSelect").options.length > 0;
    byId("runtimeSelect").disabled = running;
    byId("commandSelect").disabled = running || !commandReady;
    byId("modelSelect").disabled = running || !commandReady || !state.mainModels.length;
    byId("mmprojSelect").disabled = running || selectedRuntime() === MLX_RUNTIME || !state.mmprojModels.length;
    byId("startButton").disabled = running || !modelReady || !commandReady;
    byId("stopButton").disabled = !running;
  }

  async function loadLogs() {
    const payload = await api("/api/runtime/logs");
    const output = byId("logOutput");
    const shouldFollow = output.scrollHeight - output.scrollTop - output.clientHeight < 40;
    output.textContent = payload.logs || "尚無日誌。";
    if (shouldFollow) output.scrollTop = output.scrollHeight;
  }

  async function refreshRuntime() {
    try {
      await Promise.all([loadRuntime(), loadLogs()]);
    } catch (error) {
      if (!String(error.message).includes("登入狀態")) showMessage(error.message, "error");
    }
  }

  byId("startButton").addEventListener("click", async () => {
    const button = byId("startButton");
    const runtimeName = selectedRuntime();
    button.disabled = true;
    try {
      await api("/api/runtime/start", {
        method: "POST",
        body: JSON.stringify({
          model: byId("modelSelect").value,
          mmproj: runtimeName === MLX_RUNTIME ? "" : byId("mmprojSelect").value,
          startup_command_id: byId("commandSelect").value
        })
      });
      showMessage(`${runtimeName} 已啟動`);
      await refreshRuntime();
    } catch (error) {
      showMessage(error.message, "error");
      await loadRuntime();
    } finally {
      renderRuntime(state.runtime);
    }
  });

  byId("stopButton").addEventListener("click", async () => {
    const button = byId("stopButton");
    button.disabled = true;
    try {
      await api("/api/runtime/stop", { method: "POST", body: JSON.stringify({}) });
      showMessage("模型服務已停止");
      await refreshRuntime();
    } catch (error) {
      showMessage(error.message, "error");
    }
  });

  byId("clearLogsButton").addEventListener("click", async () => {
    const button = byId("clearLogsButton");
    button.disabled = true;
    try {
      await api("/api/runtime/logs", { method: "DELETE" });
      byId("logOutput").textContent = "尚無日誌。";
      showMessage("模型服務日誌已清除");
    } catch (error) {
      showMessage(error.message, "error");
    } finally {
      button.disabled = false;
    }
  });

  byId("refreshButton").addEventListener("click", async () => {
    try {
      await loadSettings();
      await loadRuntime();
      await loadCommands(true);
      await Promise.all([loadModels(true), loadLogs()]);
      showMessage("資料已更新");
    } catch (error) {
      showMessage(error.message, "error");
    }
  });
  byId("commandSelect").addEventListener("change", () => {
    loadModels(false).catch((error) => showMessage(error.message, "error"));
  });
  byId("runtimeSelect").addEventListener("change", () => {
    renderCommandOptions();
    loadModels(false).catch((error) => showMessage(error.message, "error"));
  });
  byId("modelSelect").addEventListener("change", () => {
    renderModelMeta();
    renderRuntime(state.runtime);
  });
  byId("mmprojSelect").addEventListener("change", renderMMProjMeta);

  async function initialize() {
    try {
      await loadSettings();
      await loadRuntime();
      await loadCommands(false);
      await Promise.all([loadModels(false), loadLogs()]);
      window.setInterval(refreshRuntime, 2500);
    } catch (error) {
      showMessage(error.message, "error");
    }
  }

  initialize();
})();
