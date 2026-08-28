(() => {
  const { api, byId, showMessage } = window.LlamaLoader;
  const directoryState = { inputID: "", path: "", parent: "" };

  async function loadSettings() {
    const settings = await api("/api/settings");
    byId("modelDirectory").value = settings.model_directory || "";
    byId("mlxModelDirectory").value = settings.mlx_model_directory || "";
    byId("hfEndpoint").value = settings.huggingface_endpoint || "";
    byId("defaultRevision").value = settings.default_revision || "main";
    byId("hfToken").value = "";
    byId("clearHFToken").checked = false;
    byId("clearHFToken").disabled = !settings.huggingface_token_set;
    byId("tokenState").textContent = settings.huggingface_token_set
      ? "清除已保存的 Token"
      : "目前沒有保存 Token";
  }

  function createDirectoryButton(entry, className) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = className;
    button.dataset.path = entry.path;
    const icon = document.createElement("span");
    icon.className = "directory-entry-icon";
    icon.textContent = "▸";
    icon.setAttribute("aria-hidden", "true");
    const label = document.createElement("span");
    label.textContent = entry.name;
    button.append(icon, label);
    button.addEventListener("click", () => loadDirectory(entry.path));
    return button;
  }

  function renderDirectoryListing(listing) {
    directoryState.path = listing.path;
    directoryState.parent = listing.parent || "";
    byId("directoryCurrentPath").textContent = listing.path;
    byId("directoryParentButton").disabled = !directoryState.parent;
    byId("confirmDirectoryBrowser").disabled = false;
    byId("directoryBrowserMessage").textContent = listing.directories?.length
      ? "點選資料夾以進入；到達目標位置後按「選擇此目錄」。"
      : "此目錄沒有可進入的子目錄，可直接選擇目前目錄。";

    const roots = byId("directoryRoots");
    roots.replaceChildren();
    (listing.roots || []).forEach((entry) => {
      roots.append(createDirectoryButton(entry, "directory-root-button"));
    });

    const entries = byId("directoryEntries");
    entries.replaceChildren();
    (listing.directories || []).forEach((entry) => {
      entries.append(createDirectoryButton(entry, "directory-entry-button"));
    });
    if (!listing.directories?.length) {
      const empty = document.createElement("p");
      empty.className = "directory-empty";
      empty.textContent = "沒有子目錄";
      entries.append(empty);
    }
  }

  async function loadDirectory(path) {
    byId("directoryBrowserMessage").textContent = "正在讀取目錄…";
    try {
      const listing = await api("/api/system/directories", {
        method: "POST",
        body: JSON.stringify({ current_path: path || "" })
      });
      renderDirectoryListing(listing);
      return true;
    } catch (error) {
      byId("directoryBrowserMessage").textContent = error.message;
      return false;
    }
  }

  async function openDirectoryBrowser(inputID) {
    const input = byId(inputID);
    const dialog = byId("directoryBrowserDialog");
    directoryState.inputID = inputID;
    directoryState.path = "";
    directoryState.parent = "";
    byId("confirmDirectoryBrowser").disabled = true;
    dialog.showModal();
    const loaded = await loadDirectory(input.value.trim());
    if (!loaded) await loadDirectory("");
  }

  function closeDirectoryBrowser() {
    byId("directoryBrowserDialog").close();
  }

  byId("selectModelDirectory").addEventListener("click", () => {
    openDirectoryBrowser("modelDirectory");
  });

  byId("selectMLXModelDirectory").addEventListener("click", () => {
    openDirectoryBrowser("mlxModelDirectory");
  });

  byId("directoryParentButton").addEventListener("click", () => {
    if (directoryState.parent) loadDirectory(directoryState.parent);
  });
  byId("closeDirectoryBrowser").addEventListener("click", closeDirectoryBrowser);
  byId("cancelDirectoryBrowser").addEventListener("click", closeDirectoryBrowser);
  byId("directoryBrowserDialog").addEventListener("click", (event) => {
    if (event.target === byId("directoryBrowserDialog")) closeDirectoryBrowser();
  });
  byId("confirmDirectoryBrowser").addEventListener("click", () => {
    const input = byId(directoryState.inputID);
    const unchanged = input.value.trim() === directoryState.path;
    input.value = directoryState.path;
    input.dispatchEvent(new Event("change", { bubbles: true }));
    input.focus();
    byId("directorySelectionMessage").textContent = unchanged
      ? `已選擇：${directoryState.path}（與目前路徑相同）`
      : `已選擇：${directoryState.path}；請按「儲存設定」完成保存。`;
    closeDirectoryBrowser();
    showMessage(unchanged ? "目錄選擇成功，路徑未變更" : "目錄選擇成功，請儲存設定");
  });

  byId("settingsForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = byId("saveSettingsButton");
    const message = byId("settingsMessage");
    button.disabled = true;
    message.textContent = "";
    try {
      await api("/api/settings", {
        method: "PUT",
        body: JSON.stringify({
          model_directory: byId("modelDirectory").value.trim(),
          mlx_model_directory: byId("mlxModelDirectory").value.trim(),
          huggingface_endpoint: byId("hfEndpoint").value.trim(),
          huggingface_token: byId("hfToken").value.trim(),
          clear_huggingface_token: byId("clearHFToken").checked,
          default_revision: byId("defaultRevision").value.trim()
        })
      });
      await loadSettings();
      message.textContent = "設定已保存。";
      showMessage("設定已保存");
    } catch (error) {
      message.textContent = error.message;
    } finally {
      button.disabled = false;
    }
  });

  loadSettings().catch((error) => showMessage(error.message, "error"));
})();
