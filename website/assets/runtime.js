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

  function preferredDFlashVariant() {
    const command = selectedCommand();
    const description = [command?.id, command?.name, ...(command?.extra_args || [])].join(" ").toLowerCase();
    return /dflash[\s_-]*2|block(?:-size)?[\s_-]*8/.test(description) ? "dflash2" : "";
  }

  function matchedDraftModel() {
    const command = selectedCommand();
    const wantsMTP = commandUsesMTP(command);
    const candidates = state.draftModels.filter(wantsMTP ? isMTPDraftModel : isDFlashDraftModel);
    const configuredPath = String(command?.draft_model || "").trim();
    if (configuredPath) {
      return candidates.find((model) => model.path === configuredPath) || null;
    }
    const target = selectedModel();
    if (!target || !candidates.length) return null;
    if (wantsMTP && target.mtp_embedded) return null;
    if (wantsMTP ? !target.mtp_supported : !target.dflash_supported) return null;
    const targetDirectory = pathDirectory(target.path);
    const targetTokens = new Set(modelTokens(target.path));
    const preferredVariant = preferredDFlashVariant();
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
    if (loading) {
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
    byId("statusDetail").textContent = running
      ? (ready
        ? `啟動時間 ${formatTime(status.started_at)} · ${status.startup_command_name || "未命名參數"}`
        : `${t("模型載入完成後即可測試。")} · ${status.startup_command_name || "未命名參數"}`)
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
    byId("runtimeSelect").disabled = running;
    byId("commandSelect").disabled = running || !commandReady;
    byId("modelSelect").disabled = running || !commandReady || !state.mainModels.length;
    byId("mmprojSelect").disabled = running
      || (selectedRuntime() === MLX_RUNTIME && !isMLXGGUFModel(selectedModel()))
      || !state.mmprojModels.length;
    byId("startButton").disabled = running || !modelReady || !commandReady;
    byId("stopButton").disabled = !running && !status.desired_running;
    // 測試本身就是 Runtime 的可用性檢查；只要程序仍在執行就應允許
    // 使用者觸發，避免健康端點受 Access Key 保護時永遠無法測試。
    byId("testRuntimeButton").disabled = !running || state.testing;
    byId("testRuntimeButton").textContent = state.testing ? t("測試中…") : t("測試");
    byId("repeatRuntimeTestButton").disabled = !running || state.testing;
    byId("longRuntimeTestButton").disabled = !running || state.testing;
    renderDFlashControl(status);
    renderFastGGUFControl(status);
    renderMMapControl(status);
    renderKVCacheQuantizationControl(status);
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
        "正在檢查轉換快取",
        "正在確認此模型是否已有可重用的轉換權重。"
      ],
      converting: [
        "正在轉換模型",
        "此模型需要轉換並建立永久快取；完成前請勿關閉程式。"
      ],
      saving_cache: [
        "正在建立轉換快取",
        "模型已完成轉換，正在寫入永久快取；後續載入可直接重用。"
      ],
      loading_cache: [
        "正在載入轉換快取",
        "已找到轉換完成的永久快取，本次不需要重新轉換。"
      ],
      direct_loading: [
        "正在直接載入模型",
        "本次不建立永久快取；正在從 GGUF 即時準備 MLX 執行所需的權重。"
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
    if (!state.runtime?.running || state.testing) return;
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
      await api("/api/runtime/start", {
        method: "POST",
        body: JSON.stringify({
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

  initialize();
})();
