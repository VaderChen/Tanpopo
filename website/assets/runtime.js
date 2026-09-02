(() => {
  const { api, byId, showMessage, formatBytes, formatTime, t } = window.LlamaLoader;
  const LLAMA_RUNTIME = "llama-server";
  const MLX_RUNTIME = "mlx-server";
  const RUNTIME_TEST_TIMEOUT_MS = 180000;
  const RUNTIME_TEST_REPEAT_TIMEOUT_MS = 540000;
  const RUNTIME_TEST_LONG_TIMEOUT_MS = 600000;
  const RUNTIME_TEST_RETRY_MS = 750;
  const RUNTIME_TEST_REPEAT_COUNT = 3;
  const RUNTIME_TEST_LONG_MIN_TOKENS = 500;
  const RUNTIME_TEST_LONG_MAX_TOKENS = 768;
  const RUNTIME_TEST_PROMPT = "Write one compact English paragraph of approximately 100 words about the benefits of running AI models locally. Do not use headings or lists.";
  const RUNTIME_TEST_LONG_PROMPT = "Write a continuous English essay of at least 900 words about practical ways to run AI models locally. Keep writing until the response limit is reached. Do not use headings or lists, and do not conclude early.";
  const MODEL_NAME_COLLATOR = new Intl.Collator(undefined, {
    numeric: true,
    sensitivity: "base"
  });
  const state = {
    settings: null,
    models: [],
    mainModels: [],
    mmprojModels: [],
    draftModels: [],
    commands: [],
    runtime: null,
    selectionTouched: false,
    testing: false,
    calibrating: false,
    calibration: null,
    modelLoadingDismissed: false
  };
  let modelConversionResolver = null;

  function selectedCommand() {
    return state.commands.find((command) => command.id === byId("commandSelect").value) || null;
  }

  function commandUsesMTP(command = selectedCommand()) {
    return command?.runtime === MLX_RUNTIME && (command.extra_args || []).some((argument) =>
      /^--mtp-(?:draft|block-size)(?:=|$)/.test(String(argument || "").trim())
    );
  }

  function selectedRuntime() {
    return byId("runtimeSelect").value || LLAMA_RUNTIME;
  }

  function localizedMemoryProtectionAction(value) {
    const action = String(value || "");
    const contextMatch = action.match(/^Context 已由 (\d+) 降為 (\d+)$/);
    if (contextMatch) {
      return `${t("Context 已由")} ${Number(contextMatch[1]).toLocaleString()} ${t("降為")} ${Number(contextMatch[2]).toLocaleString()}`;
    }
    return t(action);
  }

  async function filterRuntimeOptionsForPlatform() {
    try {
      let osName = "";
      for (let attempt = 0; attempt < 5 && !osName; attempt += 1) {
        const systemInfo = await api("/api/system/info");
        osName = String(systemInfo?.os_name || "").trim().toLowerCase();
        if (!osName && attempt < 4) {
          await new Promise((resolve) => window.setTimeout(resolve, 250));
        }
      }
      if (!osName || osName === "macos" || osName === "darwin") return;
      byId("runtimeSelect").querySelector(`option[value="${MLX_RUNTIME}"]`)?.remove();
      byId("runtimeSelect").value = LLAMA_RUNTIME;
    } catch (_error) {
      // 平台資訊暫時不可用時保留原有選項；後端仍會拒絕不支援的平台。
    }
  }

  function isMLXGGUFModel(model) {
    return model?.format === "gguf" || String(model?.path || "").startsWith("gguf:");
  }

  function applyModelFeatureDefaults() {
    const model = selectedModel();
    const isMLX = selectedRuntime() === MLX_RUNTIME;
    const isGGUF = isMLXGGUFModel(model);
    const kvEnabled = Boolean(model && state.settings?.default_kv_cache_quantization_enabled);
    byId("fastGGUFToggle").checked = Boolean(
      model && isMLX && isGGUF && state.settings?.default_fast_gguf_enabled !== false
    );
    byId("mmapToggle").checked = Boolean(model && state.settings?.default_mmap_enabled);
    byId("kvCacheQuantizationToggle").checked = kvEnabled;
    byId("dflashToggle").checked = Boolean(
      model
        && isMLX
        && !isGGUF
        && !kvEnabled
        && state.settings?.default_dflash_enabled
        && model.dflash_supported
        && matchedDraftModel()
    );
  }

  function updateRuntimeSpecificFields() {
    const isMLX = selectedRuntime() === MLX_RUNTIME;
    const supportsMMProj = !isMLX;
    byId("mmprojField").hidden = !supportsMMProj;
    byId("mmprojMeta").hidden = !supportsMMProj;
    byId("runningMMProjRow").hidden = !supportsMMProj;
  }

  function isMMProjModel(model) {
    return /(^|[\/_.-])mmproj([\/_.-]|$)/i.test(model.path);
  }

  function isDFlashDraftModel(model) {
    return Boolean(model.dflash_draft || model.draft_kind === "dflash");
  }

  function isMTPDraftModel(model) {
    return Boolean(model.mtp_draft || model.draft_kind === "mtp");
  }

  function selectedModel() {
    return state.mainModels.find(
      (model) => model.path === byId("modelSelect").value
    ) || null;
  }

  function registeredMainModels() {
    return state.mainModels.filter((model) => !model.runtime_untested);
  }

  function untestedMainModels() {
    return state.mainModels.filter((model) => model.runtime_untested);
  }

  function matchedMMProjModel(target = selectedModel()) {
    if (!isMLXGGUFModel(target) || !state.mmprojModels.length) return null;
    const targetDirectory = pathDirectory(target.path);
    const sameDirectory = state.mmprojModels.filter(
      (model) => pathDirectory(model.path) === targetDirectory
    );
    if (sameDirectory.length === 1) return sameDirectory[0];
    if (!targetDirectory && state.mmprojModels.length === 1) return state.mmprojModels[0];
    return null;
  }

  function selectMatchedMMProj(force = false) {
    const select = byId("mmprojSelect");
    const currentIsValid = state.mmprojModels.some((model) => model.path === select.value);
    if (!force && currentIsValid) return;
    select.value = matchedMMProjModel()?.path || "";
  }

  function pathDirectory(path) {
    const normalized = String(path || "").replace(/\\/g, "/");
    const separator = normalized.lastIndexOf("/");
    return separator < 0 ? "" : normalized.slice(0, separator);
  }

  function displayModelName(path) {
    const normalized = String(path || "")
      .replace(/^gguf:/, "")
      .replace(/\\/g, "/")
      .replace(/\/+$/, "");
    return normalized.split("/").pop() || "—";
  }

  function displayModelOptionLabel(path) {
    const normalized = String(path || "")
      .replace(/^gguf:/, "")
      .replace(/\\/g, "/")
      .replace(/^\/+|\/+$/g, "");
    if (!normalized) return "—";
    const parts = normalized.split("/").filter(Boolean);
    const modelName = parts[parts.length - 1] || normalized;
    const directoryName = parts[parts.length - 2] || modelName;
    return `${modelName} (${directoryName})`;
  }

  function compareModelsByDisplayName(left, right) {
    const nameOrder = MODEL_NAME_COLLATOR.compare(
      displayModelName(left?.path),
      displayModelName(right?.path)
    );
    if (nameOrder !== 0) return nameOrder;
    return MODEL_NAME_COLLATOR.compare(String(left?.path || ""), String(right?.path || ""));
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
      "model", "models", "gguf", "mlx", "draft", "dflash", "dflash1", "dflash2", "mtp",
      "bf16", "fp16", "f16", "q4", "q5", "q6", "q8", "k", "m", "s"
    ]);
    return String(path || "")
      .toLowerCase()
      .split(/[^a-z0-9]+/)
      .filter((token) => token.length > 1 && !ignored.has(token));
  }

  function preferredDFlashVariant(command = selectedCommand()) {
    const description = [command?.id, command?.name, ...(command?.extra_args || [])].join(" ").toLowerCase();
    return /dflash[\s_-]*2|block(?:-size)?[\s_-]*8/.test(description) ? "dflash2" : "";
  }

  function matchedDraftModel(target = selectedModel(), command = selectedCommand()) {
    const wantsMTP = commandUsesMTP(command);
    const candidates = state.draftModels.filter(wantsMTP ? isMTPDraftModel : isDFlashDraftModel);
    const configuredPath = String(command?.draft_model || "").trim();
    if (configuredPath) {
      return candidates.find((model) => model.path === configuredPath) || null;
    }
    if (!target || !candidates.length) return null;
    if (wantsMTP && target.mtp_embedded) return null;
    if (wantsMTP ? !target.mtp_supported : !target.dflash_supported) return null;
    const targetDirectory = pathDirectory(target.path);
    const targetTokens = new Set(modelTokens(target.path));
    const preferredVariant = preferredDFlashVariant(command);
    let best = null;
    let bestScore = -1;
    candidates.forEach((draft) => {
      let score = 0;
      const draftDirectory = pathDirectory(draft.path);
      if (targetDirectory && draftDirectory === targetDirectory) score += 100;
      const sharedTokens = modelTokens(draft.path).filter((token) => targetTokens.has(token));
      score += sharedTokens.length * 8;
      if (!wantsMTP && preferredVariant && draft.dflash_variant === preferredVariant) score += 12;
      if (!wantsMTP && !preferredVariant && draft.dflash_variant === "dflash1") score += 3;
      if (score > bestScore) {
        best = draft;
        bestScore = score;
      }
    });
    if (bestScore > 0 || candidates.length === 1) return best;
    return null;
  }

  function promptDraftDownload(mode = "DFlash") {
    byId("dflashToggle").checked = false;
    renderDFlashControl(state.runtime);
    renderKVCacheQuantizationControl(state.runtime);
    const message = mode === "MTP"
      ? t("找不到配對的 MTP Draft，請先下載後再啟用。")
      : t("找不到配對的 DFlash Draft，請先下載後再啟用。");
    const confirmation = mode === "MTP"
      ? t("找不到配對的 MTP Draft 模型。是否前往「模型下載」？")
      : t("找不到配對的 DFlash Draft 模型。是否前往「模型下載」？");
    showMessage(message, "error");
    if (window.confirm(confirmation)) {
      window.location.assign("/download.html");
    }
  }

  async function loadSettings() {
    state.settings = await api("/api/settings");
  }

  async function loadModels(preserveSelection = true) {
    const runtimeName = selectedRuntime();
    // Runtime 一確定就先更新專用欄位，避免等待模型掃描期間短暫顯示錯誤控制項。
    updateRuntimeSpecificFields();
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
    state.mmprojModels = state.models.filter(isMMProjModel).sort(compareModelsByDisplayName);
    state.draftModels = runtimeName === LLAMA_RUNTIME
      ? state.models.filter(isDFlashDraftModel)
      : (draftPayload.models || []);
    state.mainModels = state.models
      .filter((model) => !isMMProjModel(model) && !isDFlashDraftModel(model))
      .sort(compareModelsByDisplayName);
    const registeredModels = registeredMainModels();
    const untestedModels = untestedMainModels();

    if (!preserveSelection) byId("dflashToggle").checked = false;

    modelSelect.replaceChildren();
    if (!state.mainModels.length) {
      const emptyOption = document.createElement("option");
      emptyOption.value = "";
      emptyOption.textContent = runtimeName === MLX_RUNTIME
        ? "尚無可載入的 MLX 或 GGUF 模型"
        : "尚無可載入的 GGUF 模型";
      modelSelect.append(emptyOption);
    }
    if (runtimeName === MLX_RUNTIME) {
      [
        { format: "gguf", label: "GGUF" },
        { format: "mlx", label: "MLX" }
      ].forEach(({ format, label }) => {
        const models = registeredModels.filter((model) => model.format === format);
        if (!models.length) return;
        const group = document.createElement("optgroup");
        group.label = label;
        models.forEach((model) => {
          const option = document.createElement("option");
          option.value = model.path;
          option.textContent = displayModelOptionLabel(model.path);
          group.append(option);
        });
        modelSelect.append(group);
      });
      if (untestedModels.length) {
        const group = document.createElement("optgroup");
        group.label = `尚未測試（${untestedModels.length}）`;
        untestedModels.forEach((model) => {
          const option = document.createElement("option");
          option.value = model.path;
          option.textContent = displayModelOptionLabel(model.path);
          group.append(option);
        });
        modelSelect.append(group);
      }
    } else {
      registeredModels.forEach((model) => {
        const option = document.createElement("option");
        option.value = model.path;
        option.textContent = displayModelOptionLabel(model.path);
        modelSelect.append(option);
      });
    }
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
      option.textContent = displayModelOptionLabel(model.path);
      mmprojSelect.append(option);
    });
    if (previousMMProj && state.mmprojModels.some((model) => model.path === previousMMProj)) {
      mmprojSelect.value = previousMMProj;
    } else {
      selectMatchedMMProj();
    }

    const isMLX = runtimeName === MLX_RUNTIME;
    byId("modelFieldLabel").textContent = isMLX
      ? "選擇 mlx-server 的 MLX 或 GGUF 模型"
      : "選擇 llama-server 支援的 GGUF 模型";
    if (!preserveSelection && !state.runtime?.running) {
      applyModelFeatureDefaults();
    }
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
      option.textContent = t(command.name);
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
    const model = selectedModel();
    const registeredCount = registeredMainModels().length;
    const untestedCount = untestedMainModels().length;
    const testSummary = `Runtime 已登錄 ${registeredCount} 個 · 尚未測試 ${untestedCount} 個`;
    if (model) {
      const untestedHelp = model.runtime_untested
        ? "；此模型尚未測試，啟動時會由 mlx-server 實際驗證。"
        : "";
      byId("modelMeta").textContent = selectedRuntime() === MLX_RUNTIME
        ? `${formatBytes(model.size)} · 修改於 ${formatTime(model.modified_at)} · ${testSummary}${untestedHelp}`
        : `${formatBytes(model.size)} · 修改於 ${formatTime(model.modified_at)}`;
      return;
    }
    if (selectedRuntime() === MLX_RUNTIME) {
      byId("modelMeta").textContent = untestedCount
        ? `${testSummary}；尚未測試的模型仍可載入，並由 mlx-server 實際驗證。`
        : "尚無可用的 MLX 或 GGUF 模型。";
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
    const description = t("使用配對的 Draft 模型預測多個 Token，提升生成速度。");
    const describe = (statusText) => `${description} ${t(statusText)}`;
    const running = Boolean(status?.running);
    if (running) {
      const runningDFlash = status.draft_kind === "dflash" || (
        Boolean(status.dflash_enabled) && Boolean(status.draft_model)
      );
      toggle.checked = runningDFlash;
      toggle.disabled = true;
      meta.textContent = describe(runningDFlash
        ? `已啟用，Draft：${displayModelName(status.draft_model)}`
        : (status.draft_kind === "mtp"
          ? (status.draft_model
            ? `本次使用 MTP Draft：${displayModelName(status.draft_model)}。`
            : "本次使用 GGUF 內嵌 MTP 預測層。")
          : "本次啟動未使用 DFlash。"));
      return;
    }

    if (commandUsesMTP()) {
      toggle.checked = false;
      toggle.disabled = true;
      const target = selectedModel();
      if (target && !target.mtp_supported) {
        meta.textContent = describe(target.architecture
          ? `模型架構 ${target.architecture} 不支援 MTP。`
          : "無法確認模型架構，MTP 不可用。");
        return;
      }
      if (target?.mtp_embedded) {
        meta.textContent = describe("啟動參數已選用 MTP，將使用 GGUF 內嵌預測層。");
        return;
      }
      const draft = matchedDraftModel();
      meta.textContent = describe(draft
        ? `啟動參數已選用 MTP，Draft：${displayModelName(draft.path)}。`
        : "啟動參數已選用 MTP，但尚未找到配對的 Draft。");
      return;
    }

    if (byId("kvCacheQuantizationToggle").checked) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe("KV Cache 量化已啟用；兩者不可同時使用。");
      return;
    }

    const target = selectedModel();
    if (!target) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe("請先選擇 Target 模型。");
      return;
    }
    if (!target.dflash_supported) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe(target.architecture
        ? `模型架構 ${target.architecture} 不支援 DFlash。`
        : "無法確認模型架構，DFlash 不可用。");
      return;
    }

    toggle.disabled = false;
    const draft = matchedDraftModel();
    if (toggle.checked && draft) {
      meta.textContent = describe(`已啟用，Draft：${displayModelName(draft.path)}`);
      return;
    }
    const configuredDraft = String(selectedCommand()?.draft_model || "").trim();
    if (configuredDraft && !draft) {
      meta.textContent = describe(`啟動參數指定的 Draft 不存在：${displayModelName(configuredDraft)}`);
      return;
    }
    meta.textContent = describe(draft
      ? `可用 Draft：${displayModelName(draft.path)}（預設關閉）`
      : "模型支援 DFlash，但尚未找到配對的 Draft。");
  }

  function renderFastGGUFControl(status) {
    const toggle = byId("fastGGUFToggle");
    const meta = byId("fastGGUFMeta");
    const description = t("以 INT4、自動 Group 與 recurrent 控制投影 BF16 提高 MLX + GGUF 生成速度；屬實驗模式，可能明顯影響精度。");
    const describe = (statusText) => `${description} ${t(statusText)}`;
    const running = Boolean(status?.running);
    if (running) {
      toggle.checked = Boolean(status.fast_gguf);
      toggle.disabled = true;
      meta.textContent = describe(status.fast_gguf
        ? "本次啟動已使用快速GGUF模式。"
        : "本次啟動未使用快速GGUF模式。");
      return;
    }

    const available = selectedRuntime() === MLX_RUNTIME && isMLXGGUFModel(selectedModel());
    if (!available) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe("僅適用於 mlx-server 載入 GGUF。");
      return;
    }

    toggle.disabled = false;
    meta.textContent = describe(toggle.checked
      ? "已開啟；採用 INT4、自動 Group，並將 recurrent 控制投影保留為 BF16。"
      : "預設採用較保守的 INT8 與 Group 32。");
  }

  function renderMMapControl(status) {
    const toggle = byId("mmapToggle");
    const meta = byId("mmapMeta");
    const description = t("將模型權重映射為可回收的檔案頁面，降低載入時的記憶體壓力；執行中用量不一定下降。");
    const describe = (statusText) => `${description} ${t(statusText)}`;
    const reserveGB = Number(status?.running
      ? status.mmap_reserve_gb
      : selectedCommand()?.mmap_reserve_gb) || 0;
    const reserveText = reserveGB > 0 ? ` ${t("記憶體保留目標")}：${reserveGB} GB。` : "";
    const running = Boolean(status?.running);
    if (running) {
      toggle.checked = Boolean(status.mmap_enabled);
      toggle.disabled = true;
      meta.textContent = describe(status.mmap_enabled
        ? "本次啟動已使用 MMap。"
        : "本次啟動未使用 MMap。") + (status.mmap_enabled ? reserveText : "");
      return;
    }

    if (!selectedModel()) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe("請先選擇 Target 模型。");
      return;
    }

    toggle.disabled = false;
    meta.textContent = describe(toggle.checked
      ? "已開啟；速度會受儲存裝置及分頁壓力影響。"
      : "預設關閉。") + (toggle.checked ? reserveText : "");
  }

  function renderKVCacheQuantizationControl(status) {
    const toggle = byId("kvCacheQuantizationToggle");
    const meta = byId("kvCacheQuantizationMeta");
    const description = t("降低長 Context 的 KV Cache 記憶體用量；量化格式由啟動參數決定。");
    const describe = (statusText) => `${description} ${t(statusText)}`;
    const running = Boolean(status?.running);
    const quantization = String(running
      ? (status?.kv_cache_quantization || "")
      : (selectedCommand()?.kv_cache_quantization || "q4")).toLowerCase();
    const quantizationLabel = quantization ? quantization.toUpperCase() : "";
    if (running) {
      toggle.checked = Boolean(quantization);
      toggle.disabled = true;
      meta.textContent = describe(quantization
        ? `本次啟動已使用 KV Cache ${quantizationLabel}。`
        : "本次啟動未使用 KV Cache 量化。");
      return;
    }

    if (commandUsesMTP()) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe("MTP 已由啟動參數啟用；兩者不可同時使用。");
      return;
    }

    if (byId("dflashToggle").checked) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe("DFlash 已啟用；兩者不可同時使用。");
      return;
    }

    if (!selectedModel()) {
      toggle.checked = false;
      toggle.disabled = true;
      meta.textContent = describe("請先選擇 Target 模型。");
      return;
    }

    toggle.disabled = false;
    meta.textContent = describe(toggle.checked
      ? `已開啟；本次將使用 ${quantizationLabel}。`
      : `可用格式：${quantizationLabel}（預設關閉）。`);
  }

  function setAdvancedSettingsOpen(open, restoreFocus = false) {
    const popover = byId("runtimeAdvancedPopover");
    const button = byId("advancedSettingsButton");
    popover.hidden = !open;
    button.setAttribute("aria-expanded", String(open));
    if (!open && restoreFocus) button.focus();
  }

  async function loadRuntime() {
    state.runtime = await api("/api/runtime/status");
    renderRuntime(state.runtime);
  }

  function renderRuntime(status) {
    if (!status) return;
    const running = Boolean(status.running);
    const ready = running && Boolean(status.ready);
    const restoreSelection = running || !state.selectionTouched;
    if (restoreSelection && status.runtime) {
      byId("runtimeSelect").value = status.runtime;
    }
    const runtimeName = selectedRuntime();
    const runtimeLabel = runtimeName === MLX_RUNTIME ? "mlx-server" : "llama-server";
    const loading = running && !ready;
    const failed = !running && Boolean(status.last_error);
    if (loading && !state.calibrating && !byId("calibrationDialog").open) {
      showModelLoadingDialog(status);
    } else {
      closeModelLoadingDialog();
    }
    const statusDot = byId("statusDot");
    statusDot.classList.toggle("online", ready);
    statusDot.classList.toggle("loading", loading);
    statusDot.classList.toggle("failed", failed);
    const statusLabel = byId("statusLabel");
    statusLabel.classList.toggle("loading", loading);
    const loadingLabel = t("載入模型中…").replace(/\s*(?:…|\.\.\.)$/, "");
    statusLabel.textContent = ready
      ? `${runtimeLabel} 執行中`
      : loading
        ? `${runtimeLabel} · ${loadingLabel}`
        : failed ? `${runtimeLabel} 啟動失敗` : `${runtimeLabel} 已停止`;
    const runtimeAdjustments = [];
    if (status.performance_calibration_applied) runtimeAdjustments.push(t("已套用自動效能校準"));
    if (status.memory_pressure_protection_applied) runtimeAdjustments.push(t("記憶體壓力保護已調整啟動參數"));
    const adjustmentSuffix = runtimeAdjustments.length ? ` · ${runtimeAdjustments.join(" · ")}` : "";
    byId("statusDetail").textContent = running
      ? (ready
        ? `啟動時間 ${formatTime(status.started_at)} · ${status.startup_command_name || "未命名參數"}${adjustmentSuffix}`
        : `${t("模型載入完成後即可測試。")} · ${status.startup_command_name || "未命名參數"}${adjustmentSuffix}`)
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
    try {
      window.webkit?.messageHandlers?.tanpopoNative?.postMessage({
        type: "runtime-api-url",
        url: apiBaseURL
      });
    } catch (_error) {
      // 一般瀏覽器沒有原生橋接；網頁內的 API URL 顯示仍可正常使用。
    }
    link.textContent = apiBaseURL || "—";
    link.href = ready && apiBaseURL ? apiBaseURL : "#";
    byId("copyServerURL").disabled = !ready || !apiBaseURL;
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
      if (running) {
        byId("dflashToggle").checked = Boolean(status.dflash_enabled);
        byId("mmapToggle").checked = Boolean(status.mmap_enabled);
        byId("fastGGUFToggle").checked = Boolean(status.fast_gguf);
        byId("kvCacheQuantizationToggle").checked = Boolean(status.kv_cache_quantization);
      } else if (!state.selectionTouched) {
        applyModelFeatureDefaults();
      }
      renderModelMeta();
      renderMMProjMeta();
    }
    const modelReady = selectedModel() !== null;
    const commandReady = byId("commandSelect").options.length > 0;
    byId("runtimeSelect").disabled = state.calibrating || running;
    byId("commandSelect").disabled = state.calibrating || running || !commandReady;
    byId("modelSelect").disabled = state.calibrating || running || !commandReady || !state.mainModels.length;
    byId("mmprojSelect").disabled = state.calibrating || running
      || (selectedRuntime() === MLX_RUNTIME && !isMLXGGUFModel(selectedModel()))
      || !state.mmprojModels.length;
    byId("startButton").disabled = state.calibrating || running || !modelReady || !commandReady;
    byId("stopButton").disabled = state.calibrating || (!running && !status.desired_running);
    const calibrationButton = byId("calibrateRuntimeButton");
    calibrationButton.hidden = !state.settings?.auto_performance_calibration_enabled;
    calibrationButton.disabled = state.testing || state.calibrating;
    calibrationButton.textContent = t(state.calibrating ? "校準中…" : "效能校準");
    calibrationButton.title = t("可直接開啟並選擇要校準的模型，不必事先載入。");
    // 測試本身就是 Runtime 的可用性檢查；只要程序仍在執行就應允許
    // 使用者觸發，避免健康端點受 Access Key 保護時永遠無法測試。
    byId("testRuntimeButton").disabled = !running || state.testing || state.calibrating;
    byId("testRuntimeButton").textContent = state.testing ? t("測試中…") : t("測試");
    byId("repeatRuntimeTestButton").disabled = !running || state.testing || state.calibrating;
    byId("longRuntimeTestButton").disabled = !running || state.testing || state.calibrating;
    renderDFlashControl(status);
    renderFastGGUFControl(status);
    renderMMapControl(status);
    renderKVCacheQuantizationControl(status);
    if (state.calibrating) {
      for (const id of ["dflashToggle", "fastGGUFToggle", "mmapToggle", "kvCacheQuantizationToggle"]) {
        byId(id).disabled = true;
      }
    }
  }

  function showRuntimeTestResult(result = {}) {
    const dialog = byId("runtimeTestDialog");
    const usage = result.usage || {};
    const success = !result.error;
    const speed = Number(usage.tokens_per_second || 0);
    const isRepeated = result.mode === "repeat" && Array.isArray(result.runs);
    const isLong = result.mode === "long";
    const hasUsage = Number.isFinite(Number(usage.completion_tokens));
    byId("runtimeTestStatus").textContent = success
      ? t(isRepeated ? "已完成 3 次效能測試" : isLong ? "長輸出測試完成" : "模型服務運作正常")
      : t(result.error);
    byId("runtimeTestStatus").className = `runtime-test-status ${success ? "success" : "error"}`;
    byId("runtimeTestRuntime").textContent = result.runtime || state.runtime?.runtime || "—";
    byId("runtimeTestModel").textContent = displayModelName(state.runtime?.model);
    byId("runtimeTestPromptTokens").textContent = hasUsage
      ? (isRepeated ? usage.prompt_token_summary : Number(usage.prompt_tokens || 0).toLocaleString())
      : "—";
    byId("runtimeTestCompletionTokens").textContent = hasUsage
      ? (isRepeated ? usage.completion_token_summary : Number(usage.completion_tokens || 0).toLocaleString())
      : "—";
    byId("runtimeTestSpeedLabel").textContent = t(isRepeated ? "平均生成速度" : "生成速度");
    byId("runtimeTestSpeed").textContent = hasUsage && Number.isFinite(speed) && speed > 0
      ? `${speed.toFixed(1)} tokens/sec`
      : "—";
    byId("runtimeTestMedianRow").hidden = !isRepeated || !hasUsage;
    byId("runtimeTestMedianSpeed").textContent = isRepeated && Number.isFinite(result.medianTokensPerSecond)
      ? `${result.medianTokensPerSecond.toFixed(1)} tokens/sec`
      : "—";
    byId("runtimeTestElapsed").textContent = Number.isFinite(result.elapsedSeconds)
      ? `${result.elapsedSeconds.toFixed(2)} sec`
      : "—";
    if (!dialog.open) dialog.showModal();
  }

  function showRuntimeTestLoading(mode = "single", currentRun = 1) {
    const dialog = byId("runtimeTestDialog");
    byId("runtimeTestStatus").textContent = mode === "repeat"
      ? `${t("正在進行重複測試")} ${currentRun}/${RUNTIME_TEST_REPEAT_COUNT}…`
      : t(mode === "long" ? "正在進行長輸出測試（至少 500 Tokens）…" : "正在測試模型效能…");
    byId("runtimeTestStatus").className = "runtime-test-status testing";
    byId("runtimeTestRuntime").textContent = state.runtime?.runtime || "—";
    byId("runtimeTestModel").textContent = displayModelName(state.runtime?.model);
    byId("runtimeTestPromptTokens").textContent = "—";
    byId("runtimeTestCompletionTokens").textContent = "—";
    byId("runtimeTestSpeedLabel").textContent = t(mode === "repeat" ? "平均生成速度" : "生成速度");
    byId("runtimeTestSpeed").textContent = "—";
    byId("runtimeTestMedianRow").hidden = true;
    byId("runtimeTestMedianSpeed").textContent = "—";
    byId("runtimeTestElapsed").textContent = "—";
    if (!dialog.open) dialog.showModal();
  }

  function wait(milliseconds) {
    return new Promise((resolve) => window.setTimeout(resolve, milliseconds));
  }

  function openRefreshDialog() {
    const dialog = byId("refreshDialog");
    if (!dialog.open) dialog.showModal();
    return wait(1200);
  }

  function showModelLoadingDialog(status = state.runtime) {
    if (state.modelLoadingDismissed) return;
    const dialog = byId("modelLoadingDialog");
    const otherDialog = document.querySelector("dialog[open]");
    if (otherDialog && otherDialog !== dialog) return;
    const preparation = String(status?.model_preparation || "loading");
    const modelName = displayModelName(status?.model || selectedModel()?.path) || "—";
    const content = {
      checking_cache: [
        "正在檢查 Fast GGUF",
        "正在確認此模型是否已有可重用的轉換權重。"
      ],
      converting: [
        "正在轉換模型",
        "此模型需要轉換並建立 Fast GGUF；完成前請勿關閉程式。"
      ],
      saving_cache: [
        "正在建立 Fast GGUF",
        "模型已完成轉換，正在寫入 Fast GGUF；後續載入可直接重用。"
      ],
      loading_cache: [
        "正在載入 Fast GGUF",
        "已找到完成的 Fast GGUF，本次不需要重新轉換。"
      ],
      direct_loading: [
        "正在直接載入模型",
        "本次不建立 Fast GGUF；正在從 GGUF 即時準備 MLX 執行所需的權重。"
      ],
      loading: [
        "正在載入模型",
        "部分模型可能需要轉換，請耐心等候。"
      ]
    }[preparation] || [
      "正在載入模型",
      "部分模型可能需要轉換，請耐心等候。"
    ];
    byId("modelLoadingDialogTitle").textContent = t(content[0]);
    byId("modelLoadingDialogModel").textContent = `${t("模型")}：${modelName}`;
    byId("modelLoadingDialogDescription").textContent = t(content[1]);
    const completedBytes = Number(status?.model_preparation_completed_bytes || 0);
    const totalBytes = Number(status?.model_preparation_total_bytes || 0);
    const byteProgress = Number.isFinite(completedBytes)
      && Number.isFinite(totalBytes)
      && completedBytes >= 0
      && totalBytes > 0;
    const reportedPercent = Number(status?.model_preparation_progress_percent || 0);
    const reportedDeterminate = Boolean(status?.model_preparation_progress_determinate)
      && Number.isFinite(reportedPercent);
    const determinate = byteProgress || reportedDeterminate;
    const progressBar = byId("modelLoadingProgressBar");
    const progressTrack = byId("modelLoadingProgressTrack");
    const progressStage = byId("modelLoadingProgressStage");
    const progressPercent = byId("modelLoadingProgressPercent");
    progressBar.classList.toggle("indeterminate", !determinate);
    if (determinate) {
      const boundedCompleted = byteProgress ? Math.min(completedBytes, totalBytes) : 0;
      const percent = Math.min(100, Math.max(0, reportedDeterminate
        ? reportedPercent
        : boundedCompleted / totalBytes * 100));
      progressBar.style.width = `${percent}%`;
      progressStage.textContent = byteProgress
        ? `${formatBytes(boundedCompleted)} / ${formatBytes(totalBytes)}`
        : t("模型組裝進度");
      progressPercent.textContent = `${Math.floor(percent)}%`;
      progressTrack.setAttribute("aria-valuemin", "0");
      progressTrack.setAttribute("aria-valuemax", "100");
      progressTrack.setAttribute("aria-valuenow", String(Math.floor(percent)));
    } else {
      progressBar.style.width = "";
      progressStage.textContent = t("準備中…");
      progressPercent.textContent = "—";
      progressTrack.removeAttribute("aria-valuemin");
      progressTrack.removeAttribute("aria-valuemax");
      progressTrack.removeAttribute("aria-valuenow");
    }
    if (!dialog.open) dialog.showModal();
  }

  function closeModelLoadingDialog(dismiss = false) {
    if (dismiss) state.modelLoadingDismissed = true;
    const dialog = byId("modelLoadingDialog");
    if (dialog.open) dialog.close();
  }

  function requestModelConversionConfirmation(inspection) {
    const dialog = byId("modelConversionConfirmDialog");
    const modelName = displayModelName(inspection?.model || selectedModel()?.path) || "—";
    byId("modelConversionConfirmModel").textContent = modelName;
    byId("modelConversionConfirmSize").textContent = `${t("約")} ${formatBytes(
      Number(inspection?.estimated_cache_bytes || 0)
    )}`;
    if (modelConversionResolver) modelConversionResolver(null);
    return new Promise((resolve) => {
      modelConversionResolver = resolve;
      if (!dialog.open) dialog.showModal();
    });
  }

  function settleModelConversionConfirmation(choice) {
    const dialog = byId("modelConversionConfirmDialog");
    if (dialog.open) dialog.close();
    const resolve = modelConversionResolver;
    modelConversionResolver = null;
    if (resolve) resolve(choice);
  }

  async function closeRefreshDialog(minimumVisibleTime) {
    await minimumVisibleTime;
    const dialog = byId("refreshDialog");
    if (dialog.open) dialog.close();
  }

  function isRuntimeLoadingError(error) {
    const message = String(error?.message || "").trim().toLowerCase();
    return [
      "模型服務仍在載入中",
      "模型載入中",
      "loading model",
      "loading the model",
      "model is loading",
      "model loading",
      "model is still loading",
      "model is being loaded"
    ].some((fragment) => message.includes(fragment));
  }

  async function requestRuntimeTest(timeoutMilliseconds = RUNTIME_TEST_TIMEOUT_MS, options = {}) {
    const controller = new AbortController();
    const timeout = window.setTimeout(
      () => controller.abort(),
      Math.max(1, timeoutMilliseconds)
    );
    try {
      return await api("/api/chat/completions", {
        method: "POST",
        signal: controller.signal,
        body: JSON.stringify({
          messages: [{
            role: "user",
            content: options.prompt || RUNTIME_TEST_PROMPT
          }],
          max_tokens: Number(options.maxTokens || 128)
        })
      });
    } finally {
      window.clearTimeout(timeout);
    }
  }

  async function waitAndRunRuntimeTest(startedAt, options = {}) {
    let waitingForModel = false;
    const totalTimeoutMilliseconds = Number(options.timeoutMilliseconds || RUNTIME_TEST_TIMEOUT_MS);
    while (true) {
      const remainingMilliseconds = totalTimeoutMilliseconds - (performance.now() - startedAt);
      if (remainingMilliseconds <= 0) {
        throw new Error(t(waitingForModel
          ? "模型載入逾時，請查看日誌後重新啟動服務"
          : "模型效能測試逾時，請稍後再試"));
      }
      try {
        return await requestRuntimeTest(remainingMilliseconds, options);
      } catch (error) {
        if (error?.name === "AbortError") {
          throw new Error(t(waitingForModel
            ? "模型載入逾時，請查看日誌後重新啟動服務"
            : "模型效能測試逾時，請稍後再試"));
        }
        if (!isRuntimeLoadingError(error)) throw error;
        waitingForModel = true;
        if (performance.now() - startedAt >= totalTimeoutMilliseconds) {
          throw new Error(t("模型載入逾時，請查看日誌後重新啟動服務"));
        }

        const latest = await api("/api/runtime/status");
        state.runtime = latest;
        renderRuntime(latest);
        if (!latest.running) {
          throw new Error(latest.last_error || t("模型 Runtime 已停止或無法連線，請返回執行狀態確認"));
        }
        byId("runtimeTestStatus").textContent = t("模型載入中，完成後將自動開始測試…");
        byId("runtimeTestStatus").className = "runtime-test-status testing";
        await wait(RUNTIME_TEST_RETRY_MS);
      }
    }
  }

  function median(values) {
    const sorted = values.slice().sort((left, right) => left - right);
    const middle = Math.floor(sorted.length / 2);
    return sorted.length % 2 === 0
      ? (sorted[middle - 1] + sorted[middle]) / 2
      : sorted[middle];
  }

  function repeatedTokenSummary(runs, key) {
    const values = runs.map((result) => Number(result.usage?.[key] || 0));
    const total = values.reduce((sum, value) => sum + value, 0);
    return `${values.map((value) => value.toLocaleString()).join(" · ")} = ${total.toLocaleString()}`;
  }

  async function runRuntimeTest(mode = "single") {
    if (!state.runtime?.running || state.testing || state.calibrating) return;
    state.testing = true;
    renderRuntime(state.runtime);
    showRuntimeTestLoading(mode);
    const startedAt = performance.now();
    try {
      if (mode === "repeat") {
        const runs = [];
        for (let index = 0; index < RUNTIME_TEST_REPEAT_COUNT; index += 1) {
          showRuntimeTestLoading(mode, index + 1);
          runs.push(await waitAndRunRuntimeTest(startedAt, {
            timeoutMilliseconds: RUNTIME_TEST_REPEAT_TIMEOUT_MS
          }));
        }
        const speeds = runs
          .map((result) => Number(result.usage?.tokens_per_second || 0))
          .filter((value) => Number.isFinite(value) && value > 0);
        if (speeds.length !== RUNTIME_TEST_REPEAT_COUNT) {
          throw new Error(t("Runtime 未回傳完整的速度資料"));
        }
        showRuntimeTestResult({
          mode,
          runs,
          runtime: runs[0]?.runtime,
          usage: {
            prompt_tokens: runs.reduce((sum, result) => sum + Number(result.usage?.prompt_tokens || 0), 0),
            completion_tokens: runs.reduce((sum, result) => sum + Number(result.usage?.completion_tokens || 0), 0),
            prompt_token_summary: repeatedTokenSummary(runs, "prompt_tokens"),
            completion_token_summary: repeatedTokenSummary(runs, "completion_tokens"),
            tokens_per_second: speeds.reduce((sum, value) => sum + value, 0) / speeds.length
          },
          medianTokensPerSecond: median(speeds),
          elapsedSeconds: (performance.now() - startedAt) / 1000
        });
        return;
      }

      const options = mode === "long"
        ? {
            prompt: RUNTIME_TEST_LONG_PROMPT,
            maxTokens: RUNTIME_TEST_LONG_MAX_TOKENS,
            timeoutMilliseconds: RUNTIME_TEST_LONG_TIMEOUT_MS
          }
        : {};
      const result = await waitAndRunRuntimeTest(startedAt, options);
      const completionTokens = Number(result.usage?.completion_tokens || 0);
      showRuntimeTestResult({
        ...result,
        mode,
        error: mode === "long" && completionTokens < RUNTIME_TEST_LONG_MIN_TOKENS
          ? `${t("長輸出測試未達 500 Tokens")}（${completionTokens.toLocaleString()} Tokens）`
          : "",
        elapsedSeconds: (performance.now() - startedAt) / 1000
      });
    } catch (error) {
      showRuntimeTestResult({ mode, error: error.message });
    } finally {
      state.testing = false;
      renderRuntime(state.runtime);
    }
  }

  async function loadCalibrationPlan(model, startupCommandID) {
    const query = new URLSearchParams({
      model,
      startup_command_id: startupCommandID
    });
    return api(`/api/runtime/calibration?${query.toString()}`);
  }

  function calibrationScore(runs) {
    const average = runs.reduce((sum, value) => sum + value, 0) / runs.length;
    return { median: median(runs), average };
  }

  async function stopRuntimeForCalibration() {
    const current = await api("/api/runtime/status");
    if (!current.running && !current.desired_running) return;
    await api("/api/runtime/stop", { method: "POST" });
  }

  const CALIBRATION_TUNING_KEYS = ["threads", "batch_size", "ubatch_size", "prefill_step_size"];

  function calibrationTuning(status, fallback = {}) {
    const value = Object.keys(status?.performance_tuning || {}).length ? status.performance_tuning : (fallback || {});
    return Object.fromEntries(CALIBRATION_TUNING_KEYS.map((key) => [key, Number(value[key] || 0)]));
  }

  function sameCalibrationTuning(left, right) {
    return CALIBRATION_TUNING_KEYS.every((key) => Number(left?.[key] || 0) === Number(right?.[key] || 0));
  }

  function calibrationTuningLabel(tuning = {}, runtimeName) {
    if (runtimeName === MLX_RUNTIME) return `Prefill Step Size: ${tuning.prefill_step_size || 512}`;
    return `Threads: ${tuning.threads || t("自動")} · Batch: ${tuning.batch_size || t("Runtime 預設")} · UBatch: ${tuning.ubatch_size || t("Runtime 預設")}`;
  }

  function calibrationElement(tag, className, text) {
    const element = document.createElement(tag);
    if (className) element.className = className;
    if (text !== undefined) element.textContent = text;
    return element;
  }

  function calibrationProgress(value, max, label) {
    const progress = calibrationElement("progress", "calibration-progress");
    progress.max = max;
    if (value !== null) progress.value = value;
    progress.setAttribute("aria-label", label);
    return progress;
  }

  function setCalibrationStatus(message, kind = "testing") {
    byId("calibrationStatus").textContent = message;
    byId("calibrationStatus").className = `runtime-test-status ${kind}`;
  }

  function renderCalibrationProgress() {
    const session = state.calibration;
    if (!session) return;
    let completed = 0;
    const cards = session.jobs.map((job) => {
      const card = calibrationElement("section", "calibration-item");
      card.append(calibrationElement("h4", job.best ? "calibration-complete" : "", `${job.best ? "✓ " : ""}${displayModelName(job.model.path)}`));
      card.append(calibrationElement("p", "helper", `${job.command?.runtime || "—"} · ${job.command?.name || "—"} · ${job.model.path}`));
      for (const [index, candidate] of job.candidates.entries()) {
        const group = calibrationElement("div", "calibration-candidate");
        const done = candidate.runs.filter((run) => run.status === "done").length;
        completed += done;
        const heading = calibrationElement("div", "calibration-candidate-heading");
        heading.append(calibrationElement("strong", done === 3 ? "calibration-complete" : "", `${done === 3 ? "✓ " : ""}${t("配置")} ${index + 1} · ${t(candidate.label)}`));
        heading.append(calibrationElement("span", "helper", `${done}/3`));
        group.append(heading, calibrationElement("p", "helper", calibrationTuningLabel(candidate.tuning, job.command.runtime)));
        group.append(calibrationProgress(candidate.loading ? null : done, 3, `${t("配置")} ${index + 1}`));
        const runs = calibrationElement("div", "calibration-runs");
        candidate.runs.forEach((run, runIndex) => {
          const item = calibrationElement("div", `calibration-run ${run.status}`);
          const symbol = run.status === "done" ? "✓ " : run.status === "error" ? "✕ " : "";
          item.append(calibrationElement("strong", "", `${symbol}${t("測試")} ${runIndex + 1}`));
          item.append(calibrationProgress(run.status === "running" ? null : run.status === "done" ? 1 : 0, 1, `${displayModelName(job.model.path)} · ${t("配置")} ${index + 1} · ${t("測試")} ${runIndex + 1}`));
          const detail = run.status === "done" ? `${run.speed.toFixed(2)} tok/s`
            : run.status === "running" ? `${t("測試中…")} ${Math.floor((performance.now() - run.startedAt) / 1000)} sec`
              : run.status === "error" ? t("失敗") : job.error ? t("未執行") : t("等待中");
          item.append(calibrationElement("span", "", detail));
          runs.append(item);
        });
        group.append(runs);
        card.append(group);
      }
      if (job.error) card.append(calibrationElement("p", "calibration-error", job.error));
      return card;
    });
    byId("calibrationItems").replaceChildren(...cards);
    byId("calibrationTotalProgress").max = session.jobs.length * 9 || 1;
    byId("calibrationTotalProgress").value = completed;
    byId("calibrationProgressCount").textContent = `${completed} / ${session.jobs.length * 9}`;
  }

  function renderCalibrationResults() {
    const cards = state.calibration.jobs.map((job) => {
      const card = calibrationElement("section", "calibration-result");
      card.append(calibrationElement("h4", "", displayModelName(job.model.path)));
      card.append(calibrationElement("p", "helper", `${job.command?.runtime || "—"} · ${job.command?.name || "—"}`));
      const table = calibrationElement("table");
      const header = calibrationElement("tr");
      for (const label of ["配置", "測試 1", "測試 2", "測試 3", "平均生成速度", "中位生成速度"]) {
        header.append(calibrationElement("th", "", t(label)));
      }
      const head = calibrationElement("thead");
      head.append(header);
      const body = calibrationElement("tbody");
      job.candidates.forEach((candidate, index) => {
        const row = calibrationElement("tr", candidate === job.best ? "calibration-complete" : "");
        row.append(calibrationElement("td", "", `${candidate === job.best ? "✓ " : ""}${t("配置")} ${index + 1} · ${calibrationTuningLabel(candidate.tuning, job.command.runtime)}`));
        for (const run of candidate.runs) row.append(calibrationElement("td", "", run.status === "done" ? run.speed.toFixed(2) : "—"));
        row.append(calibrationElement("td", "", candidate.score ? candidate.score.average.toFixed(2) : "—"));
        row.append(calibrationElement("td", "", candidate.score ? candidate.score.median.toFixed(2) : "—"));
        body.append(row);
      });
      table.append(head, body);
      const wrapper = calibrationElement("div", "calibration-result-table");
      wrapper.append(table);
      card.append(wrapper, calibrationElement("p", "helper", "tokens/sec"));
      if (job.best) {
        const baseline = job.candidates[0].score.median;
        const improvement = (job.best.score.median / baseline - 1) * 100;
        const recommendation = calibrationElement("div", "calibration-recommendation");
        recommendation.append(calibrationElement("strong", "", `${t("建議配置")} · ${job.command.runtime} · ${calibrationTuningLabel(job.best.tuning, job.command.runtime)}`));
        recommendation.append(calibrationElement("p", "", `${t("相較第一組配置")} ${improvement >= 0 ? "+" : ""}${improvement.toFixed(1)}% · ${job.saved ? t("已保存，後續啟動自動套用") : t("結果尚未保存")}`));
        if (job.best.runtimeStatus?.effective_context_size) {
          recommendation.append(calibrationElement("p", "helper", `Context: ${job.best.runtimeStatus.effective_context_size.toLocaleString()} · KV Cache: ${job.best.runtimeStatus.kv_cache_quantization || t("關閉")}`));
        }
        for (const action of job.best.runtimeStatus?.memory_pressure_protection_actions || []) {
          recommendation.append(calibrationElement("p", "helper", localizedMemoryProtectionAction(action)));
        }
        card.append(recommendation);
      }
      if (job.error) card.append(calibrationElement("p", "calibration-error", job.error));
      return card;
    });
    byId("calibrationResults").replaceChildren(...cards);
    byId("calibrationResultsSection").hidden = false;
  }

  function calibrationModelKey(model, runtimeName) {
    const gguf = model.format === "gguf" || runtimeName === LLAMA_RUNTIME || String(model.path).startsWith("gguf:");
    return `${gguf ? "gguf" : "mlx"}:${String(model.path).replace(/^gguf:/, "")}`;
  }

  function calibrationCommandForRuntime(commands, runtimeName, preferredID) {
    const eligible = commands.filter((command) => command.runtime === runtimeName && !command.draft_model
      && !(command.extra_args || []).some((argument) => /^--(?:mtp|dflash|draft|spec|model-draft)(?:[-=]|$)/.test(argument)));
    return eligible.find((command) => command.id === preferredID) || eligible[0] || null;
  }

  async function loadCalibrationModels(current, commands, preferredID) {
    const runtimes = Array.from(byId("runtimeSelect").options, (option) => option.value);
    const catalogues = await Promise.all(runtimes.map(async (runtimeName) => {
      try {
        const payload = await api(`/api/models?runtime=${encodeURIComponent(runtimeName)}`);
        return { runtime: runtimeName, models: payload.models || [] };
      } catch (error) {
        return { runtime: runtimeName, models: [], error: error.message };
      }
    }));
    if (catalogues.every((catalogue) => catalogue.error)) throw new Error(catalogues.map((catalogue) => catalogue.error).join("；"));
    const grouped = new Map();
    for (const catalogue of catalogues) {
      for (const model of catalogue.models) {
        if (isMMProjModel(model) || isDFlashDraftModel(model) || isMTPDraftModel(model)) continue;
        const key = calibrationModelKey(model, catalogue.runtime);
        if (!grouped.has(key)) grouped.set(key, []);
        grouped.get(key).push({ ...model, calibration_key: key, calibration_runtime: catalogue.runtime });
      }
    }
    const models = Array.from(grouped.values()).map((variants) => {
      // 相同 GGUF 可能同時列在兩個 Runtime；只列一次，已載入者優先沿用。
      const loaded = current.running && variants.find((model) => model.path === current.model && model.calibration_runtime === current.runtime);
      const candidates = variants.slice().sort((left, right) => Number(right.calibration_runtime === LLAMA_RUNTIME) - Number(left.calibration_runtime === LLAMA_RUNTIME));
      const picked = loaded || candidates.find((model) => !model.runtime_untested
        && !(model.fast_gguf_fallback && model.calibration_runtime === LLAMA_RUNTIME)
        && calibrationCommandForRuntime(commands, model.calibration_runtime, preferredID)) || candidates[0];
      const command = loaded ? commands.find((item) => item.id === current.startup_command_id)
        : !picked.runtime_untested ? calibrationCommandForRuntime(commands, picked.calibration_runtime, preferredID) : null;
      return { ...picked, calibration_command: command || null };
    }).sort(compareModelsByDisplayName);
    return { models, catalogues };
  }

  async function openCalibrationDialog() {
    if (!state.settings?.auto_performance_calibration_enabled || state.testing || state.calibrating) return;
    let current;
    let commands;
    let catalog;
    try {
      const responses = await Promise.all([api("/api/runtime/status"), api("/api/startup-commands")]);
      current = responses[0];
      commands = responses[1].commands || [];
      catalog = await loadCalibrationModels(current, commands, selectedCommand()?.id);
    } catch (error) {
      showMessage(error.message, "error");
      return;
    }
    if (byId("calibrationDialog").open || state.testing || state.calibrating) return;
    // 停止後的狀態仍可能保留上次模型名稱；只有實際載入完成的模型才預先勾選。
    const loadedModel = current.running && current.ready
      ? catalog.models.find((model) => model.path === current.model && model.calibration_runtime === current.runtime && model.calibration_command) : null;
    state.calibration = {
      originalCommand: commands.find((command) => command.id === current.startup_command_id) || null,
      selected: new Set(loadedModel ? [loadedModel.calibration_key] : []),
      format: "all",
      models: catalog.models, catalogues: catalog.catalogues, jobs: [], original: null, knownTunings: new Map(),
      openingPID: current.running ? current.pid : null,
      launchDefaults: {
        running: false, model: "", mmproj: "", draft_model: "",
        dflash_enabled: false,
        mmap_enabled: Boolean(state.settings?.default_mmap_enabled),
        fast_gguf: state.settings?.default_fast_gguf_enabled !== false,
        kv_cache_quantization: Boolean(state.settings?.default_kv_cache_quantization_enabled),
        skip_gguf_conversion_cache: false
      }
    };
    renderCalibrationModelOptions();
    byId("calibrationSelection").hidden = false;
    byId("calibrationProgressSection").hidden = true;
    byId("calibrationResultsSection").hidden = true;
    byId("startCalibrationButton").hidden = false;
    byId("closeCalibrationButton").disabled = false;
    byId("closeCalibrationIcon").disabled = false;
    setCalibrationStatus(t("自動依模型選擇適用的 Server 與啟動配置。"), "");
    updateCalibrationSelectionCount();
    byId("calibrationDialog").showModal();
  }

  function renderCalibrationModelOptions() {
    const session = state.calibration;
    if (!session) return;
    document.querySelectorAll("[data-calibration-format]").forEach((button) => {
      const active = button.dataset.calibrationFormat === session.format;
      button.classList.toggle("active", active);
      button.setAttribute("aria-pressed", String(active));
    });
    // 過濾依模型格式而非 Server；Fast GGUF 即使由 MLX 執行，也歸入 GGUF。
    const visibleModels = session.models.filter((item) => session.format === "all"
      || item.calibration_key.startsWith(`${session.format}:`));
    const options = visibleModels.map((item) => {
      const label = calibrationElement("label", "calibration-model-option");
      const checkbox = document.createElement("input");
      checkbox.type = "checkbox";
      checkbox.checked = state.calibration.selected.has(item.calibration_key);
      checkbox.disabled = !item.calibration_command;
      checkbox.addEventListener("change", () => {
        if (checkbox.checked) state.calibration.selected.add(item.calibration_key);
        else state.calibration.selected.delete(item.calibration_key);
        updateCalibrationSelectionCount();
      });
      const copy = calibrationElement("span", "calibration-model-copy");
      copy.append(calibrationElement("strong", "", displayModelName(item.path)), calibrationElement("small", "", item.path));
      copy.append(calibrationElement("small", "", item.calibration_command
        ? `${item.calibration_runtime} · ${item.calibration_command.name}` : t("沒有適用的 Server 或啟動配置")));
      label.append(checkbox, copy);
      return label;
    });
    byId("calibrationModels").replaceChildren(...options);
    byId("calibrationModels").scrollTop = 0;
    byId("calibrationModelsEmpty").hidden = visibleModels.length > 0;
  }

  function updateCalibrationSelectionCount() {
    const count = state.calibration.selected.size;
    byId("calibrationSelectionCount").textContent = `${t("已選模型（全部格式）")}：${count}`;
    byId("startCalibrationButton").disabled = count === 0 || state.calibrating;
  }

  function calibrationPayload(model, original, command) {
    const sameModel = original.running && model.path === original.model && command.runtime === original.runtime;
    const defaults = sameModel ? original : state.calibration.launchDefaults;
    const catalogue = state.calibration.catalogues.find((item) => item.runtime === command.runtime);
    const projectors = (catalogue?.models || []).filter((item) => isMMProjModel(item) && pathDirectory(item.path) === pathDirectory(model.path));
    const mmproj = sameModel ? original.mmproj : command.runtime === MLX_RUNTIME ? ""
      : projectors.length === 1 ? projectors[0].path : "";
    return {
      model: model.path, mmproj: mmproj || "", draft_model: sameModel ? original.draft_model || "" : "",
      dflash_enabled: sameModel && Boolean(original.dflash_enabled), mmap_enabled: Boolean(defaults.mmap_enabled),
      fast_gguf_enabled: command.runtime === MLX_RUNTIME && isMLXGGUFModel(model)
        && (sameModel ? Boolean(original.fast_gguf) : Boolean(model.fast_gguf_fallback || defaults.fast_gguf)),
      kv_cache_quantization_enabled: Boolean(defaults.kv_cache_quantization) && Boolean(command.kv_cache_quantization),
      skip_gguf_conversion_cache: sameModel && Boolean(original.skip_gguf_conversion_cache),
      startup_command_id: command.id
    };
  }

  async function ensureCalibrationRuntime(job, tuning) {
    let current = await api("/api/runtime/status");
    const knownTuning = state.calibration.knownTunings.get(current.pid) || calibrationTuning(current);
    if (current.running && current.model === job.payload.model && current.startup_command_id === job.payload.startup_command_id
      && sameCalibrationTuning(knownTuning, tuning)) {
      state.runtime = current;
    } else {
      let confirmationKey = "";
      if (job.command.runtime === MLX_RUNTIME && isMLXGGUFModel(job.model)) {
        // 轉換預檢與啟動是不同 API；僅傳送預檢合約允許的欄位。
        const inspection = await api("/api/runtime/conversion-preflight", {
          method: "POST", body: JSON.stringify({ model: job.payload.model, mmproj: job.payload.mmproj,
            fast_gguf_enabled: job.payload.fast_gguf_enabled, startup_command_id: job.command.id })
        });
        if (inspection.requires_conversion) {
          const choice = await requestModelConversionConfirmation(inspection);
          if (choice !== "cache") throw new Error(t("校準需要先建立 Fast GGUF；此模型已略過"));
          confirmationKey = inspection.cache_key || "";
        }
      }
      await stopRuntimeForCalibration();
      current = await api("/api/runtime/start", {
        method: "POST",
        body: JSON.stringify({ ...job.payload, conversion_confirmation_key: confirmationKey,
          skip_saved_calibration: true, calibration_override: tuning })
      });
      state.runtime = current;
      state.calibration.knownTunings.set(current.pid, calibrationTuning(current, tuning));
    }
    renderRuntime(current);
    const deadline = performance.now() + RUNTIME_TEST_REPEAT_TIMEOUT_MS;
    while (!current.ready) {
      if (!current.running) throw new Error(current.last_error || t("模型 Runtime 已停止或無法連線，請返回執行狀態確認"));
      if (performance.now() >= deadline) throw new Error(t("模型載入逾時，請查看日誌後重新啟動服務"));
      const percent = current.model_preparation_progress_determinate ? ` ${current.model_preparation_progress_percent}%` : "";
      setCalibrationStatus(`${displayModelName(job.model.path)} · ${t("載入模型中…")}${percent}`);
      await wait(RUNTIME_TEST_RETRY_MS);
      current = await api("/api/runtime/status");
      state.runtime = current;
      renderRuntime(current);
    }
    return current;
  }

  async function calibrateModel(job) {
    const session = state.calibration;
    if (!job.command) throw new Error(t("沒有適用的 Server 或啟動配置"));
    job.payload = calibrationPayload(job.model, session.original, job.command);
    const plan = await loadCalibrationPlan(job.model.path, job.command.id);
    if (!plan.enabled) throw new Error(t("自動效能校準目前未啟用"));
    if (plan.candidates?.length !== 3) throw new Error(t("無可用的效能校準設定"));
    job.candidates = plan.candidates.map((candidate) => ({ ...candidate, runs: Array.from({ length: 3 }, () => ({ status: "pending" })) }));
    for (const [candidateIndex, candidate] of job.candidates.entries()) {
      candidate.loading = true;
      setCalibrationStatus(`${displayModelName(job.model.path)} · ${t("配置")} ${candidateIndex + 1} · ${t("準備中…")}`);
      renderCalibrationProgress();
      try {
        candidate.runtimeStatus = await ensureCalibrationRuntime(job, candidate.tuning);
        candidate.tuning = state.calibration.knownTunings.get(candidate.runtimeStatus.pid) || calibrationTuning(candidate.runtimeStatus, candidate.tuning);
      } finally {
        candidate.loading = false;
      }
      for (const [runIndex, run] of candidate.runs.entries()) {
        run.status = "running";
        run.startedAt = performance.now();
        setCalibrationStatus(`${displayModelName(job.model.path)} · ${t("配置")} ${candidateIndex + 1}/3 · ${t("測試")} ${runIndex + 1}/3`);
        renderCalibrationProgress();
        try {
          const current = await api("/api/runtime/status");
          if (!current.running || current.pid !== candidate.runtimeStatus.pid || current.model !== job.model.path) {
            throw new Error(t("執行狀態已變更，請重新開啟校準視窗。"));
          }
          const result = await waitAndRunRuntimeTest(run.startedAt);
          run.speed = Number(result.usage?.tokens_per_second || 0);
          if (!Number.isFinite(run.speed) || run.speed <= 0) throw new Error(t("Runtime 未回傳完整的速度資料"));
          run.status = "done";
        } catch (error) {
          run.status = "error";
          throw error;
        } finally {
          renderCalibrationProgress();
        }
      }
      candidate.score = calibrationScore(candidate.runs.map((run) => run.speed));
    }
    job.best = job.candidates.slice().sort((left, right) => right.score.median - left.score.median || right.score.average - left.score.average)[0];
    await api("/api/runtime/calibration", {
      method: "PUT", body: JSON.stringify({ model: job.model.path, startup_command_id: job.command.id,
        tuning: job.best.tuning, runs: job.best.runs.map((run) => run.speed) })
    });
    job.saved = true;
  }

  async function startCalibrationSession() {
    const session = state.calibration;
    if (!session || state.calibrating || state.testing || !session.selected.size) return;
    state.calibrating = true;
    byId("calibrationSelection").hidden = true;
    byId("calibrationProgressSection").hidden = false;
    byId("startCalibrationButton").hidden = true;
    byId("closeCalibrationButton").disabled = true;
    byId("closeCalibrationIcon").disabled = true;
    renderRuntime(state.runtime);
    setCalibrationStatus(t("準備中…"));
    const timer = window.setInterval(renderCalibrationProgress, 500);
    try {
      const original = await api("/api/runtime/status");
      if ((session.openingPID && !original.running)
        || (original.running && (!original.ready || original.startup_command_id !== session.originalCommand?.id
          || original.pid !== session.openingPID))) {
        throw new Error(t("執行狀態已變更，請重新開啟校準視窗。"));
      }
      session.original = original.running ? original : session.launchDefaults;
      if (original.running) {
        if (original.skip_gguf_conversion_cache) throw new Error(t("直接載入模式不會執行自動效能校準"));
        const originalPlan = await loadCalibrationPlan(original.model, session.originalCommand.id);
        if (!originalPlan.enabled) throw new Error(t("自動效能校準目前未啟用"));
        session.originalTuning = calibrationTuning(original, originalPlan.candidates?.[0]?.tuning);
        session.knownTunings.set(original.pid, session.originalTuning);
      }
      session.jobs = session.models.filter((model) => session.selected.has(model.calibration_key))
        .sort((left, right) => Number(right.path === session.original.model) - Number(left.path === session.original.model))
        .map((model) => ({ model, command: model.calibration_command, candidates: [], saved: false }));
      for (const job of session.jobs) {
        try { await calibrateModel(job); }
        catch (error) { job.error = error.message; }
        renderCalibrationProgress();
      }
      renderCalibrationResults();
      if (original.running) {
        setCalibrationStatus(t("正在恢復原模型並套用建議配置…"));
        const originalJob = session.jobs.find((job) => job.model.path === original.model);
        const originalModel = session.models.find((model) => model.path === original.model) || { path: original.model };
        const tuning = originalJob?.saved ? originalJob.best.tuning : session.originalTuning;
        await ensureCalibrationRuntime({ model: originalModel, command: session.originalCommand,
          payload: calibrationPayload(originalModel, original, session.originalCommand) }, tuning);
      } else {
        setCalibrationStatus(t("正在恢復未載入狀態…"));
        await stopRuntimeForCalibration();
      }
      // 若最後一組就是最佳配置，僅保存結果，不再重複載入模型。
      const failed = session.jobs.some((job) => job.error);
      const completionMessage = original.running ? "校準完成，結果已保存並恢復原模型。" : "校準完成，結果已保存並恢復未載入狀態。";
      setCalibrationStatus(t(failed ? "校準完成，部分模型未完成，請查看結果。" : completionMessage), failed ? "error" : "success");
    } catch (error) {
      setCalibrationStatus(error.message, "error");
    } finally {
      window.clearInterval(timer);
      state.calibrating = false;
      await loadRuntime().catch(() => {});
      renderRuntime(state.runtime);
      renderCalibrationProgress();
      renderCalibrationResults();
      byId("closeCalibrationButton").disabled = false;
      byId("closeCalibrationIcon").disabled = false;
      if (session.jobs.length) byId("calibrationResultsSection").scrollIntoView({ block: "start", behavior: "smooth" });
    }
  }

  async function loadLogs() {
    const payload = await api("/api/runtime/logs");
    const output = byId("logOutput");
    const shouldFollow = output.scrollHeight - output.scrollTop - output.clientHeight < 40;
    output.textContent = payload.logs || "尚無日誌。";
    if (shouldFollow) output.scrollTop = output.scrollHeight;
  }

  function setLogsExpanded(expanded) {
    const output = byId("logOutput");
    const button = byId("toggleLogsButton");
    output.hidden = !expanded;
    button.setAttribute("aria-expanded", String(expanded));
    button.textContent = t(expanded ? "收合日誌" : "展開日誌");
    if (expanded) output.scrollTop = output.scrollHeight;
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
    const mtpEnabled = runtimeName === MLX_RUNTIME && commandUsesMTP();
    const dflashEnabled = !mtpEnabled && byId("dflashToggle").checked;
    const mmapEnabled = byId("mmapToggle").checked;
    const fastGGUFEnabled = byId("fastGGUFToggle").checked;
    const kvCacheQuantizationEnabled = byId("kvCacheQuantizationToggle").checked;
    const embeddedMTP = mtpEnabled && Boolean(selectedModel()?.mtp_embedded);
    if (mtpEnabled && !selectedModel()?.mtp_supported) {
      showMessage(t("選定的 Target 模型不支援 MTP，請改用相容的 MLX 模型或內嵌 MTP 的 GGUF。"), "error");
      return;
    }
    const draftModel = (dflashEnabled || (mtpEnabled && !embeddedMTP)) ? matchedDraftModel() : null;
    if ((dflashEnabled || (mtpEnabled && !embeddedMTP)) && !draftModel) {
      promptDraftDownload(mtpEnabled ? "MTP" : "DFlash");
      return;
    }
    button.disabled = true;
    try {
      let conversionConfirmationKey = "";
      let skipGGUFConversionCache = false;
      let initialPreparation = "loading";
      if (runtimeName === MLX_RUNTIME && isMLXGGUFModel(selectedModel())) {
        const inspection = await api("/api/runtime/conversion-preflight", {
          method: "POST",
          body: JSON.stringify({
            model: byId("modelSelect").value,
            mmproj: "",
            fast_gguf_enabled: fastGGUFEnabled,
            startup_command_id: byId("commandSelect").value
          })
        });
        if (inspection.requires_conversion) {
          const conversionChoice = await requestModelConversionConfirmation(inspection);
          if (!conversionChoice) return;
          skipGGUFConversionCache = conversionChoice === "direct";
          if (!skipGGUFConversionCache) {
            conversionConfirmationKey = String(inspection.cache_key || "");
          }
          initialPreparation = skipGGUFConversionCache ? "direct_loading" : "converting";
        } else if (inspection.cache_hit) {
          initialPreparation = "loading_cache";
        } else {
          initialPreparation = "checking_cache";
        }
      }
      state.modelLoadingDismissed = false;
      showModelLoadingDialog({
        runtime: runtimeName,
        model: byId("modelSelect").value,
        model_preparation: initialPreparation
      });
      const basePayload = {
        model: byId("modelSelect").value,
        mmproj: runtimeName === MLX_RUNTIME ? "" : byId("mmprojSelect").value,
        draft_model: draftModel?.path || "",
        dflash_enabled: dflashEnabled,
        mmap_enabled: mmapEnabled,
        fast_gguf_enabled: fastGGUFEnabled,
        kv_cache_quantization_enabled: kvCacheQuantizationEnabled,
        skip_gguf_conversion_cache: skipGGUFConversionCache,
        conversion_confirmation_key: conversionConfirmationKey,
        startup_command_id: byId("commandSelect").value
      };
      const startedStatus = await api("/api/runtime/start", {
        method: "POST",
        body: JSON.stringify(basePayload)
      });
      state.runtime = startedStatus;
      if (Array.isArray(startedStatus.memory_pressure_protection_actions)
        && startedStatus.memory_pressure_protection_actions.length) {
        showMessage(`${t("記憶體壓力保護已調整啟動參數")}：${startedStatus.memory_pressure_protection_actions.map(localizedMemoryProtectionAction).join("；")}`);
      } else if (startedStatus.performance_calibration_applied) {
        showMessage(t("已套用自動效能校準"));
      } else {
        showMessage(`${runtimeName} 已啟動`);
      }
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

  byId("calibrateRuntimeButton").addEventListener("click", openCalibrationDialog);
  document.querySelectorAll("[data-calibration-format]").forEach((button) => {
    button.addEventListener("click", () => {
      if (!state.calibration || state.calibrating) return;
      state.calibration.format = button.dataset.calibrationFormat;
      renderCalibrationModelOptions();
    });
  });
  byId("startCalibrationButton").addEventListener("click", startCalibrationSession);
  for (const id of ["closeCalibrationButton", "closeCalibrationIcon"]) {
    byId(id).addEventListener("click", () => { if (!state.calibrating) byId("calibrationDialog").close(); });
  }
  byId("calibrationDialog").addEventListener("cancel", (event) => { if (state.calibrating) event.preventDefault(); });

  byId("testRuntimeButton").addEventListener("click", async () => {
    await runRuntimeTest("single");
  });

  byId("repeatRuntimeTestButton").addEventListener("click", async () => {
    await runRuntimeTest("repeat");
  });

  byId("longRuntimeTestButton").addEventListener("click", async () => {
    await runRuntimeTest("long");
  });

  const closeRuntimeTestDialog = () => byId("runtimeTestDialog").close();
  byId("closeRuntimeTestButton").addEventListener("click", closeRuntimeTestDialog);
  byId("closeRuntimeTestIcon").addEventListener("click", closeRuntimeTestDialog);

  byId("cancelModelConversionButton").addEventListener("click", () => {
    settleModelConversionConfirmation(null);
  });
  byId("skipModelConversionCacheButton").addEventListener("click", () => {
    settleModelConversionConfirmation("direct");
  });
  byId("confirmModelConversionButton").addEventListener("click", () => {
    settleModelConversionConfirmation("cache");
  });
  byId("modelConversionConfirmDialog").addEventListener("cancel", (event) => {
    event.preventDefault();
    settleModelConversionConfirmation(null);
  });

  byId("closeModelLoadingDialog").addEventListener("click", () => {
    closeModelLoadingDialog(true);
  });
  byId("modelLoadingDialog").addEventListener("cancel", (event) => {
    event.preventDefault();
    closeModelLoadingDialog(true);
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

  byId("toggleLogsButton").addEventListener("click", () => {
    const expanded = byId("toggleLogsButton").getAttribute("aria-expanded") !== "true";
    setLogsExpanded(expanded);
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

  byId("refreshDialog").addEventListener("cancel", (event) => event.preventDefault());

  byId("refreshButton").addEventListener("click", async () => {
    const button = byId("refreshButton");
    const minimumVisibleTime = openRefreshDialog();
    let resultMessage = "資料已更新";
    let resultType = "success";
    button.disabled = true;
    try {
      await loadSettings();
      await loadRuntime();
      await loadCommands(true);
      await Promise.all([loadModels(true), loadLogs()]);
    } catch (error) {
      resultMessage = error.message;
      resultType = "error";
    } finally {
      await closeRefreshDialog(minimumVisibleTime);
      button.disabled = false;
    }
    showMessage(resultMessage, resultType);
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
    selectMatchedMMProj(true);
    applyModelFeatureDefaults();
    renderModelMeta();
    renderMMProjMeta();
    updateRuntimeSpecificFields();
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
      renderKVCacheQuantizationControl(state.runtime);
      return;
    }
    byId("kvCacheQuantizationToggle").checked = false;
    toggle.disabled = true;
    try {
      // 勾選當下重新掃描目錄，避免使用已刪除的 Draft 清單快取。
      await loadModels(true);
      if (!matchedDraftModel()) {
        promptDraftDownload();
        return;
      }
      renderDFlashControl(state.runtime);
      renderKVCacheQuantizationControl(state.runtime);
    } catch (error) {
      toggle.checked = false;
      showMessage(error.message, "error");
      renderDFlashControl(state.runtime);
      renderKVCacheQuantizationControl(state.runtime);
    }
  });
  byId("mmapToggle").addEventListener("change", () => {
    state.selectionTouched = true;
    renderMMapControl(state.runtime);
  });
  byId("fastGGUFToggle").addEventListener("change", () => {
    state.selectionTouched = true;
    renderFastGGUFControl(state.runtime);
  });
  byId("kvCacheQuantizationToggle").addEventListener("change", () => {
    state.selectionTouched = true;
    if (byId("kvCacheQuantizationToggle").checked) {
      byId("dflashToggle").checked = false;
    }
    renderDFlashControl(state.runtime);
    renderKVCacheQuantizationControl(state.runtime);
  });
  byId("advancedSettingsButton").addEventListener("click", () => {
    const open = byId("runtimeAdvancedPopover").hidden;
    setAdvancedSettingsOpen(open);
  });
  byId("closeAdvancedSettingsButton").addEventListener("click", () => {
    setAdvancedSettingsOpen(false, true);
  });
  document.addEventListener("click", (event) => {
    if (!byId("runtimeAdvancedSettings").contains(event.target)) {
      setAdvancedSettingsOpen(false);
    }
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !byId("runtimeAdvancedPopover").hidden) {
      setAdvancedSettingsOpen(false, true);
    }
  });

  async function initialize() {
    const minimumVisibleTime = openRefreshDialog();
    let initializeError = null;
    try {
      await filterRuntimeOptionsForPlatform();
      await loadSettings();
      await loadRuntime();
      await loadCommands(false);
      await Promise.all([loadModels(false), loadLogs()]);
      window.setInterval(refreshRuntime, 2500);
    } catch (error) {
      initializeError = error;
    } finally {
      await closeRefreshDialog(minimumVisibleTime);
    }
    if (initializeError) showMessage(initializeError.message, "error");
  }

  window.addEventListener("beforeunload", (event) => {
    if (!state.calibrating) return;
    event.preventDefault();
    event.returnValue = "";
  });

  initialize();
})();
