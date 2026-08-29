(() => {
  const { api, byId, showMessage, formatBytes, formatTime, t, setLanguage, setTheme, getTheme } = window.LlamaLoader;
  const directoryState = { inputID: "", path: "", parent: "" };
  let settingsState = {};
  let accessControlState = { policy: {}, keys: [] };
  let adminCredentialsState = { authenticationEnabled: true, account: "root" };
  let netPassState = {};
  let netPassFormHydrated = false;
  let netPassPollBusy = false;
  let disableAuthenticationConfirmed = false;
  const settingsPaneHashes = {
    settingsGeneralPane: "general",
    settingsModelSourcePane: "model-source",
    settingsAdminPane: "admin-sign-in",
    settingsSecurityPane: "model-api-security",
    settingsNetPassPane: "reverse-proxy",
    settingsSystemInfoPane: "system-info",
    settingsAboutPane: "about"
  };

  function activateSettingsPane(targetID, updateLocation = true) {
    const tabs = [...document.querySelectorAll("[data-settings-target]")];
    const target = byId(targetID) || byId("settingsGeneralPane");
    tabs.forEach((tab) => {
      const active = tab.dataset.settingsTarget === target.id;
      tab.classList.toggle("active", active);
      tab.setAttribute("aria-selected", String(active));
      tab.tabIndex = active ? 0 : -1;
    });
    document.querySelectorAll(".settings-pane").forEach((pane) => {
      pane.hidden = pane !== target;
    });
    if (updateLocation) {
      const hash = settingsPaneHashes[target.id] || settingsPaneHashes.settingsGeneralPane;
      history.replaceState(null, "", `${location.pathname}${location.search}#${hash}`);
    }
  }

  function setupSettingsNavigation() {
    const tabs = [...document.querySelectorAll("[data-settings-target]")];
    tabs.forEach((tab, index) => {
      tab.addEventListener("click", () => activateSettingsPane(tab.dataset.settingsTarget));
      tab.addEventListener("keydown", (event) => {
        let nextIndex = index;
        if (["ArrowDown", "ArrowRight"].includes(event.key)) nextIndex = (index + 1) % tabs.length;
        else if (["ArrowUp", "ArrowLeft"].includes(event.key)) nextIndex = (index - 1 + tabs.length) % tabs.length;
        else if (event.key === "Home") nextIndex = 0;
        else if (event.key === "End") nextIndex = tabs.length - 1;
        else return;
        event.preventDefault();
        tabs[nextIndex].focus();
        activateSettingsPane(tabs[nextIndex].dataset.settingsTarget);
      });
    });
    const hash = location.hash.replace(/^#/, "");
    const initialPane = Object.entries(settingsPaneHashes).find(([, value]) => value === hash)?.[0]
      || "settingsGeneralPane";
    activateSettingsPane(initialPane, false);
    window.addEventListener("hashchange", () => {
      const currentHash = location.hash.replace(/^#/, "");
      const paneID = Object.entries(settingsPaneHashes).find(([, value]) => value === currentHash)?.[0];
      if (paneID) activateSettingsPane(paneID, false);
    });
  }

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
    settingsState = settings;
    byId("uiLanguage").value = settings.ui_language || "auto";
    setLanguage(byId("uiLanguage").value);
    const selectedTheme = settings.ui_theme || getTheme() || "tanpopo";
    const themeInput = document.querySelector(`input[name="uiTheme"][value="${selectedTheme}"]`)
      || document.querySelector('input[name="uiTheme"][value="tanpopo"]');
    themeInput.checked = true;
    setTheme(themeInput.value);
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
    byId("tokenState").textContent = "清除 Access Token";
  }

  function settingsPayload(scope) {
    const saveGeneral = scope === "general";
    const saveModelSource = scope === "model-source";
    return {
      model_directory: saveGeneral ? byId("modelDirectory").value.trim() : (settingsState.model_directory || byId("modelDirectory").value.trim()),
      mlx_model_directory: saveGeneral ? byId("mlxModelDirectory").value.trim() : (settingsState.mlx_model_directory || byId("mlxModelDirectory").value.trim()),
      resident_mode: saveGeneral || typeof settingsState.resident_mode !== "boolean" ? byId("residentMode").checked : settingsState.resident_mode,
      ui_language: saveGeneral ? byId("uiLanguage").value : (settingsState.ui_language || byId("uiLanguage").value),
      ui_theme: saveGeneral ? (document.querySelector('input[name="uiTheme"]:checked')?.value || "tanpopo") : (settingsState.ui_theme || "tanpopo"),
      huggingface_endpoint: saveModelSource ? byId("hfEndpoint").value.trim() : (settingsState.huggingface_endpoint || byId("hfEndpoint").value.trim()),
      huggingface_token: saveModelSource ? byId("hfToken").value.trim() : "",
      clear_huggingface_token: saveModelSource && byId("clearHFToken").checked,
      default_revision: saveModelSource ? byId("defaultRevision").value.trim() : (settingsState.default_revision || byId("defaultRevision").value.trim())
    };
  }

  async function saveSettings(scope, button, message, successMessage) {
    button.disabled = true;
    message.textContent = "";
    try {
      const payload = settingsPayload(scope);
      try {
        await api("/api/settings", { method: "PUT", body: JSON.stringify(payload) });
      } catch (error) {
        const rejectedField = error.message.match(/unknown field\s+"([^"]+)"/i)?.[1];
        const rollingUpdateFields = new Set(["ui_theme"]);
        if (!rejectedField || !rollingUpdateFields.has(rejectedField)) throw error;
        const compatiblePayload = { ...payload };
        delete compatiblePayload[rejectedField];
        await api("/api/settings", { method: "PUT", body: JSON.stringify(compatiblePayload) });
      }
      await loadSettings();
      message.textContent = `${t(successMessage)}。`;
      showMessage(successMessage);
    } catch (error) {
      message.textContent = error.message;
    } finally {
      button.disabled = false;
    }
  }

  function displayVersion(value) {
    const version = String(value || "").trim();
    if (!version) return "—";
    if (version === "dev") return version;
    return version.replace(/^v/i, "");
  }

  function normalizeManagementURL(value) {
    try {
      const parsed = new URL(String(value || "").trim());
      if (!["http:", "https:"].includes(parsed.protocol)) return "";
      return parsed.origin;
    } catch (_) {
      return "";
    }
  }

  function isLoopbackManagementURL(value) {
    try {
      const hostname = new URL(value).hostname.replace(/^\[|\]$/g, "").toLowerCase();
      return hostname === "localhost" || hostname === "::1" || /^127(?:\.|$)/.test(hostname);
    } catch (_) {
      return false;
    }
  }

  async function copyText(value) {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(value);
      return;
    }
    const input = document.createElement("textarea");
    input.value = value;
    input.setAttribute("readonly", "");
    input.style.position = "fixed";
    input.style.opacity = "0";
    document.body.append(input);
    input.select();
    const copied = document.execCommand("copy");
    input.remove();
    if (!copied) throw new Error("copy failed");
  }

  function renderManagementURLs(values) {
    const list = byId("managementURLList");
    const candidates = [window.location.origin, ...(Array.isArray(values) ? values : [])];
    const urls = [...new Set(candidates
      .map(normalizeManagementURL)
      .filter((url) => url && !isLoopbackManagementURL(url)))];
    list.replaceChildren();
    if (!urls.length) {
      const empty = document.createElement("li");
      empty.className = "empty-state";
      empty.textContent = "—";
      list.append(empty);
      return;
    }
    urls.forEach((url) => {
      const item = document.createElement("li");
      item.className = "management-url-item";
      const link = document.createElement("a");
      link.className = "about-link";
      link.href = url;
      link.target = "_blank";
      link.rel = "noopener noreferrer";
      link.textContent = url;
      const copyButton = document.createElement("button");
      copyButton.className = "api-copy-button";
      copyButton.type = "button";
      copyButton.title = t("複製管理頁面網址");
      copyButton.setAttribute("aria-label", t("複製管理頁面網址"));
      copyButton.innerHTML = '<svg viewBox="0 0 24 24" aria-hidden="true"><rect x="8" y="8" width="11" height="11" rx="2"></rect><path d="M16 8V6a2 2 0 0 0-2-2H6a2 2 0 0 0-2 2v8a2 2 0 0 0 2 2h2"></path></svg>';
      copyButton.addEventListener("click", async () => {
        try {
          await copyText(url);
          showMessage("管理頁面網址已複製");
        } catch (_) {
          showMessage("無法自動複製，請手動選取網址", "error");
        }
      });
      item.append(link, copyButton);
      list.append(item);
    });
  }

  function setNetPassPrerequisite(id, ready, detail) {
    const element = byId(id);
    element.classList.toggle("ready", ready);
    element.classList.toggle("blocked", !ready);
    element.querySelector(".netpass-state-label").textContent = ready ? t("通過") : t("沒開啟");
    if (detail) element.querySelector("small").textContent = detail;
  }

  function updateNetPassControls() {
    const running = Boolean(netPassState.running);
    const canStart = Boolean(netPassState.security_ready
      && netPassState.api_key_set
      && byId("acceptNetPassPolicy").checked
      && !running);
    byId("saveNetPassButton").disabled = running;
    byId("startNetPassButton").disabled = !canStart;
    byId("stopNetPassButton").disabled = !running;
    byId("netPassAPIKey").disabled = running;
    byId("netPassName").disabled = running;
    byId("clearNetPassAPIKeyButton").disabled = running || !netPassState.api_key_set;
  }

  function renderNetPassStatus(status, hydrateForm = false) {
    netPassState = status || {};
    if (hydrateForm || !netPassFormHydrated) {
      byId("netPassEndpoint").value = status.endpoint || "https://netpass.mars-cloud.com";
      byId("netPassName").value = status.name || "";
      byId("netPassAPIKey").value = "";
      netPassFormHydrated = true;
    }
    byId("netPassAPIKey").placeholder = status.api_key_set
      ? t("留空即保留目前設定")
      : t("尚未設定");

    const authenticationReady = Boolean(status.authentication_enabled);
    const apiKeyReady = Boolean(status.api_key_enabled);
    setNetPassPrerequisite(
      "netPassAdminAuthState",
      authenticationReady,
      authenticationReady ? t("登入驗證已開啟") : t("登入驗證尚未開啟")
    );
    setNetPassPrerequisite(
      "netPassAPIKeyState",
      apiKeyReady,
      apiKeyReady
        ? `${t("已核發")} ${Number(status.access_key_count || 0)} ${t("組金鑰")}`
        : t("金鑰驗證尚未開啟")
    );

    const securityMessage = byId("netPassSecurityMessage");
    securityMessage.classList.toggle("ready", Boolean(status.security_ready));
    if (status.security_ready) {
      securityMessage.textContent = t("安全性前置檢查已通過。");
    } else if (!authenticationReady && !apiKeyReady) {
      securityMessage.textContent = t("請先開啟管理介面帳號密碼與模型 API Access Key 驗證");
    } else if (!authenticationReady) {
      securityMessage.textContent = t("請先開啟管理介面帳號密碼驗證");
    } else if (!apiKeyReady) {
      securityMessage.textContent = t("請先核發 Access Key 並開啟模型 API 金鑰驗證");
    } else {
      securityMessage.textContent = t("請先完成必要的安全性設定。");
    }

    byId("netPassRuntimeState").textContent = status.running
      ? t("執行中")
      : (!status.runtime_checked
        ? t("開啟時檢查")
        : (status.available ? t("可使用") : t("安裝包未包含 NetPassClient")));
    byId("netPassConnectionState").textContent = status.connected
      ? t("已連線")
      : (status.running ? t("連線中…") : t("未連線"));
    byId("netPassPID").textContent = status.pid || "—";
    byId("netPassClientID").textContent = status.client_id || "—";

    const publicURL = String(status.public_url || "").trim();
    const publicContainer = byId("netPassPublicURLContainer");
    const publicLink = byId("netPassPublicURL");
    publicContainer.hidden = !status.connected || !publicURL;
    publicLink.textContent = publicURL;
    if (publicURL) publicLink.href = publicURL;
    else publicLink.removeAttribute("href");

    const runtimeMessage = byId("netPassRuntimeMessage");
    runtimeMessage.classList.toggle("error", Boolean(status.last_error));
    runtimeMessage.textContent = status.last_error || (status.runtime_checked && !status.available
      ? t("NetPassClient 為閉源元件，必須由正式 .app 或 MSI 安裝包提供。")
      : "");
    updateNetPassControls();
  }

  async function loadNetPassStatus(hydrateForm = false) {
    if (netPassPollBusy) return;
    netPassPollBusy = true;
    try {
      renderNetPassStatus(await api("/api/netpass/status"), hydrateForm);
    } catch (error) {
      const message = byId("netPassRuntimeMessage");
      if (netPassState.running) {
        message.classList.add("error");
        message.textContent = error.message;
      } else {
        message.classList.remove("error");
        message.textContent = "";
      }
    } finally {
      netPassPollBusy = false;
    }
  }

  async function saveNetPassConfig(showSuccess = true) {
    const result = await api("/api/netpass/config", {
      method: "PUT",
      body: JSON.stringify({
        endpoint: byId("netPassEndpoint").value.trim(),
        api_key: byId("netPassAPIKey").value.trim(),
        clear_api_key: false,
        name: byId("netPassName").value.trim()
      })
    });
    renderNetPassStatus(result, true);
    if (showSuccess) showMessage("NetPass 連線設定已保存");
    return result;
  }

  function renderAppVersion(status) {
    byId("currentAppVersion").textContent = displayVersion(status.current_version);
    byId("latestAppVersion").textContent = displayVersion(status.latest_version);
    byId("lastUpdateCheck").textContent = status.checked_at ? formatTime(status.checked_at) : t("尚未檢查");

    const repositoryLink = byId("githubRepositoryLink");
    const repositoryURL = String(status.repository_url || "").trim();
    repositoryLink.textContent = repositoryURL || "—";
    if (repositoryURL) repositoryLink.href = repositoryURL;
    else repositoryLink.removeAttribute("href");

    const releaseLink = byId("githubReleaseLink");
    const releaseURL = String(status.release_url || "").trim();
    releaseLink.hidden = !status.update_available || !releaseURL;
    if (releaseURL) releaseLink.href = releaseURL;
    else releaseLink.removeAttribute("href");

    const summary = byId("appUpdateSummary");
    summary.className = "about-update-summary";
    if (status.check_error) {
      summary.classList.add("error");
      summary.textContent = `${t("更新檢查失敗")}：${status.check_error}`;
    } else if (status.update_available) {
      summary.classList.add("update-available");
      summary.textContent = t(`有新版本 ${displayVersion(status.latest_version)} 可用。`);
    } else if (status.latest_version) {
      summary.textContent = t("目前已是最新版本。");
    } else {
      summary.textContent = t("尚未完成更新檢查。");
    }
  }

  async function loadAppVersion(force = false) {
    const button = byId("checkUpdateButton");
    if (force) {
      button.disabled = true;
      byId("appUpdateSummary").textContent = t("正在檢查更新…");
    }
    try {
      const status = await api(force ? "/api/app-version/check" : "/api/app-version", {
        method: force ? "POST" : "GET"
      });
      renderAppVersion(status);
    } catch (error) {
      const summary = byId("appUpdateSummary");
      summary.className = "about-update-summary error";
      summary.textContent = `${t("更新檢查失敗")}：${error.message}`;
    } finally {
      if (force) button.disabled = false;
    }
  }

  function systemInfoValue(value) {
    const text = String(value || "").trim();
    return text || "—";
  }

  function renderSystemInfo(info) {
    byId("systemOSName").textContent = systemInfoValue(info.os_name);
    byId("systemOSVersion").textContent = systemInfoValue(info.os_version);
    byId("systemOSBuild").textContent = systemInfoValue(info.os_build);
    byId("systemKernelVersion").textContent = systemInfoValue(info.kernel_version);
    byId("systemArchitecture").textContent = systemInfoValue(info.architecture);
    byId("systemHostname").textContent = systemInfoValue(info.hostname);
    byId("systemCPUModel").textContent = systemInfoValue(info.cpu_model);
    byId("systemPhysicalCores").textContent = Number(info.physical_cores) > 0 ? String(info.physical_cores) : "—";
    byId("systemLogicalCores").textContent = Number(info.logical_cores) > 0 ? String(info.logical_cores) : "—";
    byId("systemGPUModel").textContent = systemInfoValue(info.gpu_model);
    byId("systemMemory").textContent = Number(info.memory_bytes) > 0 ? formatBytes(Number(info.memory_bytes)) : "—";
    renderNetworkInterfaces(info.network_interfaces);
    renderManagementURLs(info.management_urls);
  }

  function networkDetail(label, value, code = false) {
    const row = document.createElement("div");
    const term = document.createElement("dt");
    const description = document.createElement("dd");
    term.textContent = t(label);
    const content = code ? document.createElement("code") : document.createElement("span");
    content.textContent = systemInfoValue(value);
    description.append(content);
    row.append(term, description);
    return row;
  }

  function renderNetworkInterfaces(interfaces) {
    const container = byId("systemNetworkInterfaces");
    container.replaceChildren();
    const entries = Array.isArray(interfaces) ? interfaces : [];
    if (!entries.length) {
      const empty = document.createElement("p");
      empty.className = "empty-state";
      empty.textContent = t("沒有可顯示的網路介面。");
      container.append(empty);
      return;
    }
    entries.forEach((networkInterface) => {
      const addresses = Array.isArray(networkInterface.addresses)
        ? networkInterface.addresses.map((value) => String(value || "").trim()).filter(Boolean)
        : [];
      const active = Boolean(networkInterface.up) && addresses.length > 0;
      const card = document.createElement("article");
      card.className = "network-interface-card";
      const heading = document.createElement("header");
      const name = document.createElement("strong");
      name.textContent = systemInfoValue(networkInterface.name);
      const status = document.createElement("span");
      status.className = `network-interface-status ${active ? "active" : (networkInterface.up ? "enabled" : "offline")}`;
      status.textContent = t(active ? "已連線" : (networkInterface.up ? "已啟用" : "未連線"));
      heading.append(name, status);
      const details = document.createElement("dl");
      details.append(
        networkDetail("IP 位址", addresses.length ? addresses.join("\n") : "—", true),
        networkDetail("MAC 位址", networkInterface.hardware_address, true),
        networkDetail("MTU", Number(networkInterface.mtu) > 0 ? String(networkInterface.mtu) : "—", true)
      );
      card.append(heading, details);
      container.append(card);
    });
  }

  async function loadSystemInfo(retry = 0) {
    const message = byId("systemInfoMessage");
    try {
      const info = await api("/api/system/info");
      if (!info.collected_at && retry < 4) {
        window.setTimeout(() => loadSystemInfo(retry + 1), 500);
        return;
      }
      renderSystemInfo(info);
      message.textContent = "";
    } catch (error) {
      message.textContent = `${t("無法讀取系統資訊")}：${error.message}`;
    }
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
  document.querySelectorAll('input[name="uiTheme"]').forEach((input) => {
    input.addEventListener("change", () => {
      if (input.checked) setTheme(input.value);
    });
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
      settingsState = settings;
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
    await saveSettings("general", byId("saveSettingsButton"), byId("settingsMessage"), "設定已保存");
  });

  byId("modelSourceForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    await saveSettings("model-source", byId("saveModelSourceButton"), byId("modelSourceMessage"), "模型來源已保存");
  });

  document.querySelectorAll("[data-netpass-settings-target]").forEach((button) => {
    button.addEventListener("click", () => activateSettingsPane(button.dataset.netpassSettingsTarget));
  });
  byId("acceptNetPassPolicy").addEventListener("change", updateNetPassControls);
  byId("clearNetPassAPIKeyButton").addEventListener("click", async () => {
    if (!window.confirm(t("確定要清除 NetPass Server API Key？清除後必須重新設定才能連線。"))) return;
    const button = byId("clearNetPassAPIKeyButton");
    const message = byId("netPassMessage");
    button.disabled = true;
    message.textContent = "";
    try {
      const status = await api("/api/netpass/config", {
        method: "PUT",
        body: JSON.stringify({
          endpoint: byId("netPassEndpoint").value.trim(),
          api_key: "",
          clear_api_key: true,
          name: byId("netPassName").value.trim()
        })
      });
      renderNetPassStatus(status, true);
      message.textContent = t("NetPass Server API Key 已清除");
      showMessage("NetPass Server API Key 已清除");
    } catch (error) {
      message.textContent = error.message;
      showMessage(error.message, "error");
    } finally {
      updateNetPassControls();
    }
  });
  byId("netPassForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = byId("saveNetPassButton");
    const message = byId("netPassMessage");
    button.disabled = true;
    message.textContent = "";
    try {
      await saveNetPassConfig();
      message.textContent = t("NetPass 連線設定已保存");
    } catch (error) {
      message.textContent = error.message;
      showMessage(error.message, "error");
    } finally {
      updateNetPassControls();
    }
  });
  byId("startNetPassButton").addEventListener("click", async () => {
    const button = byId("startNetPassButton");
    const message = byId("netPassMessage");
    button.disabled = true;
    message.textContent = t("正在建立公共連線…");
    try {
      await saveNetPassConfig(false);
      const status = await api("/api/netpass/start", {
        method: "POST",
        body: JSON.stringify({
          accept_usage_policy: byId("acceptNetPassPolicy").checked
        })
      });
      renderNetPassStatus(status);
      message.textContent = t("NetPassClient 已啟動，正在等待公開網址。");
      showMessage("反向代理正在連線");
    } catch (error) {
      message.textContent = error.message;
      showMessage(error.message, "error");
    } finally {
      updateNetPassControls();
    }
  });
  byId("stopNetPassButton").addEventListener("click", async () => {
    const button = byId("stopNetPassButton");
    const message = byId("netPassMessage");
    button.disabled = true;
    message.textContent = t("正在停止反向代理…");
    try {
      renderNetPassStatus(await api("/api/netpass/stop", { method: "POST" }));
      byId("acceptNetPassPolicy").checked = false;
      message.textContent = t("反向代理已停止。");
      showMessage("反向代理已停止");
    } catch (error) {
      message.textContent = error.message;
      showMessage(error.message, "error");
    } finally {
      updateNetPassControls();
    }
  });
  byId("copyNetPassURLButton").addEventListener("click", async () => {
    const publicURL = String(netPassState.public_url || "").trim();
    if (!publicURL) return;
    try {
      await copyText(publicURL);
      showMessage("NetPass 網址已複製");
    } catch (_) {
      showMessage("無法自動複製，請手動選取網址", "error");
    }
  });

  byId("checkUpdateButton").addEventListener("click", () => loadAppVersion(true));

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

  setupSettingsNavigation();
  renderManagementURLs([]);
  Promise.all([loadSettings(), loadAdminCredentials(), loadAccessControl(), loadSystemInfo(), loadAppVersion()])
    .catch((error) => showMessage(error.message, "error"));
  loadNetPassStatus(true);
  window.setInterval(() => {
    if (!byId("settingsSystemInfoPane").hidden) loadSystemInfo();
  }, 3000);
  window.setInterval(() => {
    if (!byId("settingsNetPassPane").hidden || netPassState.running) loadNetPassStatus();
  }, 2000);
})();
