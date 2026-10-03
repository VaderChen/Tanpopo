// 只量測正式 SSE reader；使用記憶體串流，沒有網路或 DOM 的時間。
const path = require('node:path');
const assert = require('node:assert/strict');
const { consume } = require(path.resolve(process.argv[2] || 'website/assets/chat-stream.js'));
const encoder = new TextEncoder();
const cases = [
  { name: '一般事件跨封包', count: 2000, content: '中文內容', chunkBytes: 97 },
  { name: '大型事件跨小封包', count: 1, content: '中文字'.repeat(32768), chunkBytes: 32 }
];
(async () => {
  const results = [];
  for (const fixture of cases) {
    const bytes = encoder.encode(`data: ${JSON.stringify({ content: fixture.content })}\r\n\r\n`.repeat(fixture.count) + 'data: [DONE]\r\n\r\n');
    const chunks = [];
    for (let offset = 0; offset < bytes.length; offset += fixture.chunkBytes) chunks.push(bytes.subarray(offset, offset + fixture.chunkBytes));
    const samples = [];
    for (let sample = 0; sample < 6; sample++) {
      let position = 0, count = 0;
      const stream = new ReadableStream({pull(controller) {
        if (position < chunks.length) controller.enqueue(chunks[position++]); else controller.close();
      }});
      const start = performance.now();
      await consume(stream, event => { assert.equal(event.content, fixture.content); count++; });
      const elapsed = performance.now() - start;
      assert.equal(count, fixture.count); assert(!stream.locked);
      if (sample) samples.push(elapsed);
    }
    results.push({ name: fixture.name, events: fixture.count, bytes: bytes.length, chunk_bytes: fixture.chunkBytes,
      samples_ms: samples, median_ms: samples.toSorted((a,b)=>a-b)[2], verified: true });
  }
  console.log(JSON.stringify(results, null, 2));
})().catch(error => { console.error(error); process.exitCode = 1; });
