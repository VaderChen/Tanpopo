(() => {
  const { api, byId, showMessage, formatBytes, formatTime, getLanguage, t } = window.LlamaLoader;
  const LLAMA_RUNTIME = "llama-server";
  const MLX_RUNTIME = "mlx-server";
  const QUICK_MODEL_URL = "/assets/popular-models.json";
  const MODEL_SIZE_TIERS = [
    { id: "8b", label: "8B 級", upperBound: 20 },
    { id: "30b", label: "30B 級", upperBound: 70 },
    { id: "70b", label: "70B 以上", upperBound: Number.POSITIVE_INFINITY }
  ];
  const MODEL_NAME_COLLATOR = new Intl.Collator("en", { numeric: true, sensitivity: "base" });
  let quickModelCatalog = null;
  let downloadedModelsLoaded = false;
  let storageDirectories = { gguf: "", mlx: "" };
  let downloadFavorites = [];
  let ggufFileRequestSequence = 0;
  let ggufFileScanTimer = null;
  let repositorySearchSequence = 0;
  let appliedSearchRepository = "";
  const cancellingJobs = new Set();
  const REPOSITORY_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._-]*\/[A-Za-z0-9][A-Za-z0-9._-]*$/;
  const Q4_0_PATTERN = /(?:^|[._-])Q4_0(?:[._-]|$)/i;

  function nativeBridge() {
    return window.webkit?.messageHandlers?.tanpopoNative || null;
  }

  function isLoopbackInterface() {
    const hostname = String(window.location.hostname || "")
      .replace(/^\[|\]$/g, "")
      .toLowerCase();
    return hostname === "localhost" || hostname === "::1" || /^127(?:\.|$)/.test(hostname);
  }

  function localNativeBridge() {
    return isLoopbackInterface() ? nativeBridge() : null;
  }

  function modelDirectoryLabel(format) {
    return format === "MLX" ? t("開啟 MLX 模型目錄") : t("開啟 GGUF 模型目錄");
  }

  function updateDownloadedModelDirectoryButtons() {
    document.querySelectorAll("[data-open-model-directory]").forEach((button) => {
      const format = button.dataset.openModelDirectory;
      const available = Boolean(localNativeBridge() && storageDirectories[format.toLowerCase()]);
      button.disabled = !available;
      button.title = available
        ? modelDirectoryLabel(format)
        : t("僅能在 Tanpopo 本機桌面介面使用");
    });
  }

  function openModelDirectory(format) {
    const bridge = localNativeBridge();
    const path = storageDirectories[String(format || "").toLowerCase()];
    if (!bridge || !path) return;
    bridge.postMessage({ type: "open-model-directory", path });
  }

  function updateOpenStorageButton() {
    const button = byId("openStorageButton");
    const format = byId("downloadRuntime").value === MLX_RUNTIME ? "mlx" : "gguf";
    const available = Boolean(localNativeBridge() && storageDirectories[format]);
    button.disabled = !available;
    button.title = available ? t("開啟儲存位置") : t("僅能在 Tanpopo 本機桌面介面使用");
  }

  function renderRuntimeFields() {
    const isMLX = byId("downloadRuntime").value === MLX_RUNTIME;
    byId("filenameField").hidden = isMLX;
    byId("filename").required = !isMLX;
    byId("mlxDownloadHint").hidden = !isMLX;
    if (isMLX) {
      ggufFileRequestSequence += 1;
      byId("refreshGGUFFilesButton").disabled = true;
    } else {
      updateGGUFRefreshButton();
    }
    updateAddFavoriteButton();
    updateOpenStorageButton();
  }

  function repositoryLookupValues() {
    return {
      repository: byId("repository").value.trim(),
      revision: byId("revision").value.trim() || byId("revision").placeholder || "main"
    };
  }

  function canScanGGUFFiles() {
    const { repository, revision } = repositoryLookupValues();
    return byId("downloadRuntime").value !== MLX_RUNTIME
      && REPOSITORY_PATTERN.test(repository)
      && Boolean(revision)
      && !/[\r\n\0]/.test(revision);
  }

  function canAddDownloadFavorite() {
    const { repository, revision } = repositoryLookupValues();
    return REPOSITORY_PATTERN.test(repository) && Boolean(revision) && !/[\r\n\0]/.test(revision);
  }

  function updateAddFavoriteButton() {
    const button = byId("addDownloadFavoriteButton");
    if (!button) return;
    const { repository, revision } = repositoryLookupValues();
    const favorite = {
      runtime: byId("downloadRuntime").value,
      repository,
      revision
    };
    const alreadyFavorite = canAddDownloadFavorite()
      && downloadFavorites.some((entry) => entry.runtime === favorite.runtime
        && String(entry.repository || "").toLowerCase() === favorite.repository.toLowerCase()
        && entry.revision === favorite.revision);
    button.disabled = !canAddDownloadFavorite() || alreadyFavorite;
    button.classList.toggle("is-favorite", alreadyFavorite);
    button.querySelector("span").textContent = t(alreadyFavorite ? "已加入最愛" : "加入最愛");
  }

  function updateGGUFRefreshButton(loading = false) {
    byId("refreshGGUFFilesButton").disabled = loading || !canScanGGUFFiles();
  }

  function replaceGGUFFileOptions(label, disabled = true) {
    const option = document.createElement("option");
    option.value = "";
    option.textContent = t(label);
    byId("filename").replaceChildren(option);
    byId("filename").disabled = disabled;
  }

  function sortedGGUFFiles(files) {
    return [...new Set((Array.isArray(files) ? files : [])
      .map((filename) => String(filename || "").trim())
      .filter((filename) => filename.toLowerCase().endsWith(".gguf")))]
      .sort((left, right) => MODEL_NAME_COLLATOR.compare(left, right));
  }

  async function loadGGUFFilenames({ preserveSelection = false, notify = false } = {}) {
    if (!canScanGGUFFiles()) {
      replaceGGUFFileOptions("請先輸入 Repository");
      updateGGUFRefreshButton();
      return;
    }
    const { repository, revision } = repositoryLookupValues();
    const previousSelection = preserveSelection ? byId("filename").value : "";
    const requestSequence = ++ggufFileRequestSequence;
    replaceGGUFFileOptions("正在掃描 GGUF 檔案…");
    updateGGUFRefreshButton(true);
    try {
      const query = new URLSearchParams({ repository, revision });
      const payload = await api(`/api/downloads/repository-files?${query}`);
      if (requestSequence !== ggufFileRequestSequence) return;
      const files = sortedGGUFFiles(payload.files);
      if (!files.length) {
        replaceGGUFFileOptions("找不到可下載的 GGUF 主模型檔案");
        return;
      }
      const select = byId("filename");
      select.replaceChildren(...files.map((filename) => {
        const option = document.createElement("option");
        option.value = filename;
        option.textContent = filename;
        return option;
      }));
      select.disabled = false;
      const defaultFilename = files.find((filename) => Q4_0_PATTERN.test(filename)) || files[0];
      select.value = previousSelection && files.includes(previousSelection)
        ? previousSelection
        : defaultFilename;
      if (notify) showMessage(t("GGUF 檔案清單已更新"));
    } catch (error) {
      if (requestSequence !== ggufFileRequestSequence) return;
      replaceGGUFFileOptions("無法載入 GGUF 檔案清單");
      if (notify) showMessage(error.message, "error");
    } finally {
      if (requestSequence === ggufFileRequestSequence) updateGGUFRefreshButton();
    }
  }

  function scheduleGGUFFileScan() {
    window.clearTimeout(ggufFileScanTimer);
    if (!canScanGGUFFiles()) {
      ggufFileRequestSequence += 1;
      replaceGGUFFileOptions("請先輸入 Repository");
      updateGGUFRefreshButton();
      return;
    }
    ggufFileRequestSequence += 1;
    replaceGGUFFileOptions("正在掃描 GGUF 檔案…");
    updateGGUFRefreshButton(true);
    ggufFileScanTimer = window.setTimeout(() => {
      loadGGUFFilenames().catch((error) => showMessage(error.message, "error"));
    }, 450);
  }

  async function loadSettings() {
    const settings = await api("/api/settings");
    byId("revision").placeholder = settings.default_revision || "main";
    downloadFavorites = Array.isArray(settings.download_favorites) ? settings.download_favorites : [];
    storageDirectories = {
      gguf: String(settings.model_directory || "").trim(),
      mlx: String(settings.mlx_model_directory || "").trim()
    };
    updateOpenStorageButton();
    updateDownloadedModelDirectoryButtons();
    updateAddFavoriteButton();
    if (quickModelCatalog && !byId("quickModelPopover").hidden) renderQuickModels(quickModelCatalog);
  }

  async function loadDownloads() {
    const payload = await api("/api/downloads");
    renderDownloads(payload.downloads || []);
  }

  function downloadedModelName(path) {
    const normalized = String(path || "")
      .replace(/^gguf:/, "")
      .replace(/\\/g, "/")
      .replace(/\/+$/, "");
    return normalized.split("/").pop() || normalized || "—";
  }

  async function deleteDownloadedModel(format, model, button) {
    const name = downloadedModelName(model.path);
    const relativePath = String(model.path || "").replace(/^gguf:/, "").replace(/\\/g, "/");
    const removesDirectory = format === "MLX" || relativePath.includes("/");
    const warning = removesDirectory
      ? t("確定要刪除此模型的完整目錄嗎？目錄內所有檔案都會從硬碟移除，且無法復原。")
      : t("確定要刪除此模型嗎？模型檔案將從硬碟移除，且無法復原。");
    const confirmed = window.confirm(
      `${name}\n${formatBytes(Number(model.size || 0))}\n\n${warning}`
    );
    if (!confirmed) return;
    button.disabled = true;
    button.textContent = t("刪除中…");
    try {
      await api("/api/models", {
        method: "DELETE",
        body: JSON.stringify({ format: format.toLowerCase(), path: model.path })
      });
      downloadedModelsLoaded = false;
      showMessage(t("模型已刪除"));
      await loadDownloadedModels(true);
    } catch (error) {
      button.disabled = false;
      button.textContent = t("刪除");
      throw error;
    }
  }

  async function clearDownloadedModelConversionCache(model, button) {
    const name = downloadedModelName(model.path);
    const cacheBytes = Number(model.conversion_cache_bytes || 0);
    const confirmed = window.confirm(
      `${name}\n${formatBytes(cacheBytes)}\n\n${t("確定要移除此模型的 Fast GGUF 嗎？原始 GGUF 與模型目錄不會刪除；下次以 MLX 載入時需要重新建立 Fast GGUF。")}`
    );
    if (!confirmed) return;
    button.disabled = true;
    button.textContent = t("正在移除 Fast GGUF…");
    try {
      await api("/api/models/conversion-cache", {
        method: "DELETE",
        body: JSON.stringify({ path: model.path })
      });
      downloadedModelsLoaded = false;
      showMessage(t("Fast GGUF 已移除"));
      await loadDownloadedModels(true);
    } catch (error) {
      button.disabled = false;
      button.textContent = t("移除 Fast GGUF");
      throw error;
    }
  }

  function downloadedModelGroup(format, models) {
    const section = document.createElement("section");
    section.className = "downloaded-model-group";
    const heading = document.createElement("div");
    heading.className = "downloaded-model-group-heading";
    const headingLabel = document.createElement("div");
    headingLabel.className = "downloaded-model-group-label";
    const title = document.createElement("h4");
    title.textContent = format;
    const count = document.createElement("span");
    count.className = "downloaded-model-group-count";
    count.textContent = `${models.length} ${t("個模型")}`;
    const openDirectoryButton = document.createElement("button");
    openDirectoryButton.className = "downloaded-model-directory-button";
    openDirectoryButton.type = "button";
    openDirectoryButton.dataset.openModelDirectory = format;
    openDirectoryButton.setAttribute("aria-label", modelDirectoryLabel(format));
    openDirectoryButton.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><path d="M3.5 6.75A1.75 1.75 0 0 1 5.25 5h4.1l1.8 2h7.6a1.75 1.75 0 0 1 1.75 1.75v8.5A1.75 1.75 0 0 1 18.75 19H5.25a1.75 1.75 0 0 1-1.75-1.75V6.75Z"></path></svg>';
    openDirectoryButton.addEventListener("click", () => openModelDirectory(format));
    headingLabel.append(openDirectoryButton, title);
    heading.append(headingLabel, count);
    section.append(heading);

    const list = document.createElement("div");
    list.className = "downloaded-model-list";
    if (!models.length) {
      list.classList.add("is-empty");
      const empty = document.createElement("div");
      empty.className = "downloaded-model-empty";
      const icon = document.createElement("span");
      icon.className = "downloaded-model-empty-icon";
      icon.setAttribute("aria-hidden", "true");
      icon.innerHTML = '<svg viewBox="0 0 24 24"><path d="M4.5 7.25A1.75 1.75 0 0 1 6.25 5.5h3.4l1.8 2h6.3a1.75 1.75 0 0 1 1.75 1.75v7.5a1.75 1.75 0 0 1-1.75 1.75H6.25a1.75 1.75 0 0 1-1.75-1.75v-9.5Z"></path><path d="M8.5 13h7"></path></svg>';
      const title = document.createElement("strong");
      title.textContent = t("尚未下載模型");
      const hint = document.createElement("span");
      hint.textContent = t("下載完成後會顯示在這裡。");
      empty.append(icon, title, hint);
      list.append(empty);
    } else {
      models.forEach((model) => {
        const item = document.createElement("article");
        item.className = "downloaded-model-item";
        const itemHeading = document.createElement("div");
        itemHeading.className = "downloaded-model-item-heading";
        const name = document.createElement("strong");
        name.textContent = downloadedModelName(model.path);
        const actions = document.createElement("div");
        actions.className = "downloaded-model-item-actions";
        const buttons = document.createElement("div");
        buttons.className = "downloaded-model-item-buttons";
        if (format === "GGUF" && model.conversion_cached) {
          const clearCacheButton = document.createElement("button");
          clearCacheButton.className = "button secondary compact downloaded-model-conversion-delete";
          clearCacheButton.type = "button";
          clearCacheButton.textContent = t("移除 Fast GGUF");
          clearCacheButton.title = formatBytes(Number(model.conversion_cache_bytes || 0));
          clearCacheButton.setAttribute("aria-label", `${t("移除 Fast GGUF")} ${name.textContent}`);
          clearCacheButton.addEventListener("click", () => {
            clearDownloadedModelConversionCache(model, clearCacheButton)
              .catch((error) => showMessage(error.message, "error"));
          });
          buttons.append(clearCacheButton);
        }
        const deleteButton = document.createElement("button");
        deleteButton.className = "button danger compact downloaded-model-delete";
        deleteButton.type = "button";
        deleteButton.textContent = t("刪除");
        deleteButton.setAttribute("aria-label", `${t("刪除")} ${name}`);
        deleteButton.addEventListener("click", () => {
          deleteDownloadedModel(format, model, deleteButton)
            .catch((error) => showMessage(error.message, "error"));
        });
        buttons.append(deleteButton);
        actions.append(buttons);
        itemHeading.append(name, actions);
        const metaRow = document.createElement("div");
        metaRow.className = "downloaded-model-meta-row";
        const meta = document.createElement("p");
        meta.className = "muted";
        meta.textContent = `${formatBytes(Number(model.size || 0))} · ${t("修改於")} ${formatTime(model.modified_at)}`;
        metaRow.append(meta);
        item.append(itemHeading, metaRow);
        list.append(item);
      });
    }
    section.append(list);
    return section;
  }

  function renderDownloadedModels(models) {
    const mainModels = models.filter((model) => {
      const path = String(model.path || "").replace(/^gguf:/, "").replace(/\\/g, "/");
      const filename = path.split("/").pop() || "";
      return !/(^|[_.-])mmproj([_.-]|$)/i.test(filename)
        && !path.split("/").some((segment) => segment.startsWith(".tanpopo-"));
    });
    const groups = {
      MLX: mainModels.filter((model) => String(model.format).toLowerCase() === "mlx"),
      GGUF: mainModels.filter((model) => String(model.format).toLowerCase() === "gguf")
    };
    Object.values(groups).forEach((items) => items.sort((left, right) =>
      MODEL_NAME_COLLATOR.compare(String(left.path || ""), String(right.path || ""))
    ));
    byId("downloadedModels").replaceChildren(
      downloadedModelGroup("MLX", groups.MLX),
      downloadedModelGroup("GGUF", groups.GGUF)
    );
    updateDownloadedModelDirectoryButtons();
  }

  async function loadDownloadedModels(force = false) {
    if (downloadedModelsLoaded && !force) return;
    const button = byId("refreshDownloadedModelsButton");
    button.disabled = true;
    if (!downloadedModelsLoaded) {
      const loading = document.createElement("p");
      loading.className = "empty-state";
      loading.textContent = t("模型清單讀取中…");
      byId("downloadedModels").replaceChildren(loading);
    }
    try {
      const payload = await api(`/api/models?runtime=${encodeURIComponent(MLX_RUNTIME)}`);
      renderDownloadedModels(Array.isArray(payload.models) ? payload.models : []);
      downloadedModelsLoaded = true;
    } finally {
      button.disabled = false;
    }
  }

  function activateDownloadPane(paneID, updateHash = true) {
    const tabs = [...document.querySelectorAll("[data-download-target]")];
    tabs.forEach((tab) => {
      const active = tab.dataset.downloadTarget === paneID;
      tab.classList.toggle("active", active);
      tab.setAttribute("aria-selected", String(active));
      byId(tab.dataset.downloadTarget).hidden = !active;
    });
    if (updateHash) {
      history.replaceState(null, "", paneID === "downloadedModelsPane"
        ? "#downloaded-models"
        : "#download-center");
    }
    if (paneID === "downloadedModelsPane") {
      loadDownloadedModels().catch((error) => showMessage(error.message, "error"));
    }
  }

  function localizedCatalogText(value) {
    if (typeof value === "string") return value;
    if (!value || typeof value !== "object") return "";
    const language = getLanguage().resolved;
    return String(value[language] || value.en || value["zh-Hant"] || "").trim();
  }

  function inferParameterSizeB(name) {
    const sizes = Array.from(String(name || "").matchAll(/(?:^|[^A-Za-z0-9])(?:A|E)?(\d+(?:\.\d+)?)B\b/gi))
      .map((match) => Number(match[1]))
      .filter((value) => Number.isFinite(value) && value > 0);
    return sizes.length ? Math.max(...sizes) : 0;
  }

  function validateCatalogEntry(entry, format) {
    if (!entry || typeof entry !== "object") return null;
    const name = String(entry.name || "").trim();
    const repository = String(entry.repository || "").trim();
    const revision = String(entry.revision || "main").trim() || "main";
    const configuredSize = Number(entry.parameter_size_b);
    const parameterSizeB = Number.isFinite(configuredSize) && configuredSize > 0
      ? configuredSize
      : inferParameterSizeB(name);
    if (!name || !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) return null;
    if (!parameterSizeB) return null;
    return {
      name,
      repository,
      revision,
      parameterSizeB,
      description: localizedCatalogText(entry.description)
    };
  }

  function normalizeCatalog(payload) {
    if (!payload || payload.schema_version !== 1) throw new Error(t("不支援的常用模型清單格式"));
    const normalizeGroup = (format) => (Array.isArray(payload[format]) ? payload[format] : [])
      .map((entry) => validateCatalogEntry(entry, format))
      .filter(Boolean);
    return { gguf: normalizeGroup("gguf"), mlx: normalizeGroup("mlx") };
  }

  async function applyQuickModel(format, model) {
    byId("downloadRuntime").value = format === "mlx" ? MLX_RUNTIME : LLAMA_RUNTIME;
    byId("repository").value = model.repository;
    byId("revision").value = model.revision;
    renderRuntimeFields();
    closeQuickModelPopover();
    if (format === "gguf") await loadGGUFFilenames();
    showMessage(t("已填入快速下載資訊"));
  }

  function favoriteForModel(format, model) {
    return {
      runtime: format === "mlx" ? MLX_RUNTIME : LLAMA_RUNTIME,
      repository: String(model.repository || "").trim(),
      revision: String(model.revision || "main").trim() || "main"
    };
  }

  function isFavoriteModel(format, model) {
    const favorite = favoriteForModel(format, model);
    return downloadFavorites.some((entry) => entry.runtime === favorite.runtime
      && String(entry.repository || "").toLowerCase() === favorite.repository.toLowerCase()
      && entry.revision === favorite.revision);
  }

  async function addDownloadFavorite(favorite) {
    const payload = await api("/api/download-favorites", {
      method: "POST",
      body: JSON.stringify(favorite)
    });
    downloadFavorites = Array.isArray(payload.favorites) ? payload.favorites : [];
    updateAddFavoriteButton();
    if (quickModelCatalog && !byId("quickModelPopover").hidden) renderQuickModels(quickModelCatalog);
    showMessage(t("已加入我的最愛"));
  }

  async function addCurrentDownloadFavorite() {
    const { repository, revision } = repositoryLookupValues();
    await addDownloadFavorite({
      runtime: byId("downloadRuntime").value,
      repository,
      revision
    });
  }

  function favoriteStarIcon(filled) {
    return `<svg viewBox="0 0 24 24" aria-hidden="true"${filled ? ' class="is-filled"' : ""}><path d="m12 3 2.8 5.7 6.2.9-4.5 4.4 1.1 6.2-5.6-3-5.6 3 1.1-6.2L3 9.6l6.2-.9L12 3Z"></path></svg>`;
  }

  function quickModelList(format, models) {
    const list = document.createElement("div");
    list.className = "quick-model-list";
    models.forEach((model) => {
      const item = document.createElement("div");
      item.className = "quick-model-option";
      const button = document.createElement("button");
      button.className = "quick-model-select";
      button.type = "button";
      const name = document.createElement("strong");
      name.textContent = model.name;
      const repository = document.createElement("code");
      repository.textContent = model.repository;
      button.append(name, repository);
      if (model.description) {
        const description = document.createElement("span");
        description.textContent = model.description;
        button.append(description);
      }
      button.addEventListener("click", () => {
        applyQuickModel(format, model).catch((error) => showMessage(error.message, "error"));
      });
      const favoriteButton = document.createElement("button");
      favoriteButton.className = "quick-model-star";
      favoriteButton.type = "button";
      favoriteButton.setAttribute("aria-label", t("加入我的最愛"));
      favoriteButton.title = t("加入我的最愛");
      favoriteButton.innerHTML = favoriteStarIcon(false);
      favoriteButton.addEventListener("click", () => {
        addDownloadFavorite(favoriteForModel(format, model))
          .catch((error) => showMessage(error.message, "error"));
      });
      item.append(button, favoriteButton);
      list.append(item);
    });
    return list;
  }

  function favoriteFormat(favorite) {
    return favorite.runtime === MLX_RUNTIME ? "mlx" : "gguf";
  }

  async function removeDownloadFavorite(favorite) {
    const confirmed = window.confirm(
      `${favorite.repository}\n${favorite.revision}\n\n${t("確定要從我的最愛移除嗎？")}`
    );
    if (!confirmed) return;
    const payload = await api("/api/download-favorites", {
      method: "DELETE",
      body: JSON.stringify(favorite)
    });
    downloadFavorites = Array.isArray(payload.favorites) ? payload.favorites : [];
    updateAddFavoriteButton();
    renderQuickModels(quickModelCatalog);
    showMessage(t("已從我的最愛移除"));
  }

  function quickFavoriteGroup(format) {
    const section = document.createElement("section");
    section.className = "quick-model-group quick-favorite-group";
    const heading = document.createElement("h4");
    heading.textContent = t("我的最愛");
    section.append(heading);
    const favorites = downloadFavorites
      .filter((favorite) => favoriteFormat(favorite) === format)
      .sort((left, right) => MODEL_NAME_COLLATOR.compare(
        `${left.repository}@${left.revision}`,
        `${right.repository}@${right.revision}`
      ));
    if (!favorites.length) {
      const empty = document.createElement("p");
      empty.className = "quick-model-empty";
      empty.textContent = t("尚未加入常用模型。");
      section.append(empty);
      return section;
    }
    const list = document.createElement("div");
    list.className = "quick-model-list";
    favorites.forEach((favorite) => {
      const catalogModel = (quickModelCatalog?.[format] || []).find((model) =>
        model.repository.toLowerCase() === favorite.repository.toLowerCase()
          && model.revision === favorite.revision
      );
      const item = document.createElement("div");
      item.className = "quick-model-option quick-favorite-option";
      const button = document.createElement("button");
      button.className = "quick-model-select";
      button.type = "button";
      const content = document.createElement("div");
      content.className = "quick-favorite-content";
      const name = document.createElement("strong");
      name.textContent = catalogModel?.name || favorite.repository.split("/").pop() || favorite.repository;
      const repository = document.createElement("code");
      repository.textContent = `${favorite.repository} · ${favorite.revision}`;
      content.append(name, repository);
      if (catalogModel?.description) {
        const description = document.createElement("span");
        description.textContent = catalogModel.description;
        content.append(description);
      }
      button.append(content);
      button.addEventListener("click", () => {
        applyQuickModel(format, favorite).catch((error) => showMessage(error.message, "error"));
      });
      const removeButton = document.createElement("button");
      removeButton.className = "quick-model-star is-favorite";
      removeButton.type = "button";
      removeButton.setAttribute("aria-label", t("從我的最愛移除"));
      removeButton.title = t("從我的最愛移除");
      removeButton.innerHTML = favoriteStarIcon(true);
      removeButton.addEventListener("click", () => {
        removeDownloadFavorite(favorite).catch((error) => showMessage(error.message, "error"));
      });
      item.append(button, removeButton);
      list.append(item);
    });
    section.append(list);
    return section;
  }

  function modelSizeTier(parameterSizeB) {
    return MODEL_SIZE_TIERS.find((tier) => parameterSizeB < tier.upperBound)
      || MODEL_SIZE_TIERS[MODEL_SIZE_TIERS.length - 1];
  }

  function quickModelGroup(format, title, models) {
    const section = document.createElement("section");
    section.className = "quick-model-group";
    const heading = document.createElement("h4");
    heading.textContent = title;
    section.append(heading);
    if (!models.length) {
      const empty = document.createElement("p");
      empty.className = "quick-model-empty";
      empty.textContent = t("目前沒有可用的常用模型。");
      section.append(empty);
      return section;
    }
    MODEL_SIZE_TIERS.forEach((tier) => {
      const tierModels = models
        .filter((model) => modelSizeTier(model.parameterSizeB).id === tier.id)
        .sort((left, right) => MODEL_NAME_COLLATOR.compare(left.name, right.name));
      if (!tierModels.length) return;
      const tierSection = document.createElement("section");
      tierSection.className = "quick-model-tier";
      const tierHeading = document.createElement("h5");
      tierHeading.textContent = t(tier.label);
      tierSection.append(tierHeading, quickModelList(format, tierModels));
      section.append(tierSection);
    });
    return section;
  }

  function renderQuickModels(catalog) {
    const content = byId("quickModelContent");
    const format = byId("downloadRuntime").value === MLX_RUNTIME ? "mlx" : "gguf";
    const title = format === "mlx" ? t("MLX 模型") : t("GGUF 模型");
    const availableModels = catalog[format].filter((model) => !isFavoriteModel(format, model));
    content.replaceChildren(
      quickFavoriteGroup(format),
      quickModelGroup(format, title, availableModels)
    );
  }

  async function loadQuickModels() {
    if (quickModelCatalog) {
      renderQuickModels(quickModelCatalog);
      return;
    }
    const response = await fetch(QUICK_MODEL_URL, {
      cache: "no-store",
      headers: { "Accept": "application/json" }
    });
    if (!response.ok) throw new Error(`${t("請求失敗")} (${response.status})`);
    quickModelCatalog = normalizeCatalog(await response.json());
    renderQuickModels(quickModelCatalog);
  }

  function closeQuickModelPopover() {
    byId("quickModelPopover").hidden = true;
    byId("quickModelButton").setAttribute("aria-expanded", "false");
  }

  async function openQuickModelPopover() {
    byId("quickModelPopover").hidden = false;
    byId("quickModelButton").setAttribute("aria-expanded", "true");
    try {
      await loadQuickModels();
    } catch (error) {
      const status = document.createElement("p");
      status.className = "quick-model-status error";
      status.textContent = `${t("無法載入常用模型清單")}：${error.message}`;
      byId("quickModelContent").replaceChildren(status);
    }
  }

  function setRepositorySearchStatus(message, type = "") {
    const status = byId("repositorySearchStatus");
    status.textContent = message;
    status.className = `repository-search-status${type ? ` ${type}` : ""}`;
  }

  function repositorySearchMetric(value, label) {
    const count = Number(value || 0);
    if (!Number.isFinite(count) || count <= 0) return "";
    return `${new Intl.NumberFormat(getLanguage().resolved, { notation: "compact", maximumFractionDigits: 1 }).format(count)} ${t(label)}`;
  }

  async function applyRepositorySearchResult(result) {
    byId("repository").value = result.repository;
    byId("revision").value = "main";
    appliedSearchRepository = result.repository;
    renderRuntimeFields();
    renderRepositorySearchResults(repositorySearchResults);
    updateAddFavoriteButton();
    setRepositorySearchStatus(`${t("已選用 Repository")}：${result.repository}`, "success");
    closeRepositorySearchDialog();
    if (byId("downloadRuntime").value === LLAMA_RUNTIME) await loadGGUFFilenames();
  }

  let repositorySearchResults = [];

  function sortedRepositorySearchResults(results) {
    const mode = byId("repositorySearchSort").value;
    return [...results].sort((left, right) => {
      if (mode === "downloads") {
        const difference = Number(right.downloads || 0) - Number(left.downloads || 0);
        if (difference) return difference;
      } else if (mode === "likes") {
        const difference = Number(right.likes || 0) - Number(left.likes || 0);
        if (difference) return difference;
      }
      return MODEL_NAME_COLLATOR.compare(left.repository, right.repository);
    });
  }

  function renderRepositorySearchResults(results) {
    const container = byId("repositorySearchResults");
    container.replaceChildren();
    sortedRepositorySearchResults(results).forEach((result) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "repository-search-result";
      button.classList.toggle("is-applied", result.repository === appliedSearchRepository);
      const copy = document.createElement("span");
      copy.className = "repository-search-result-copy";
      const name = document.createElement("strong");
      name.textContent = result.repository.split("/").pop() || result.repository;
      const repository = document.createElement("code");
      repository.textContent = result.repository;
      const meta = document.createElement("span");
      meta.className = "repository-search-result-meta";
      const metrics = [
        repositorySearchMetric(result.downloads, "次下載"),
        repositorySearchMetric(result.likes, "個讚")
      ].filter(Boolean);
      if (result.last_modified) metrics.push(`${t("更新於")} ${formatTime(result.last_modified)}`);
      metrics.push(`Revision · ${result.revision || "main"}`);
      meta.textContent = metrics.join(" · ");
      copy.append(name, repository, meta);
      const action = document.createElement("span");
      action.className = "repository-search-result-action";
      action.textContent = t(result.repository === appliedSearchRepository ? "已選用" : "選用");
      button.append(copy, action);
      button.addEventListener("click", () => {
        applyRepositorySearchResult(result).catch((error) => setRepositorySearchStatus(error.message, "error"));
      });
      container.append(button);
    });
  }

  async function searchRepositories() {
    const keyword = byId("repositorySearchKeyword").value.trim();
    if (!keyword) {
      setRepositorySearchStatus(t("請輸入搜尋關鍵字"), "error");
      byId("repositorySearchKeyword").focus();
      return;
    }
    const requestSequence = ++repositorySearchSequence;
    const button = byId("submitRepositorySearchButton");
    button.disabled = true;
    button.textContent = t("搜尋中…");
    setRepositorySearchStatus(t("正在搜尋 Hugging Face Repository…"));
    try {
      const query = new URLSearchParams({
        query: keyword,
        runtime: byId("downloadRuntime").value
      });
      const payload = await api(`/api/downloads/repositories?${query}`);
      if (requestSequence !== repositorySearchSequence) return;
      repositorySearchResults = Array.isArray(payload.repositories) ? payload.repositories : [];
      appliedSearchRepository = "";
      renderRepositorySearchResults(repositorySearchResults);
      setRepositorySearchStatus(repositorySearchResults.length
        ? `${t("找到 Repository")}：${repositorySearchResults.length}`
        : t("找不到符合的 Repository。"));
    } catch (error) {
      if (requestSequence !== repositorySearchSequence) return;
      repositorySearchResults = [];
      renderRepositorySearchResults(repositorySearchResults);
      setRepositorySearchStatus(`${t("Repository 搜尋失敗")}：${error.message}`, "error");
    } finally {
      if (requestSequence === repositorySearchSequence) {
        button.disabled = false;
        button.textContent = t("搜尋");
      }
    }
  }

  function openRepositorySearchDialog() {
    closeQuickModelPopover();
    const dialog = byId("repositorySearchDialog");
    if (!dialog.open) dialog.showModal();
    window.requestAnimationFrame(() => byId("repositorySearchKeyword").focus());
  }

  function closeRepositorySearchDialog() {
    const dialog = byId("repositorySearchDialog");
    if (dialog.open) dialog.close();
  }

  function renderDownloads(jobs) {
    const container = byId("downloadJobs");
    container.replaceChildren();
    const visibleJobs = jobs.filter((job) => job.state !== "completed");
    if (!visibleJobs.length) {
      const empty = document.createElement("p");
      empty.className = "empty-state";
      empty.textContent = t("目前沒有下載工作。");
      container.append(empty);
      return;
    }
    visibleJobs.slice(0, 8).forEach((job) => {
      const item = document.createElement("article");
      item.className = `job-item ${job.state}`;
      const header = document.createElement("div");
      header.className = "job-header";
      const name = document.createElement("strong");
      name.textContent = `${job.runtime === MLX_RUNTIME ? "MLX" : "GGUF"} · ${job.filename}`;
      const stateLabel = document.createElement("span");
      stateLabel.className = "job-state";
      stateLabel.textContent = t(({ queued: "等待中", downloading: "下載中", cancelling: "取消中…", completed: "完成", failed: "失敗" })[job.state] || job.state);
      const actions = document.createElement("div");
      actions.className = "job-actions";
      actions.append(stateLabel);
      if (job.state === "queued" || job.state === "downloading" || job.state === "cancelling") {
        const cancelButton = document.createElement("button");
        cancelButton.className = "job-cancel-button";
        cancelButton.type = "button";
        cancelButton.textContent = t("取消");
        cancelButton.disabled = job.state === "cancelling" || cancellingJobs.has(job.id);
        cancelButton.addEventListener("click", async () => {
          cancellingJobs.add(job.id);
          cancelButton.disabled = true;
          stateLabel.textContent = t("取消中…");
          try {
            await api(`/api/downloads/${encodeURIComponent(job.id)}`, { method: "DELETE" });
            showMessage("下載已取消");
          } catch (error) {
            showMessage(error.message, "error");
          } finally {
            cancellingJobs.delete(job.id);
            await loadDownloads();
          }
        });
        actions.append(cancelButton);
      }
      header.append(name, actions);
      const progress = document.createElement("div");
      progress.className = "progress-track";
      const bar = document.createElement("div");
      bar.className = "progress-bar";
      const percent = job.bytes_total > 0 ? Math.min(100, (job.bytes_done / job.bytes_total) * 100) : 0;
      bar.style.width = `${job.state === "completed" ? 100 : percent}%`;
      progress.append(bar);
      const meta = document.createElement("div");
      meta.className = "job-meta";
      meta.textContent = job.state === "failed"
        ? job.error
        : `${formatBytes(job.bytes_done)} / ${formatBytes(job.bytes_total)}`;
      item.append(header, progress, meta);
      container.append(item);
    });
  }

  byId("downloadForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = byId("downloadButton");
    button.disabled = true;
    try {
      const result = await api("/api/downloads", {
        method: "POST",
        body: JSON.stringify({
          runtime: byId("downloadRuntime").value,
          repository: byId("repository").value.trim(),
          filename: byId("filename").value.trim(),
          revision: byId("revision").value.trim(),
          overwrite: byId("overwrite").checked
        })
      });
      const requestedRepository = byId("repository").value.trim();
      const companions = (result.jobs || []).slice(1).map((job) =>
        job.repository && job.repository !== requestedRepository
          ? `${job.repository}/${job.filename}`
          : job.filename
      );
      const skipped = result.skipped_companions || [];
      const warnings = result.warnings || [];
      const details = [];
      const isMLX = byId("downloadRuntime").value === MLX_RUNTIME;
      if (companions.length) {
        details.push(isMLX
          ? `另有 ${companions.length} 個模型檔案一併下載`
          : `一併下載：${companions.join("、")}`);
      }
      if (skipped.length) details.push(`本機已存在：${skipped.join("、")}`);
      if (warnings.length) details.push(warnings.join("；"));
      showMessage(`模型已加入下載佇列${details.length ? `；${details.join("；")}` : (isMLX ? "" : "；未發現附屬 GGUF")}`);
      await loadDownloads();
    } catch (error) {
      showMessage(error.message, "error");
    } finally {
      button.disabled = false;
    }
  });

  async function initialize() {
    try {
      await Promise.all([loadSettings(), loadDownloads()]);
      window.setInterval(async () => {
        try {
          await loadDownloads();
        } catch (error) {
          if (!String(error.message).includes("登入狀態")) showMessage(error.message, "error");
        }
      }, 2500);
    } catch (error) {
      showMessage(error.message, "error");
    }
  }

  byId("downloadRuntime").addEventListener("change", () => {
    renderRuntimeFields();
    if (byId("downloadRuntime").value === LLAMA_RUNTIME) scheduleGGUFFileScan();
    if (quickModelCatalog && !byId("quickModelPopover").hidden) {
      renderQuickModels(quickModelCatalog);
    }
  });
  ["repository", "revision"].forEach((fieldID) => {
    byId(fieldID).addEventListener("input", () => {
      updateAddFavoriteButton();
      if (byId("downloadRuntime").value === LLAMA_RUNTIME) scheduleGGUFFileScan();
    });
  });
  document.querySelectorAll("[data-download-target]").forEach((tab) => {
    tab.addEventListener("click", () => activateDownloadPane(tab.dataset.downloadTarget));
  });
  byId("refreshDownloadedModelsButton").addEventListener("click", () => {
    loadDownloadedModels(true).catch((error) => showMessage(error.message, "error"));
  });
  byId("quickModelButton").addEventListener("click", () => {
    if (byId("quickModelPopover").hidden) openQuickModelPopover();
    else closeQuickModelPopover();
  });
  byId("repositorySearchButton").addEventListener("click", openRepositorySearchDialog);
  byId("closeRepositorySearchButton").addEventListener("click", closeRepositorySearchDialog);
  byId("repositorySearchForm").addEventListener("submit", (event) => {
    event.preventDefault();
    searchRepositories().catch((error) => setRepositorySearchStatus(error.message, "error"));
  });
  byId("repositorySearchSort").addEventListener("change", () => {
    renderRepositorySearchResults(repositorySearchResults);
  });
  byId("repositorySearchDialog").addEventListener("close", () => {
    byId("repositorySearchButton").focus();
  });
  byId("openStorageButton").addEventListener("click", () => {
    const format = byId("downloadRuntime").value === MLX_RUNTIME ? "mlx" : "gguf";
    openModelDirectory(format.toUpperCase());
  });
  byId("refreshGGUFFilesButton").addEventListener("click", () => {
    loadGGUFFilenames({ preserveSelection: true, notify: true })
      .catch((error) => showMessage(error.message, "error"));
  });
  byId("addDownloadFavoriteButton").addEventListener("click", () => {
    addCurrentDownloadFavorite().catch((error) => showMessage(error.message, "error"));
  });
  byId("closeQuickModelButton").addEventListener("click", closeQuickModelPopover);
  document.addEventListener("click", (event) => {
    if (!byId("quickModelPicker").contains(event.target)) closeQuickModelPopover();
  });
  document.addEventListener("keydown", (event) => {
    if (event.key === "Escape" && !byId("quickModelPopover").hidden) {
      closeQuickModelPopover();
      byId("quickModelButton").focus();
    }
  });
  renderRuntimeFields();
  activateDownloadPane(
    window.location.hash === "#downloaded-models" ? "downloadedModelsPane" : "downloadCenterPane",
    false
  );
  initialize();
})();
