const { test } = require("node:test");
const assert = require("node:assert/strict");
const { consume } = require("../website/assets/chat-stream.js");
const encoder = new TextEncoder();

function streamOf(text, close = true) {
  let cancelled = false;
  const stream = new ReadableStream({
    start(controller) {
      // 每個 byte 切開，涵蓋 UTF-8、CRLF 與 event 邊界。
      for (const byte of encoder.encode(text)) controller.enqueue(Uint8Array.of(byte));
      if (close) controller.close();
    },
    cancel() { cancelled = true; }
  });
  return { stream, cancelled: () => cancelled };
}

test("保留跨 chunk 的中文、思考及 usage，DONE 後取消上游", async () => {
  const payloads = [
    { choices: [{ delta: { reasoning_content: "思考" } }] },
    { choices: [{ delta: { content: "你好" }, finish_reason: "stop" }] },
    { choices: [], usage: { completion_tokens: 2 } }
  ];
  const fixture = streamOf(": heartbeat\r\n\r\n" + payloads.map((p) => `data: ${JSON.stringify(p)}\r\n\r\n`).join("") + "data: [DONE]\r\n\r\n", false);
  const events = [];
  await consume(fixture.stream, (payload) => events.push(payload));
  assert.deepEqual(events, payloads);
  assert.equal(fixture.cancelled(), true);
  assert.equal(fixture.stream.locked, false);
});

for (const suffix of ["", "data: [DONE]"]) {
  test(`EOF 未完整收到 DONE 不得視為完成 (${JSON.stringify(suffix)})`, async () => {
    const { stream } = streamOf('data: {"choices":[{"delta":{"content":"部分回答"}}]}\n\n' + suffix);
    await assert.rejects(consume(stream, () => {}), /回答未完成/);
    assert.equal(stream.locked, false);
  });
}

for (const [data, expected] of [
  ['{"error":{"message":"測試錯誤"}}', /測試錯誤/],
  ['{invalid', /格式錯誤/]
]) {
  test(`錯誤 event 取消尚未結束的上游：${data}`, async () => {
    const fixture = streamOf(`data: ${data}\n\n`, false);
    await assert.rejects(consume(fixture.stream, () => {}), expected);
    assert.equal(fixture.cancelled(), true);
    assert.equal(fixture.stream.locked, false);
  });
}

test("讀取失敗保留原始錯誤並釋放 reader", async () => {
  const failure = new Error("network failed");
  const stream = new ReadableStream({ start(controller) { controller.error(failure); } });
  await assert.rejects(consume(stream, () => {}), (error) => error === failure);
  assert.equal(stream.locked, false);
});

test("更新畫面失敗仍取消 reader", async () => {
  const fixture = streamOf("data: {}\n\n", false);
  await assert.rejects(consume(fixture.stream, () => { throw new Error("render failed"); }), /render failed/);
  assert.equal(fixture.cancelled(), true);
  assert.equal(fixture.stream.locked, false);
});
