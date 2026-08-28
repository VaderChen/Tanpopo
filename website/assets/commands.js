(() => {
  const { api, byId, showMessage, formatTime } = window.LlamaLoader;
  const LLAMA_RUNTIME = "llama-server";
  const MLX_RUNTIME = "mlx-server";
  const state = { commands: [], models: [], selectedID: "" };

  function commandPayload() {
    return {
      runtime: byId("commandRuntime").value,
      name: byId("commandName").value.trim(),
      draft_model: byId("commandDraftModel").value.trim(),
      server_host: byId("commandHost").value.trim(),
      server_port: Number(byId("commandPort").value),
      context_size: Number(byId("commandContextSize").value),
      gpu_layers: Number(byId("commandGPULayers").value),
      threads: Number(byId("commandThreads").value),
      extra_args: byId("commandExtraArgs").value
        .split(/\r?\n/)
        .map((argument) => argument.trim())
        .filter(Boolean)
    };
  }

  function fillForm(command) {
    state.selectedID = command?.id || "";
    byId("commandRuntime").value = command?.runtime || LLAMA_RUNTIME;
    byId("commandName").value = command?.name || "新啟動參數";
    byId("commandDraftModel").value = command?.draft_model || "";
    byId("commandHost").value = command?.server_host || "0.0.0.0";
    byId("commandPort").value = command?.server_port ?? 8080;
    byId("commandContextSize").value = command?.context_size ?? 262144;
    byId("commandGPULayers").value = command?.gpu_layers ?? -1;
    byId("commandThreads").value = command?.threads ?? 0;
    byId("commandExtraArgs").value = (command?.extra_args || []).join("\n");
    byId("commandEditorTitle").textContent = command ? "編輯啟動參數" : "新增啟動參數";
    byId("commandUpdatedAt").textContent = command ? `更新於 ${formatTime(command.updated_at)}` : "尚未保存";
    byId("deleteCommandButton").disabled = !command || state.commands.length <= 1;
    byId("saveCommandButton").textContent = command ? "儲存參數" : "建立參數";
    byId("commandMessage").textContent = "";
    renderList();
    renderRuntimeFields();
    renderPreview();
  }

  function renderList() {
    const container = byId("commandList");
    container.replaceChildren();
    state.commands.forEach((command) => {
      const button = document.createElement("button");
      button.type = "button";
      button.className = "command-list-item";
      button.classList.toggle("active", command.id === state.selectedID);
      const name = document.createElement("strong");
      name.textContent = command.name;
      const summary = document.createElement("span");
      const runtimeLabel = command.runtime === MLX_RUNTIME ? "MLX" : "llama";
      summary.textContent = `${runtimeLabel} · ${command.server_host}:${command.server_port} · Context ${command.context_size}${command.draft_model ? " · Draft" : ""}`;
      button.append(name, summary);
      button.addEventListener("click", () => fillForm(command));
      container.append(button);
    });
  }

  function renderPreview() {
    const command = commandPayload();
    const isMLX = command.runtime === MLX_RUNTIME;
    const args = [
      ...command.extra_args,
      "--model", isMLX ? "<選擇的 MLX 模型目錄>" : "<選擇的 GGUF>"
    ];
    if (!isMLX && command.draft_model) args.push("--model-draft", command.draft_model);
    args.push("--host", command.server_host || "<Host>", "--port", String(command.server_port || "<Port>"));
    if (isMLX) {
      args.push("--max-kv-size", String(command.context_size || "<Context>"));
    } else {
      args.push(
        "--ctx-size", String(command.context_size || "<Context>"),
        "--n-gpu-layers", String(Number.isFinite(command.gpu_layers) ? command.gpu_layers : "<GPU Layers>")
      );
      if (command.threads > 0) args.push("--threads", String(command.threads));
    }
    byId("commandPreview").textContent = [isMLX ? "mlx-server" : "llama-server", ...args].join(" ");
  }

  function renderRuntimeFields() {
    const isMLX = byId("commandRuntime").value === MLX_RUNTIME;
    byId("draftModelField").hidden = isMLX;
    byId("gpuLayersField").hidden = isMLX;
    byId("threadsField").hidden = isMLX;
    if (isMLX) byId("commandDraftModel").value = "";
  }

  async function loadCommands(preferredID = "") {
    const payload = await api("/api/startup-commands");
    state.commands = payload.commands || [];
    const selected = state.commands.find((command) => command.id === preferredID)
      || state.commands.find((command) => command.id === state.selectedID)
      || state.commands[0];
    fillForm(selected);
  }

  async function loadModels() {
    const payload = await api(`/api/models?runtime=${encodeURIComponent(LLAMA_RUNTIME)}`);
    state.models = payload.models || [];
    const datalist = byId("draftModelOptions");
    datalist.replaceChildren();
    state.models.filter((model) => !/(^|[\/_.-])mmproj([\/_.-]|$)/i.test(model.path)).forEach((model) => {
      const option = document.createElement("option");
      option.value = model.path;
      datalist.append(option);
    });
  }

  byId("newCommandButton").addEventListener("click", () => fillForm(null));
  byId("commandForm").addEventListener("input", () => {
    renderRuntimeFields();
    renderPreview();
  });
  byId("commandForm").addEventListener("submit", async (event) => {
    event.preventDefault();
    const button = byId("saveCommandButton");
    button.disabled = true;
    byId("commandMessage").textContent = "";
    try {
      const path = state.selectedID
        ? `/api/startup-commands/${encodeURIComponent(state.selectedID)}`
        : "/api/startup-commands";
      const saved = await api(path, {
        method: state.selectedID ? "PUT" : "POST",
        body: JSON.stringify(commandPayload())
      });
      await loadCommands(saved.id);
      byId("commandMessage").textContent = "啟動參數已保存。";
      showMessage("啟動參數已保存");
    } catch (error) {
      byId("commandMessage").textContent = error.message;
    } finally {
      button.disabled = false;
    }
  });

  byId("deleteCommandButton").addEventListener("click", async () => {
    if (!state.selectedID || !window.confirm("確定要刪除這組啟動參數嗎？")) return;
    const button = byId("deleteCommandButton");
    button.disabled = true;
    try {
      await api(`/api/startup-commands/${encodeURIComponent(state.selectedID)}`, { method: "DELETE" });
      state.selectedID = "";
      await loadCommands();
      showMessage("啟動參數已刪除");
    } catch (error) {
      showMessage(error.message, "error");
    } finally {
      button.disabled = state.commands.length <= 1;
    }
  });

  Promise.all([loadModels(), loadCommands()]).catch((error) => showMessage(error.message, "error"));
})();
