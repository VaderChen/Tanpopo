(() => {
  const { api, byId, showMessage, formatTime, setLanguage } = window.LlamaLoader;
  const directoryState = { inputID: "", path: "", parent: "" };
  let accessControlState = { policy: {}, keys: [] };
  let adminCredentialsState = { authenticationEnabled: true, account: "root" };
  let disableAuthenticationConfirmed = false;

  function updateResidentModeLabel() {
    byId("residentModeLabel").textContent = byId("residentMode").checked ? "啟用" : "關閉";
  }

  function notifyNativeResidentMode(enabled) {
    try {
      window.webkit?.messageHandlers?.tanpopoNative?.postMessage({
        type: "resident-mode",
        enabled: Boolean(enabled)
      });
    } catch (_error) {
      // 一般瀏覽器沒有 WKWebView bridge；設定仍會由後端正常保存。
    }
  }

  async function loadSettings() {
    const settings = await api("/api/settings");
    byId("uiLanguage").value = settings.ui_language || "auto";
    setLanguage(byId("uiLanguage").value);
    byId("modelDirectory").value = settings.model_directory || "";
    byId("mlxModelDirectory").value = settings.mlx_model_directory || "";
    byId("residentMode").checked = Boolean(settings.resident_mode);
    updateResidentModeLabel();
    notifyNativeResidentMode(settings.resident_mode);
    byId("hfEndpoint").value = settings.huggingface_endpoint || "";
    byId("defaultRevision").value = settings.default_revision || "main";
    byId("hfToken").value = "";
    byId("clearHFToken").checked = false;
    byId("clearHFToken").disabled = !settings.huggingface_token_set;
    byId("tokenState").textContent = settings.huggingface_token_set
      ? "清除已保存的 Token"
      : "目前沒有保存 Token";
  }

  function updateAdminCredentialFields() {
    const enabled = byId("authenticationEnabled").checked;
    const wasEnabled = adminCredentialsState.authenticationEnabled;
    byId("currentAdminPassword").disabled = !wasEnabled;
    byId("currentAdminPassword").required = wasEnabled;
    byId("newAdminPassword").required = !wasEnabled && enabled;
    byId("confirmAdminPassword").required = !wasEnabled && enabled;
    byId("authenticationEnabledLabel").textContent = enabled ? "啟用" : "關閉";
    byId("adminCredentialsHint").textContent = enabled
      ? (wasEnabled
        ? "變更帳號或密碼後，所有既有登入工作階段都會失效。"
        : "重新啟用登入時必須設定新密碼；保存後請以新帳密登入。")
      : "登入驗證關閉後，不需帳號密碼即可進入管理介面；原帳密會保留供日後重新啟用。";
  }

  async function loadAdminCredentials() {
    const credentials = await api("/api/admin-credentials");
    adminCredentialsState = {
      authenticationEnabled: Boolean(credentials.authentication_enabled),
      account: credentials.account || "root"
    };
    byId("adminAccount").value = adminCredentialsState.account;
    byId("authenticationEnabled").checked = adminCredentialsState.authenticationEnabled;
    byId("currentAdminPassword").value = "";
    byId("newAdminPassword").value = "";
    byId("confirmAdminPassword").value = "";
    disableAuthenticationConfirmed = false;
    updateAdminCredentialFields();
  }

  function confirmDisableAuthentication() {
    return window.confirm(
      "確定關閉管理介面登入驗證？\n\n關閉後，能連線到 Tanpopo 的使用者不需帳號密碼即可操作所有管理功能。"
    );
  }

  function securityModeText() {
    const useKey = byId("apiKeyEnabled").checked;
    const useIP = byId("ipAllowlistEnabled").checked;
    if (useKey && useIP) return "金鑰與 IP 白名單同時啟用（兩者皆須通過）";
    if (useKey) return "只使用存取金鑰";
    if (useIP) return "只使用 IP 白名單";
    return "目前不使用額外限制";
  }

  function updateSecurityMode() {
    const summary = byId("securityModeSummary");
    summary.textContent = securityModeText();
    summary.classList.toggle("active", byId("apiKeyEnabled").checked || byId("ipAllowlistEnabled").checked);
  }

  function renderAccessKeys() {
    const container = byId("accessKeyList");
    container.replaceChildren();
    const keys = accessControlState.keys || [];
    if (!keys.length) {
      const empty = document.createElement("p");
      empty.className = "access-key-empty";
      empty.textContent = "尚未核發任何金鑰。";
      container.append(empty);
      return;
    }
    keys.forEach((key) => {
      const row = document.createElement("div");
      row.className = "access-key-item";
      const details = document.createElement("div");
      const name = document.createElement("strong");
      name.textContent = key.name;
      const meta = document.createElement("span");
      meta.textContent = `${key.prefix}… · 核發於 ${formatTime(key.created_at)}`;
      details.append(name, meta);
      const revoke = document.createElement("button");
      revoke.type = "button";
      revoke.className = "button danger compact";
      revoke.textContent = "撤銷";
      const isLastRequiredKey = byId("apiKeyEnabled").checked && keys.length === 1;
      revoke.disabled = isLastRequiredKey;
      revoke.title = isLastRequiredKey ? "金鑰驗證啟用中，不可撤銷最後一把金鑰" : `撤銷 ${key.name}`;
      revoke.addEventListener("click", () => revokeAccessKey(key));
      row.append(details, revoke);
      container.append(row);
    });
  }

  async function loadAccessControl() {
    accessControlState = await api("/api/access-control");
    const policy = accessControlState.policy || {};
    byId("apiKeyEnabled").checked = Boolean(policy.api_key_enabled);
    byId("ipAllowlistEnabled").checked = Boolean(policy.ip_allowlist_enabled);
    byId("ipAllowlist").value = (policy.ip_allowlist || []).join("\n");
    updateSecurityMode();
    renderAccessKeys();
  }

  function allowlistValues() {
    return byId("ipAllowlist").value
      .split(/\r?\n/)
      .map((value) => value.trim())
      .filter(Boolean);
  }

  async function revokeAccessKey(key) {
    if (!window.confirm(`確定撤銷金鑰「${key.name}」？撤銷後無法復原。`)) return;
    try {
      await api(`/api/access-control/keys/${encodeURIComponent(key.id)}`, { method: "DELETE" });
      await loadAccessControl();
      showMessage("金鑰已撤銷");
    } catch (error) {
      showMessage(error.message, "error");
    }
  }

  function hideIssuedKey() {
    byId("issuedAccessKey").value = "";
    byId("issuedKeyPanel").hidden = true;
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
  byId("uiLanguage").addEventListener("change", () => {
    setLanguage(byId("uiLanguage").value);
  });
  byId("residentMode").addEventListener("change", async () => {
    const toggle = byId("residentMode");
    const requested = toggle.checked;
    toggle.disabled = true;
    updateResidentModeLabel();
    try {
      const settings = await api("/api/settings/resident-mode", {
        method: "PUT",
        body: JSON.stringify({ enabled: requested })
      });
      toggle.checked = Boolean(settings.resident_mode);
      updateResidentModeLabel();
      notifyNativeResidentMode(settings.resident_mode);
      showMessage(settings.resident_mode
        ? "常駐模式已啟用，可從系統選單列重新開啟視窗"
        : "常駐模式已關閉，關閉視窗將停止服務");
    } catch (error) {
      toggle.checked = !requested;
      updateResidentModeLabel();
      showMessage(error.message, "error");
    } finally {
      toggle.disabled = false;
    }
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
          resident_mode: byId("residentMode").checked,
          ui_language: byId("uiLanguage").value,
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

  byId("authenticationEnabled").addEventListener("change", async () => {
    const toggle = byId("authenticationEnabled");
    const message = byId("adminCredentialsMessage");
    if (!adminCredentialsState.authenticationEnabled || toggle.checked) {
      disableAuthenticationConfirmed = false;
      updateAdminCredentialFields();
      return;
    }

    disableAuthenticationConfirmed = confirmDisableAuthentication();
    if (!disableAuthenticationConfirmed) {
      toggle.checked = true;
      updateAdminCredentialFields();
      return;
    }

    toggle.disabled = true;
    message.textContent = "正在關閉登入驗證…";
    updateAdminCredentialFields();
    try {
      await api("/api/admin-credentials", {
        method: "PUT",
        body: JSON.stringify({
          account: adminCredentialsState.account,
          current_password: "",
          password: "",
          authentication_enabled: false,
          disable_authentication_confirmed: true
        })
      });
      await loadAdminCredentials();
      byId("logoutButton").hidden = true;
      message.textContent = "登入驗證已關閉並保存。";
      showMessage("管理介面已改為不需登入");
    } catch (error) {
      toggle.checked = true;
      disableAuthenticationConfirmed = false;
      message.textContent = error.message;
      showMessage(error.message, "error");
      updateAdminCredentialFields();
    } finally {
      toggle.disabled = false;
    }
  });

  byId("adminCredentialsForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const authenticationEnabled = byId("authenticationEnabled").checked;
    const newPassword = byId("newAdminPassword").value;
    const confirmPassword = byId("confirmAdminPassword").value;
    const button = byId("saveAdminCredentialsButton");
    const message = byId("adminCredentialsMessage");

    if (newPassword !== confirmPassword) {
      message.textContent = "新密碼與確認密碼不一致。";
      byId("confirmAdminPassword").focus();
      return;
    }
    if (adminCredentialsState.authenticationEnabled && !authenticationEnabled && !disableAuthenticationConfirmed) {
      disableAuthenticationConfirmed = confirmDisableAuthentication();
      if (!disableAuthenticationConfirmed) {
        byId("authenticationEnabled").checked = true;
        updateAdminCredentialFields();
        return;
      }
    }

    button.disabled = true;
    message.textContent = "";
    try {
      const result = await api("/api/admin-credentials", {
        method: "PUT",
        body: JSON.stringify({
          account: byId("adminAccount").value,
          current_password: byId("currentAdminPassword").value,
          password: newPassword,
          authentication_enabled: authenticationEnabled,
          disable_authentication_confirmed: disableAuthenticationConfirmed
        })
      });
      if (result.authentication_enabled) {
        message.textContent = "登入設定已保存，正在返回登入頁面…";
        showMessage("登入設定已保存，請重新登入");
        window.setTimeout(() => location.replace("/login.html"), 500);
        return;
      }
      await loadAdminCredentials();
      byId("logoutButton").hidden = true;
      message.textContent = "登入驗證已關閉。";
      showMessage("管理介面已改為不需登入");
    } catch (error) {
      message.textContent = error.message;
    } finally {
      button.disabled = false;
    }
  });

  byId("apiKeyEnabled").addEventListener("change", () => {
    updateSecurityMode();
    renderAccessKeys();
  });
  byId("ipAllowlistEnabled").addEventListener("change", updateSecurityMode);

  byId("accessControlForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = byId("saveAccessControlButton");
    const message = byId("accessControlMessage");
    button.disabled = true;
    message.textContent = "";
    try {
      accessControlState = await api("/api/access-control", {
        method: "PUT",
        body: JSON.stringify({
          api_key_enabled: byId("apiKeyEnabled").checked,
          ip_allowlist_enabled: byId("ipAllowlistEnabled").checked,
          ip_allowlist: allowlistValues()
        })
      });
      await loadAccessControl();
      message.textContent = "安全設定已保存，執行中的 Runtime 將在數秒內套用。";
      showMessage("模型 API 安全設定已保存");
    } catch (error) {
      message.textContent = error.message;
    } finally {
      button.disabled = false;
    }
  });

  byId("issueAccessKeyButton").addEventListener("click", async () => {
    const button = byId("issueAccessKeyButton");
    const name = byId("accessKeyName").value.trim();
    button.disabled = true;
    try {
      const issued = await api("/api/access-control/keys", {
        method: "POST",
        body: JSON.stringify({ name })
      });
      byId("issuedAccessKey").value = issued.key || "";
      byId("issuedKeyPanel").hidden = false;
      byId("accessKeyName").value = "";
      await loadAccessControl();
      showMessage("新金鑰已核發，請立即保存");
    } catch (error) {
      showMessage(error.message, "error");
    } finally {
      button.disabled = false;
    }
  });

  byId("copyAccessKeyButton").addEventListener("click", async () => {
    const input = byId("issuedAccessKey");
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(input.value);
      } else {
        input.select();
        document.execCommand("copy");
      }
      showMessage("金鑰已複製");
    } catch (_) {
      input.select();
      showMessage("請手動複製已選取的金鑰", "error");
    }
  });
  byId("dismissIssuedKeyButton").addEventListener("click", hideIssuedKey);

  Promise.all([loadSettings(), loadAdminCredentials(), loadAccessControl()])
    .catch((error) => showMessage(error.message, "error"));
})();
