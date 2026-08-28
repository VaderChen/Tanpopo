(() => {
  const { api, byId, showMessage, formatBytes, formatTime } = window.LlamaLoader;
  const LLAMA_RUNTIME = "llama-server";
  const MLX_RUNTIME = "mlx-server";
  const state = {
    settings: null,
    models: [],
    mainModels: [],
    mmprojModels: [],
    draftModels: [],
    commands: [],
    runtime: null,
    selectionTouched: false
  };

  function selectedCommand() {
    return state.commands.find((command) => command.id === byId("commandSelect").value) || null;
  }

  function selectedRuntime() {
    return byId("runtimeSelect").value || LLAMA_RUNTIME;
  }

  function updateRuntimeSpecificFields() {
    const isMLX = selectedRuntime() === MLX_RUNTIME;
    byId("mmprojField").hidden = isMLX;
    byId("mmprojMeta").hidden = isMLX;
    byId("runningMMProjRow").hidden = isMLX;
  }

  function isMMProjModel(model) {
    return /(^|[\/_.-])mmproj([\/_.-]|$)/i.test(model.path);
  }

  function isDFlashDraftModel(model) {
    return Boolean(model.dflash_draft);
  }

  function selectedModel() {
    return state.mainModels.find((model) => model.path === byId("modelSelect").value) || null;
  }

  function pathDirectory(path) {
    const normalized = String(path || "").replace(/\\/g, "/");
    const separator = normalized.lastIndexOf("/");
    return separator < 0 ? "" : normalized.slice(0, separator);
  }

  function displayModelName(path) {
    const normalized = String(path || "").replace(/\\/g, "/").replace(/\/+$/, "");
    return normalized.split("/").pop() || "—";
  }

  function openAIBaseURL(value) {
    const root = String(value || "").trim().replace(/\/+$/, "");
    if (!root) return "";
    return root.endsWith("/v1") ? root : `${root}/v1`;
  }

  async function copyText(value) {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return;
    }
    const temporary = document.createElement("textarea");
    temporary.value = value;
    temporary.readOnly = true;
    temporary.style.position = "fixed";
    temporary.style.opacity = "0";
    document.body.append(temporary);
    temporary.select();
    const copied = document.execCommand("copy");
    temporary.remove();
    if (!copied) throw new Error("無法存取剪貼簿");
  }

  function modelTokens(path) {
    const ignored = new Set([
      "model", "models", "gguf", "mlx", "draft", "dflash", "dflash1", "dflash2",
      "bf16", "fp16", "f16", "q4", "q5", "q6", "q8", "k", "m", "s"
    ]);
    return String(path || "")
      .toLowerCase()
      .split(/[^a-z0-9]+/)
      .filter((token) => token.length > 1 && !ignored.has(token));
  }

  function preferredDFlashVariant() {
    const command = selectedCommand();
    const description = [command?.id, command?.name, ...(command?.extra_args || [])].join(" ").toLowerCase();
    return /dflash[\s_-]*2|block(?:-size)?[\s_-]*8/.test(description) ? "dflash2" : "";
  }

  function matchedDraftModel() {
    const command = selectedCommand();
    const configuredPath = String(command?.draft_model || "").trim();
    if (configuredPath) {
      return state.draftModels.find((model) => model.path === configuredPath) || null;
    }
    const target = selectedModel();
    if (!target || !state.draftModels.length) return null;
    const targetDirectory = pathDirectory(target.path);
    const targetTokens = new Set(modelTokens(target.path));
    const preferredVariant = preferredDFlashVariant();
    let best = null;
    let bestScore = -1;
    state.draftModels.forEach((draft) => {
      let score = 0;
      const draftDirectory = pathDirectory(draft.path);
      if (targetDirectory && draftDirectory === targetDirectory) score += 100;
      const sharedTokens = modelTokens(draft.path).filter((token) => targetTokens.has(token));
      score += sharedTokens.length * 8;
      if (preferredVariant && draft.dflash_variant === preferredVariant) score += 12;
      if (!preferredVariant && draft.dflash_variant === "dflash1") score += 3;
      if (score > bestScore) {
        best = draft;
        bestScore = score;
      }
    });
    if (bestScore > 0 || state.draftModels.length === 1) return best;
    return null;
  }

  function promptDraftDownload() {
    byId("dflashToggle").checked = false;
    renderDFlashControl(state.runtime);
    showMessage("找不到配對的 DFlash Draft，請先下載後再啟用。", "error");
    if (window.confirm("找不到配對的 DFlash Draft 模型。是否前往「模型下載」？")) {
      window.location.assign("/download.html");
    }
  }

  async function loadSettings() {
    state.settings = await api("/api/settings");
  }

  async function loadModels(preserveSelection = true) {
    const runtimeName = selectedRuntime();
    const modelSelect = byId("modelSelect");
    const previousModel = preserveSelection ? modelSelect.value : "";
    const previousMMProj = preserveSelection ? byId("mmprojSelect").value : "";
    const [payload, draftPayload] = await Promise.all([
      api(`/api/models?runtime=${encodeURIComponent(runtimeName)}`),
      runtimeName === MLX_RUNTIME
        ? api(`/api/models?runtime=${encodeURIComponent(runtimeName)}&role=draft`)
        : Promise.resolve({ models: [] })
    ]);
    state.models = payload.models || [];
    state.mmprojModels = runtimeName === LLAMA_RUNTIME ? state.models.filter(isMMProjModel) : [];
    state.draftModels = runtimeName === LLAMA_RUNTIME
      ? state.models.filter(isDFlashDraftModel)
      : (draftPayload.models || []);
    state.mainModels = runtimeName === LLAMA_RUNTIME
      ? state.models.filter((model) => !isMMProjModel(model) && !isDFlashDraftModel(model))
      : state.models;

    if (!preserveSelection) byId("dflashToggle").checked = false;

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
      option.textContent = displayModelName(model.path);
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
      option.textContent = displayModelName(model.path);
      mmprojSelect.append(option);
    });
    if (previousMMProj && state.mmprojModels.some((model) => model.path === previousMMProj)) {
      mmprojSelect.value = previousMMProj;
    }

    const isMLX = runtimeName === MLX_RUNTIME;
    byId("modelFieldLabel").textContent = isMLX
      ? "選擇 mlx-server 支援的 MLX 模型"
      : "選擇 llama-server 支援的 GGUF 模型";
    updateRuntimeSpecificFields();
    renderModelMeta();
    renderMMProjMeta();
    renderRuntime(state.runtime);
  }

  async function loadCommands(preserveSelection = true) {
    const select = byId("commandSelect");
    const previous = preserveSelection ? select.value : "";
    const payload = await api("/api/startup-commands");
    state.commands = payload.commands || [];
    if (state.runtime?.runtime && (state.runtime.running || !state.selectionTouched)) {
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
    const persistedID = state.runtime?.running || !state.selectionTouched
      ? state.runtime?.startup_command_id
      : "";
    const preferred = persistedID || preferredID;
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
      byId("modelMeta").textContent = "尚無可用的 MLX 模型。";
      return;
    }
    byId("modelMeta").textContent = "尚無可用的 GGUF 模型。";
  }

  function renderMMProjMeta() {
    const mmproj = state.mmprojModels.find((item) => item.path === byId("mmprojSelect").value);
    byId("mmprojMeta").textContent = mmproj
      ? `${formatBytes(mmproj.size)} · 修改於 ${formatTime(mmproj.modified_at)}`
      : (state.mmprojModels.length ? "未掛載 mmproj" : "模型目錄內尚無檔名含 mmproj 的 GGUF");
  }

  function renderDFlashControl(status) {
    const toggle = byId("dflashToggle");
    const meta = byId("dflashMeta");
    const running = Boolean(status?.running);
    if (running) {
      toggle.checked = Boolean(status.draft_model);
      toggle.disabled = true;
      meta.textContent = status.draft_model
        ? `已啟用，Draft：${displayModelName(status.draft_model)}`
        : "本次啟動未使用 DFlash。";
      return;
    }

    const target = selectedModel();
    if (!target) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = "請先選擇 Target 模型。";
      return;
    }
    if (!target.dflash_supported) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = target.architecture
        ? `模型架構 ${target.architecture} 不支援 DFlash。`
        : "無法確認模型架構，DFlash 不可用。";
      return;
    }

    toggle.disabled = false;
    const draft = matchedDraftModel();
    if (toggle.checked && draft) {
      meta.textContent = `已啟用，Draft：${displayModelName(draft.path)}`;
      return;
    }
    const configuredDraft = String(selectedCommand()?.draft_model || "").trim();
    if (configuredDraft && !draft) {
      meta.textContent = `啟動參數指定的 Draft 不存在：${displayModelName(configuredDraft)}`;
      return;
    }
    meta.textContent = draft
      ? `可用 Draft：${displayModelName(draft.path)}（預設關閉）`
      : "模型支援 DFlash，但尚未找到配對的 Draft。";
  }

  async function loadRuntime() {
    state.runtime = await api("/api/runtime/status");
    renderRuntime(state.runtime);
  }

  function renderRuntime(status) {
    if (!status) return;
    const running = Boolean(status.running);
    const restoreSelection = running || !state.selectionTouched;
    if (restoreSelection && status.runtime) {
      byId("runtimeSelect").value = status.runtime;
    }
    const runtimeName = selectedRuntime();
    const runtimeLabel = runtimeName === MLX_RUNTIME ? "mlx-server" : "llama-server";
    byId("statusDot").classList.toggle("online", running);
    byId("statusLabel").textContent = running ? `${runtimeLabel} 執行中` : `${runtimeLabel} 已停止`;
    byId("statusDetail").textContent = running
      ? `啟動時間 ${formatTime(status.started_at)} · ${status.startup_command_name || "未命名參數"}`
      : (status.last_error ? `上次錯誤：${status.last_error}` : "選擇模型後即可啟動");
    byId("runningRuntime").textContent = running ? runtimeLabel : "—";
    byId("runningModel").textContent = status.model ? displayModelName(status.model) : "—";
    byId("runningMMProj").textContent = status.mmproj ? displayModelName(status.mmproj) : "—";
    updateRuntimeSpecificFields();
    byId("runningDraft").textContent = status.draft_model ? displayModelName(status.draft_model) : "—";
    byId("runningDraftRow").hidden = !running || !status.draft_model;
    byId("runningPID").textContent = status.pid || "—";
    byId("logTitle").textContent = `${runtimeLabel} 日誌`;
    const link = byId("serverURL");
    const apiBaseURL = openAIBaseURL(status.url);
    link.textContent = apiBaseURL || "—";
    link.href = apiBaseURL || "#";
    byId("copyServerURL").disabled = !apiBaseURL;
    if (restoreSelection && state.commands.some((command) => command.id === status.startup_command_id)) {
      renderCommandOptions(status.startup_command_id);
      byId("commandSelect").value = status.startup_command_id;
    }
    if (restoreSelection) {
      if (state.mainModels.some((model) => model.path === status.model)) {
        byId("modelSelect").value = status.model;
      }
      if (state.mmprojModels.some((model) => model.path === status.mmproj)) {
        byId("mmprojSelect").value = status.mmproj;
      }
      byId("dflashToggle").checked = Boolean(status.dflash_enabled);
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
    byId("stopButton").disabled = !running && !status.desired_running;
    renderDFlashControl(status);
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
    const dflashEnabled = byId("dflashToggle").checked;
    const draftModel = dflashEnabled ? matchedDraftModel() : null;
    if (dflashEnabled && !draftModel) {
      promptDraftDownload();
      return;
    }
    button.disabled = true;
    try {
      await api("/api/runtime/start", {
        method: "POST",
        body: JSON.stringify({
          model: byId("modelSelect").value,
          mmproj: runtimeName === MLX_RUNTIME ? "" : byId("mmprojSelect").value,
          draft_model: draftModel?.path || "",
          dflash_enabled: dflashEnabled,
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

  byId("copyServerURL").addEventListener("click", async () => {
    const value = openAIBaseURL(state.runtime?.url);
    if (!value) return;
    try {
      await copyText(value);
      showMessage("API Base URL 已複製");
    } catch (_error) {
      showMessage("無法自動複製，請手動選取 API Base URL", "error");
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
    state.selectionTouched = true;
    loadModels(false).catch((error) => showMessage(error.message, "error"));
  });
  byId("runtimeSelect").addEventListener("change", () => {
    state.selectionTouched = true;
    updateRuntimeSpecificFields();
    renderCommandOptions();
    loadModels(false).catch((error) => showMessage(error.message, "error"));
  });
  byId("modelSelect").addEventListener("change", () => {
    state.selectionTouched = true;
    byId("dflashToggle").checked = false;
    renderModelMeta();
    renderRuntime(state.runtime);
  });
  byId("mmprojSelect").addEventListener("change", () => {
    state.selectionTouched = true;
    renderMMProjMeta();
  });
  byId("dflashToggle").addEventListener("change", async () => {
    state.selectionTouched = true;
    const toggle = byId("dflashToggle");
    if (!toggle.checked) {
      renderDFlashControl(state.runtime);
      return;
    }
    toggle.disabled = true;
    try {
      // 勾選當下重新掃描目錄，避免使用已刪除的 Draft 清單快取。
      await loadModels(true);
      if (!matchedDraftModel()) {
        promptDraftDownload();
        return;
      }
      renderDFlashControl(state.runtime);
    } catch (error) {
      toggle.checked = false;
      showMessage(error.message, "error");
      renderDFlashControl(state.runtime);
    }
  });

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
