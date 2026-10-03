// 串流讀取與畫面分離，讓正常完成、異常及取消走同一套清理流程。
((root) => {
  async function consume(body, onEvent) {
    const reader = body.getReader();
    const decoder = new TextDecoder();
    // event 可能跨許多封包；只保留分隔符所需的尾端，完整片段等 event 結束才合併。
    let tail = "";
    let fragments = [];
    const boundaryPattern = /\r?\n\r?\n/g;
    let receivedDone = false;
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
      try { payload = JSON.parse(data); } catch (_error) {
        throw new Error("模型 Runtime 串流格式錯誤");
      }
      if (payload?.error) throw new Error(payload.error.message || "模型 Runtime 串流失敗");
      onEvent(payload);
    };

    try {
      while (!receivedDone) {
        const { value, done } = await reader.read();
        const text = tail + decoder.decode(value || new Uint8Array(), { stream: !done });
        boundaryPattern.lastIndex = 0;
        let start = 0;
        let boundary = boundaryPattern.exec(text);
        while (boundary) {
          const eventText = fragments.join("") + text.slice(start, boundary.index);
          fragments = [];
          start = boundary.index + boundary[0].length;
          consumeEvent(eventText);
          if (receivedDone) break;
          boundary = boundaryPattern.exec(text);
        }
        const split = Math.max(start, text.length - 3);
        if (split > start) fragments.push(text.slice(start, split));
        tail = text.slice(split);
        if (done) break;
      }
      // EOF 不是完成訊號；未結束的 SSE event 也不可當成完整回答。
      if (!receivedDone) throw new Error("模型 Runtime 串流中斷，回答未完成");
    } finally {
      try { await reader.cancel(); } catch (_error) { /* 保留原始串流錯誤。 */ }
      reader.releaseLock();
    }
  }

  if (typeof module === "object" && module.exports) module.exports = { consume };
  else root.TanpopoChatStream = { consume };
})(globalThis);
