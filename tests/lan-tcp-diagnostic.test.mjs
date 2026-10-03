import { test } from "node:test";
import assert from "node:assert/strict";
import http from "node:http";
import { diagnose } from "../scripts/diagnose-lan-tcp.mjs";

test("持續連線降速時保留部分傳輸與新連線對照，並釋放連線", async () => {
  const counts = new Map();
  const sockets = new Set();
  const server = http.createServer((request, response) => {
    const count = (counts.get(request.socket) ?? 0) + 1;
    counts.set(request.socket, count);
    response.writeHead(200, { "Content-Length": 4096 });
    if (count === 2) {
      response.write(Buffer.alloc(500));
      const trickle = setInterval(() => response.write(Buffer.alloc(1)), 20);
      response.on("close", () => clearInterval(trickle));
      return;
    }
    response.end(Buffer.alloc(4096));
  });
  server.on("connection", socket => { sockets.add(socket); socket.on("close", () => sockets.delete(socket)); });
  await new Promise(resolve => server.listen(0, "127.0.0.1", resolve));
  try {
    const report = await diagnose({ url: `http://127.0.0.1:${server.address().port}`,
      requests: 3, intervalMs: 0, timeoutMs: 500, controlIntervalMs: 100 });
    assert.equal(report.completed, false);
    assert.equal(report.persistent.length, 2);
    assert.equal(report.persistent[0].error, undefined);
    assert.equal(report.persistent[1].reused, true);
    assert.ok(report.persistent[1].bytes > 500 && report.persistent[1].bytes < 4096);
    assert.match(report.persistent[1].error, /500 ms/);
    assert.ok(report.fresh.length > 0);
    assert.ok(report.fresh.every(item => !item.reused && item.bytes === 4096 && !item.error));
  } finally {
    server.closeAllConnections();
    await new Promise(resolve => server.close(resolve));
  }
  assert.equal(sockets.size, 0);
});

test("拒絕非 HTTP 端點與無界限的測量", async () => {
  await assert.rejects(diagnose({ url: "file:///tmp/fixture" }), /http\(s\)/);
  await assert.rejects(diagnose({ url: "http://user:password@127.0.0.1" }), /帳密/);
  await assert.rejects(diagnose({ url: "http://127.0.0.1", requests: 0 }), /requests/);
});
