(() => {
  const { api, byId, showMessage, formatBytes } = window.LlamaLoader;
  const LLAMA_RUNTIME = "llama-server";
  const MLX_RUNTIME = "mlx-server";

  function renderRuntimeFields() {
    const isMLX = byId("downloadRuntime").value === MLX_RUNTIME;
    byId("filenameField").hidden = isMLX;
    byId("filename").required = !isMLX;
    byId("mlxDownloadHint").hidden = !isMLX;
  }

  async function loadSettings() {
    const settings = await api("/api/settings");
    byId("revision").placeholder = settings.default_revision || "main";
  }

  async function loadDownloads() {
    const payload = await api("/api/downloads");
    renderDownloads(payload.downloads || []);
  }

  function renderDownloads(jobs) {
    const container = byId("downloadJobs");
    container.replaceChildren();
    if (!jobs.length) {
      const empty = document.createElement("p");
      empty.className = "empty-state";
      empty.textContent = "目前沒有下載工作。";
      container.append(empty);
      return;
    }
    jobs.slice(0, 8).forEach((job) => {
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
      const companions = (result.jobs || []).slice(1).map((job) => job.filename);
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

  byId("downloadRuntime").addEventListener("change", renderRuntimeFields);
  renderRuntimeFields();
  initialize();
})();
