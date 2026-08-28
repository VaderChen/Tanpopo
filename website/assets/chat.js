(() => {
  const { api, byId, showMessage } = window.LlamaLoader;
  const state = {
    messages: [],
    running: false,
    waiting: false
  };
  const markdownRenderer = typeof window.markdownit === "function"
    ? window.markdownit({ html: false, breaks: true, linkify: true, typographer: false })
    : null;

  function renderMarkdown(container, source) {
    const content = String(source || "");
    if (!markdownRenderer) {
      container.textContent = content;
      return;
    }
    try {
      container.innerHTML = markdownRenderer.render(content);
      container.querySelectorAll("a").forEach((link) => {
        link.target = "_blank";
        link.rel = "noopener noreferrer";
      });
      if (typeof window.renderMathInElement === "function") {
        window.renderMathInElement(container, {
          delimiters: [
            { left: "$$", right: "$$", display: true },
            { left: "\\[", right: "\\]", display: true },
            { left: "\\(", right: "\\)", display: false },
            { left: "$", right: "$", display: false }
          ],
          throwOnError: false,
          strict: "warn",
          trust: false,
          ignoredTags: ["script", "noscript", "style", "textarea", "pre", "code", "option"]
        });
      }
    } catch (_error) {
      container.replaceChildren();
      container.textContent = content;
    }
  }

  function createThinkingIndicator() {
    const indicator = document.createElement("div");
    indicator.className = "chat-thinking-indicator";
    indicator.setAttribute("role", "status");
    indicator.setAttribute("aria-label", "模型思考中");
    const dots = document.createElement("span");
    dots.className = "chat-thinking-dots";
    for (let index = 0; index < 3; index += 1) {
      const dot = document.createElement("i");
      dot.setAttribute("aria-hidden", "true");
      dots.append(dot);
    }
    const label = document.createElement("span");
    label.textContent = "思考中";
    indicator.append(dots, label);
    return indicator;
  }

  function displayName(path) {
    const normalized = String(path || "").replace(/\\/g, "/").replace(/\/+$/, "");
    return normalized.split("/").pop() || "未命名模型";
  }

  function updateComposer() {
    const enabled = state.running && !state.waiting;
    byId("chatInput").disabled = !enabled;
    byId("sendChatButton").disabled = !enabled || !byId("chatInput").value.trim();
    byId("sendChatButton").textContent = state.waiting ? "生成中…" : "傳送";
    byId("clearChatButton").disabled = state.waiting || state.messages.length === 0;
  }

  function scrollToLatest() {
    const container = byId("chatMessages");
    container.scrollTop = container.scrollHeight;
  }

  function appendMessage(role, content, options = {}) {
    byId("chatEmptyState")?.remove();
    const row = document.createElement("article");
    row.className = `chat-message ${role}${options.pending ? " pending" : ""}${options.error ? " error" : ""}`;

    const bubble = document.createElement("div");
    bubble.className = "chat-bubble";
    const roleLabel = document.createElement("span");
    roleLabel.className = "chat-message-role";
    roleLabel.textContent = role === "user" ? "你" : options.error ? "錯誤" : "Tanpopo";
    bubble.append(roleLabel);

    if (options.pending) {
      bubble.append(createThinkingIndicator());
    } else {
      const reasoning = String(options.reasoning || "").trim();
      if (reasoning) {
        row.classList.add("has-reasoning");
        const thinking = document.createElement("details");
        thinking.className = "chat-reasoning";
        thinking.open = true;
        const summary = document.createElement("summary");
        summary.textContent = "思考過程";
        const reasoningContent = document.createElement("div");
        reasoningContent.className = "chat-reasoning-content markdown-content";
        renderMarkdown(reasoningContent, reasoning);
        thinking.append(summary, reasoningContent);
        bubble.append(thinking);
      }
      const message = document.createElement("div");
      message.className = "chat-message-content markdown-content";
      renderMarkdown(message, content);
      bubble.append(message);
    }

    if (options.meta) {
      const meta = document.createElement("span");
      meta.className = "chat-message-meta";
      meta.textContent = options.meta;
      bubble.append(meta);
    }
    row.append(bubble);
    byId("chatMessages").append(row);
    scrollToLatest();
    return row;
  }

  function restoreEmptyState() {
    const empty = document.createElement("div");
    empty.id = "chatEmptyState";
    empty.className = "chat-empty-state";
    const title = document.createElement("strong");
    title.textContent = "開始一段本機對話";
    const description = document.createElement("p");
    description.textContent = "訊息只會傳送給目前執行中的模型，不會保存對話紀錄。";
    empty.append(title, description);
    byId("chatMessages").replaceChildren(empty);
  }

  function usageText(usage) {
    const prompt = Number(usage?.prompt_tokens || 0);
    const completion = Number(usage?.completion_tokens || 0);
    const speed = Number(usage?.tokens_per_second || 0);
    if (!prompt && !completion) return "";
    const parts = [`輸入 ${prompt} tokens`, `輸出 ${completion} tokens`];
    if (Number.isFinite(speed) && speed > 0) parts.push(`${speed.toFixed(1)} tok/s`);
    return parts.join(" · ");
  }

  async function refreshStatus() {
    try {
      const status = await api("/api/runtime/status");
      state.running = Boolean(status.running);
      byId("chatStatusDot").classList.toggle("online", state.running);
      if (state.running) {
        const runtime = status.runtime || "模型服務";
        byId("chatStatusLabel").textContent = `${runtime} 已就緒`;
        byId("chatStatusMeta").textContent = displayName(status.model);
        byId("chatComposerHint").textContent = "目前對話只保留在此頁面；重新整理即可清除。";
      } else {
        byId("chatStatusLabel").textContent = "模型服務尚未啟動";
        byId("chatStatusMeta").textContent = "請先到執行狀態載入模型";
        byId("chatComposerHint").textContent = "請先於「執行狀態」啟動模型服務。";
      }
      updateComposer();
    } catch (error) {
      state.running = false;
      byId("chatStatusDot").classList.remove("online");
      byId("chatStatusLabel").textContent = "無法取得模型狀態";
      byId("chatStatusMeta").textContent = error.message;
      updateComposer();
    }
  }

  async function sendMessage(event) {
    event.preventDefault();
    const input = byId("chatInput");
    const content = input.value.trim();
    if (!content || !state.running || state.waiting) return;

    state.messages.push({ role: "user", content });
    appendMessage("user", content);
    input.value = "";
    input.style.height = "";
    state.waiting = true;
    const pending = appendMessage("assistant", "", { pending: true });
    updateComposer();

    try {
      const result = await api("/api/chat/completions", {
        method: "POST",
        body: JSON.stringify({ messages: state.messages }),
        headers: byId("chatRuntimeKey").value.trim()
          ? { "X-Tanpopo-Key": byId("chatRuntimeKey").value.trim() }
          : {}
      });
      pending.remove();
      const assistant = { role: "assistant", content: String(result.content || "") };
      state.messages.push(assistant);
      appendMessage("assistant", assistant.content, {
        reasoning: String(result.reasoning || ""),
        meta: usageText(result.usage)
      });
    } catch (error) {
      pending.remove();
      appendMessage("assistant", error.message, { error: true });
      showMessage(error.message, "error");
      await refreshStatus();
    } finally {
      state.waiting = false;
      updateComposer();
      if (state.running) input.focus();
    }
  }

  byId("chatForm").addEventListener("submit", sendMessage);
  byId("chatInput").addEventListener("input", (event) => {
    const input = event.currentTarget;
    input.style.height = "auto";
    input.style.height = `${Math.min(input.scrollHeight, 220)}px`;
    updateComposer();
  });
  byId("chatInput").addEventListener("keydown", (event) => {
    if (event.key === "Enter" && !event.shiftKey && !event.isComposing) {
      event.preventDefault();
      byId("chatForm").requestSubmit();
    }
  });
  byId("clearChatButton").addEventListener("click", () => {
    state.messages = [];
    restoreEmptyState();
    updateComposer();
    byId("chatInput").focus();
  });

  refreshStatus();
  window.setInterval(refreshStatus, 10000);
})();
