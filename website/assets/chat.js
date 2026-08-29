(() => {
  const { api, byId, showMessage, t } = window.LlamaLoader;
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
    indicator.setAttribute("aria-label", t("思考中"));
    const dots = document.createElement("span");
    dots.className = "chat-thinking-dots";
    for (let index = 0; index < 3; index += 1) {
      const dot = document.createElement("i");
      dot.setAttribute("aria-hidden", "true");
      dots.append(dot);
    }
    const label = document.createElement("span");
    label.textContent = t("思考中");
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
    byId("sendChatButton").textContent = state.waiting ? t("生成中…") : t("傳送");
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
    roleLabel.textContent = role === "user" ? t("你") : options.error ? t("錯誤") : "Tanpopo";
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
        summary.textContent = t("思考過程");
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

  function createStreamingMessage() {
    byId("chatEmptyState")?.remove();
    const row = document.createElement("article");
    row.className = "chat-message assistant pending";
    const bubble = document.createElement("div");
    bubble.className = "chat-bubble";
    const roleLabel = document.createElement("span");
    roleLabel.className = "chat-message-role";
    roleLabel.textContent = "Tanpopo";
    const indicator = createThinkingIndicator();
    const thinking = document.createElement("details");
    thinking.className = "chat-reasoning";
    thinking.open = true;
    thinking.hidden = true;
    const summary = document.createElement("summary");
    summary.textContent = t("思考過程");
    const reasoningContent = document.createElement("div");
    reasoningContent.className = "chat-reasoning-content markdown-content";
    thinking.append(summary, reasoningContent);
    const message = document.createElement("div");
    message.className = "chat-message-content markdown-content";
    message.hidden = true;
    const meta = document.createElement("span");
    meta.className = "chat-message-meta";
    meta.hidden = true;
    bubble.append(roleLabel, indicator, thinking, message, meta);
    row.append(bubble);
    byId("chatMessages").append(row);
    scrollToLatest();
    return { row, indicator, thinking, reasoningContent, message, meta };
  }

  function splitStreamingReasoning(value) {
    let content = String(value || "");
    const reasoningParts = [];
    content = content.replace(/<think\b[^>]*>([\s\S]*?)<\/think\s*>/gi, (_match, reasoning) => {
      if (String(reasoning).trim()) reasoningParts.push(String(reasoning).trim());
      return "";
    });
    const closeMatch = /<\/think\s*>/i.exec(content);
    if (closeMatch) {
      const before = content.slice(0, closeMatch.index).trim();
      if (before) reasoningParts.push(before);
      content = content.slice(closeMatch.index + closeMatch[0].length);
    } else {
      const openMatch = /<think\b[^>]*>/i.exec(content);
      if (openMatch) {
        const after = content.slice(openMatch.index + openMatch[0].length).trim();
        if (after) reasoningParts.push(after);
        content = content.slice(0, openMatch.index);
      }
    }
    const labeled = /^\s*(?:#{1,6}\s*)?(?:\*\*)?thinking process(?:\*\*)?\s*:\s*([\s\S]*?)\n\s*(?:#{1,6}\s*)?(?:\*\*)?final answer(?:\*\*)?\s*:\s*([\s\S]*)$/i.exec(content);
    if (labeled) {
      if (labeled[1].trim()) reasoningParts.push(labeled[1].trim());
      content = labeled[2];
    } else {
      const thinkingOnly = /^\s*(?:#{1,6}\s*)?(?:\*\*)?thinking process(?:\*\*)?\s*:\s*([\s\S]*)$/i.exec(content);
      if (thinkingOnly) {
        if (thinkingOnly[1].trim()) reasoningParts.push(thinkingOnly[1].trim());
        content = "";
      }
    }
    return { content: content.trim(), reasoning: reasoningParts.join("\n\n") };
  }

  function mergeReasoning(explicitReasoning, embeddedReasoning) {
    const explicit = String(explicitReasoning || "").trim();
    const embedded = String(embeddedReasoning || "").trim();
    if (!explicit) return embedded;
    if (!embedded || explicit.includes(embedded)) return explicit;
    return `${explicit}\n\n${embedded}`;
  }

  function updateStreamingMessage(view, snapshot, complete = false) {
    const split = splitStreamingReasoning(snapshot.content);
    const reasoning = mergeReasoning(snapshot.reasoning, split.reasoning);
    let content = split.content;
    if (complete && !content) content = t("模型未提供最終回答");
    view.indicator.hidden = Boolean(reasoning || content || complete);
    view.thinking.hidden = !reasoning;
    view.message.hidden = !content;
    if (reasoning) renderMarkdown(view.reasoningContent, reasoning);
    if (content) renderMarkdown(view.message, content);
    const metaText = complete ? usageText(snapshot.usage) : "";
    view.meta.hidden = !metaText;
    view.meta.textContent = metaText;
    view.row.classList.toggle("has-reasoning", Boolean(reasoning));
    view.row.classList.toggle("pending", !complete);
    scrollToLatest();
    return { content, reasoning };
  }

  function decodeDeltaContent(value) {
    if (typeof value === "string") return value;
    if (!Array.isArray(value)) return "";
    return value.map((part) => typeof part === "string" ? part : String(part?.text || "")).join("");
  }

  async function responseError(response) {
    const text = await response.text();
    let payload = {};
    try { payload = text ? JSON.parse(text) : {}; } catch (_error) { payload = {}; }
    if (response.status === 401) {
      location.replace("/login.html");
      return new Error(t("登入狀態已失效"));
    }
    return new Error(payload?.error?.message || `${t("請求失敗")} (${response.status})`);
  }

  async function streamChat(messages, runtimeKey, onUpdate) {
    const headers = {
      "Accept": "text/event-stream",
      "Content-Type": "application/json"
    };
    if (runtimeKey) headers["X-Tanpopo-Key"] = runtimeKey;
    const response = await fetch("/api/chat/completions", {
      method: "POST",
      credentials: "same-origin",
      cache: "no-store",
      headers,
      body: JSON.stringify({ messages, stream: true })
    });
    if (!response.ok) throw await responseError(response);
    if (!response.body) throw new Error(t("目前的瀏覽器不支援串流回應"));

    const reader = response.body.getReader();
    const decoder = new TextDecoder();
    const snapshot = { content: "", reasoning: "", usage: null, finishReason: "" };
    const startedAt = performance.now();
    let buffer = "";
    let receivedDone = false;
    let frameID = 0;
    const notify = () => {
      if (frameID) return;
      frameID = window.requestAnimationFrame(() => {
        frameID = 0;
        onUpdate(snapshot, false);
      });
    };
    const consumeEvent = (eventText) => {
      const data = eventText.split(/\r?\n/)
        .filter((line) => line.startsWith("data:"))
        .map((line) => line.slice(5).replace(/^ /, ""))
        .join("\n");
      if (!data) return;
      if (data === "[DONE]") {
        receivedDone = true;
        return;
      }
      let payload;
      try { payload = JSON.parse(data); } catch (_error) { return; }
      if (payload?.error) throw new Error(payload.error.message || t("模型 Runtime 串流失敗"));
      const choice = payload?.choices?.[0] || {};
      const delta = choice.delta || {};
      snapshot.content += decodeDeltaContent(delta.content);
      snapshot.reasoning += decodeDeltaContent(delta.reasoning_content ?? delta.reasoning);
      if (choice.finish_reason) snapshot.finishReason = String(choice.finish_reason);
      if (payload?.usage) snapshot.usage = payload.usage;
      notify();
    };

    while (!receivedDone) {
      const { value, done } = await reader.read();
      buffer += decoder.decode(value || new Uint8Array(), { stream: !done });
      let boundary = /\r?\n\r?\n/.exec(buffer);
      while (boundary) {
        const eventText = buffer.slice(0, boundary.index);
        buffer = buffer.slice(boundary.index + boundary[0].length);
        consumeEvent(eventText);
        if (receivedDone) break;
        boundary = /\r?\n\r?\n/.exec(buffer);
      }
      if (done) break;
    }
    if (!receivedDone && buffer.trim()) consumeEvent(buffer);
    if (frameID) {
      window.cancelAnimationFrame(frameID);
      frameID = 0;
    }
    if (!snapshot.content && !snapshot.reasoning) throw new Error(t("模型 Runtime 沒有回傳對話內容"));
    const completionTokens = Number(snapshot.usage?.completion_tokens || 0);
    if (completionTokens > 0 && !Number(snapshot.usage?.tokens_per_second || 0)) {
      const elapsedSeconds = Math.max((performance.now() - startedAt) / 1000, 0.001);
      snapshot.usage = { ...snapshot.usage, tokens_per_second: completionTokens / elapsedSeconds };
    }
    return snapshot;
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
      const processRunning = Boolean(status.running);
      state.running = processRunning && Boolean(status.ready);
      byId("chatStatusDot").classList.toggle("online", state.running);
      if (state.running) {
        const runtime = status.runtime || "模型服務";
        byId("chatStatusLabel").textContent = `${runtime} 已就緒`;
        byId("chatStatusMeta").textContent = displayName(status.model);
        byId("chatComposerHint").textContent = "目前對話只保留在此頁面；重新整理即可清除。";
      } else if (processRunning) {
        const runtime = status.runtime || "模型服務";
        byId("chatStatusLabel").textContent = `${runtime} · ${t("載入模型中…")}`;
        byId("chatStatusMeta").textContent = displayName(status.model);
        byId("chatComposerHint").textContent = t("模型載入完成後即可開始對話。");
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
    const streaming = createStreamingMessage();
    updateComposer();

    try {
      let latest = { content: "", reasoning: "", usage: null };
      const result = await streamChat(
        state.messages,
        byId("chatRuntimeKey").value.trim(),
        (snapshot) => {
          latest = updateStreamingMessage(streaming, snapshot, false);
        }
      );
      latest = updateStreamingMessage(streaming, result, true);
      const assistant = { role: "assistant", content: latest.content };
      state.messages.push(assistant);
    } catch (error) {
      streaming.row.remove();
      const message = t(error.message);
      appendMessage("assistant", message, { error: true });
      showMessage(message, "error");
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
