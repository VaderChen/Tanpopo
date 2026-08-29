(() => {
  const { api, byId, showMessage, formatBytes, getLanguage, t } = window.LlamaLoader;
  const LLAMA_RUNTIME = "llama-server";
  const MLX_RUNTIME = "mlx-server";
  const QUICK_MODEL_URL = "/assets/popular-models.json";
  let quickModelCatalog = null;
  let storageDirectories = { gguf: "", mlx: "" };

  function nativeBridge() {
    return window.webkit?.messageHandlers?.tanpopoNative || null;
  }

  function updateOpenStorageButton() {
    const button = byId("openStorageButton");
    const format = byId("downloadRuntime").value === MLX_RUNTIME ? "mlx" : "gguf";
    const available = Boolean(nativeBridge() && storageDirectories[format]);
    button.disabled = !available;
    button.title = available ? t("開啟儲存位置") : t("僅能在 Tanpopo 桌面介面使用");
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
  }

  async function loadDownloads() {
    const payload = await api("/api/downloads");
    renderDownloads(payload.downloads || []);
  }

  function localizedCatalogText(value) {
    if (typeof value === "string") return value;
    if (!value || typeof value !== "object") return "";
    const language = getLanguage().resolved;
    return String(value[language] || value.en || value["zh-Hant"] || "").trim();
  }

  function validateCatalogEntry(entry, format) {
    if (!entry || typeof entry !== "object") return null;
    const name = String(entry.name || "").trim();
    const repository = String(entry.repository || "").trim();
    const revision = String(entry.revision || "main").trim() || "main";
    const filename = String(entry.filename || "").trim();
    if (!name || !/^[A-Za-z0-9_.-]+\/[A-Za-z0-9_.-]+$/.test(repository)) return null;
    if (format === "gguf" && (!filename || !filename.toLowerCase().endsWith(".gguf") || filename.includes("/"))) return null;
    return {
      name,
      repository,
      revision,
      filename: format === "gguf" ? filename : "",
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
    section.append(list);
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
      empty.textContent = "目前沒有下載工作。";
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
      stateLabel.textContent = ({ queued: "等待中", downloading: "下載中", completed: "完成", failed: "失敗" })[job.state] || job.state;
      header.append(name, stateLabel);
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
  byId("quickModelButton").addEventListener("click", () => {
    if (byId("quickModelPopover").hidden) openQuickModelPopover();
    else closeQuickModelPopover();
  });
  byId("openStorageButton").addEventListener("click", () => {
    const format = byId("downloadRuntime").value === MLX_RUNTIME ? "mlx" : "gguf";
    const path = storageDirectories[format];
    const bridge = nativeBridge();
    if (!bridge || !path) return;
    bridge.postMessage({ type: "open-model-directory", path });
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
  initialize();
})();
