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
  const cancellingJobs = new Set();

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
    updateOpenStorageButton();
  }

  async function loadSettings() {
    const settings = await api("/api/settings");
    byId("revision").placeholder = settings.default_revision || "main";
    storageDirectories = {
      gguf: String(settings.model_directory || "").trim(),
      mlx: String(settings.mlx_model_directory || "").trim()
    };
    updateOpenStorageButton();
    updateDownloadedModelDirectoryButtons();
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
      `${name}\n${formatBytes(cacheBytes)}\n\n${t("確定要清除此模型的轉換快取嗎？原始 GGUF 與模型目錄不會刪除；下次以 MLX 載入時需要重新轉換。")}`
    );
    if (!confirmed) return;
    button.disabled = true;
    button.textContent = t("正在清除快取…");
    try {
      await api("/api/models/conversion-cache", {
        method: "DELETE",
        body: JSON.stringify({ path: model.path })
      });
      downloadedModelsLoaded = false;
      showMessage(t("轉換快取已清除"));
      await loadDownloadedModels(true);
    } catch (error) {
      button.disabled = false;
      button.textContent = t("清除快取");
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
          clearCacheButton.textContent = t("清除快取");
          clearCacheButton.title = formatBytes(Number(model.conversion_cache_bytes || 0));
          clearCacheButton.setAttribute("aria-label", `${t("清除快取")} ${name.textContent}`);
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
    const filename = String(entry.filename || "").trim();
    const configuredSize = Number(entry.parameter_size_b);
    const parameterSizeB = Number.isFinite(configuredSize) && configuredSize > 0
      ? configuredSize
      : inferParameterSizeB(name);
    if (!name || !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) return null;
    if (!parameterSizeB) return null;
    if (format === "gguf" && (!filename || !filename.toLowerCase().endsWith(".gguf") || filename.includes("/"))) return null;
    return {
      name,
      repository,
      revision,
      filename: format === "gguf" ? filename : "",
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

  function applyQuickModel(format, model) {
    byId("downloadRuntime").value = format === "mlx" ? MLX_RUNTIME : LLAMA_RUNTIME;
    byId("repository").value = model.repository;
    byId("revision").value = model.revision;
    byId("filename").value = model.filename;
    renderRuntimeFields();
    closeQuickModelPopover();
    showMessage("已填入快速下載資訊");
  }

  function quickModelList(format, models) {
    const list = document.createElement("div");
    list.className = "quick-model-list";
    models.forEach((model) => {
      const button = document.createElement("button");
      button.className = "quick-model-option";
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
      button.addEventListener("click", () => applyQuickModel(format, model));
      list.append(button);
    });
    return list;
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
    content.replaceChildren(quickModelGroup(format, title, catalog[format]));
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
    if (quickModelCatalog && !byId("quickModelPopover").hidden) {
      renderQuickModels(quickModelCatalog);
    }
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
  byId("openStorageButton").addEventListener("click", () => {
    const format = byId("downloadRuntime").value === MLX_RUNTIME ? "mlx" : "gguf";
    openModelDirectory(format.toUpperCase());
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
