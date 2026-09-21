// 機器能力由伺服器回報；不可依瀏覽器平台猜測 GPU 或自行放寬顯示條件。
(() => {
  const AMD = "amd-vulkan";
  let detectedMode = "vulkan";
  const commandsBySelect = new WeakMap();
  const isVariant = (value) => value === AMD;
  const family = (value) => isVariant(value) ? "llama-server" : (value || "llama-server");
  const optionValue = (command) => isVariant(command?.runtime_variant) ? command.runtime_variant : (command?.runtime || "llama-server");
  function mode(command) {
    if (command?.amd_mode) return command.amd_mode;
    const args = command?.extra_args || [];
    let requested = "auto";
    for (let index = 0; index < args.length; index++) {
      if (args[index] === "--tanpopo-amd-mode") requested = args[++index];
      else if (args[index].startsWith("--tanpopo-amd-mode=")) requested = args[index].slice("--tanpopo-amd-mode=".length);
    }
    return requested === "auto" ? detectedMode : requested;
  }
  const label = (value, command) => value === AMD
    ? (mode(command) === "strix-halo" ? "LLaMA Server（Strix Halo）" : "LLaMA Server（AMD Vulkan）")
    : value === "mlx-server" ? "mlx-server" : "LLaMA Server（標準版）";
  function updateLabel(select, command) {
    const option = Array.from(select.options).find((item) => item.value === AMD);
    // 其他 Runtime 的參數不可覆寫 AMD 選項；未選取時使用切換後會選中的首組 AMD 參數。
    const amdCommand = isVariant(command?.runtime_variant)
      ? command
      : (commandsBySelect.get(select) || []).find((item) => item.runtime_variant === AMD);
    if (option) option.textContent = label(AMD, amdCommand) + (option.dataset.unavailableReason ? ` · 不可用：${option.dataset.unavailableReason}` : "");
  }
  function apply(select, capabilities = [], commands = []) {
    commandsBySelect.set(select, commands);
    const previous = select.value;
    select.querySelectorAll("option[data-runtime-variant]").forEach((option) => option.remove());
    const entries = new Map(capabilities.map((capability) => [capability.variant, capability]));
    for (const command of commands) {
      if (isVariant(command.runtime_variant) && !entries.has(command.runtime_variant)) {
        entries.set(command.runtime_variant, { variant: command.runtime_variant, available: false, reason: "找不到指定的 Runtime 版本" });
      }
    }
    for (const capability of entries.values()) {
      if (!isVariant(capability.variant)) continue;
      if (capability.available !== true && !commands.some((command) => command.runtime_variant === capability.variant)) continue;
      detectedMode = capability.mode || "vulkan";
      const option = document.createElement("option");
      option.value = capability.variant;
      option.dataset.runtimeVariant = capability.variant;
      option.dataset.unavailableReason = capability.available === true ? "" : (capability.reason || "Runtime 暫時不可用");
      select.append(option);
      updateLabel(select);
    }
    select.value = Array.from(select.options).some((option) => option.value === previous) ? previous : "llama-server";
  }
  function restore(select, command) {
    const value = optionValue(command);
    if (Array.from(select.options).some((option) => option.value === value)) select.value = value;
    else if (isVariant(value)) select.value = "llama-server";
    updateLabel(select, command);
  }
  window.TanpopoRuntimeVariants = { AMD, isVariant, family, optionValue, mode, label, updateLabel, apply, restore };
})();
