// 以 Playwright 操作真實前端，再用 FFmpeg 產生循環 GIF。
const { chromium } = require("playwright");
const fs = require("node:fs/promises");
const path = require("node:path");
const os = require("node:os");
const assert = require("node:assert/strict");
const { execFileSync } = require("node:child_process");
const { setTimeout: delay } = require("node:timers/promises");
const { startDemoServer } = require("./server.cjs");
const fixtures = require("./fixtures.json");

async function main() {
  const preview = process.argv.includes("--preview");
  const outputIndex = process.argv.indexOf("--output");
  if (outputIndex >= 0 && !process.argv[outputIndex + 1]) throw new Error("--output 缺少目的 GIF 路徑");
  const output = path.resolve(outputIndex >= 0 ? process.argv[outputIndex + 1] : path.join(__dirname, "../../images/tanpopo-demo.gif"));
  const artifacts = await fs.mkdtemp(path.join(os.tmpdir(), "tanpopo-readme-demo-"));
  console.log(`錄製暫存：${artifacts}`);
  const demo = await startDemoServer();
  let browser, context;
  let stopCapture = false, captureTask;
  const frames = [], errors = [], blocked = [], checks = [];
  try {
    browser = await chromium.launch({ headless: true });
    context = await browser.newContext({ viewport: { width: 1280, height: 900 }, deviceScaleFactor: 1, locale: "zh-TW", timezoneId: "Asia/Taipei" });
    // 錄製只允許這個隔離伺服器；即使前端日後新增外部連線，也不會真的下載模型。
    await context.route("**/*", (route) => {
      if (new URL(route.request().url()).origin === demo.url) return route.continue();
      blocked.push(route.request().url());
      return route.abort();
    });
    const page = await context.newPage();
    page.on("pageerror", (error) => errors.push(error.message));
    page.on("response", (response) => { if (response.status() >= 400) errors.push(`${response.status()} ${response.url()}`); });
    await page.goto(`${demo.url}/demo`);
    const ui = page.frameLocator("iframe");
    const wait = (milliseconds) => delay(milliseconds);
    async function checkpoint(name, selector) {
      await ui.locator(selector).waitFor({ state: "visible" });
      const overflow = await ui.locator("body").evaluate(() => document.documentElement.scrollWidth > innerWidth);
      assert.equal(overflow, false, `${name} 出現水平溢出`);
      await page.screenshot({ path: path.join(artifacts, `${name}.png`) });
      checks.push(name);
    }
    async function move(locator) {
      await locator.scrollIntoViewIfNeeded();
      const box = await locator.boundingBox();
      assert.ok(box, "操作目標不可見");
      const x = box.x + box.width * .55, y = box.y + box.height * .55;
      await page.evaluate(({ x, y }) => {
        const pointer = document.querySelector("#pointer");
        pointer.style.left = `${x}px`; pointer.style.top = `${y}px`;
      }, { x, y });
      await page.mouse.move(x, y);
      await wait(350);
    }
    async function click(locator) {
      await move(locator);
      await page.evaluate(() => {
        const pointer = document.querySelector("#pointer");
        pointer.classList.remove("click");
        void pointer.offsetWidth;
        pointer.classList.add("click");
      });
      await locator.click();
    }
    await ui.locator("#refreshDialog").waitFor({ state: "hidden" });
    await ui.locator("#startButton:enabled").waitFor();
    await checkpoint("01-model", "#modelSelect");
    if (preview) return;

    // 從已就緒的第一張畫面開始，避免 GIF 封面出現空白或讀取中的 UI。
    // 保留每張影格的真實間隔，錄製變慢時也不會改變演示的相對節奏。
    captureTask = (async () => {
      while (!stopCapture) {
        const started = Date.now();
        const filename = `frame-${String(frames.length).padStart(4, "0")}.png`;
        await page.screenshot({ path: path.join(artifacts, filename) });
        frames.push({ filename, started });
        await wait(Math.max(0, 100 - (Date.now() - started)));
      }
    })();

    await wait(1200);
    await move(ui.locator("#modelSelect"));
    const ringModel = fixtures.models.find(model => model.architecture === "qwen3");
    await ui.locator("#modelSelect").selectOption(ringModel.path);
    await wait(900);
    await move(ui.locator("#clusterSearch"));
    await checkpoint("02-card", "#clusterPanel");
    await wait(1400);
    await click(ui.locator("#clusterSearch"));
    await ui.locator("#clusterPeerList input").first().waitFor();
    await checkpoint("03-discovery", "#clusterDialog");
    await wait(1300);
    for (const peer of fixtures.cluster.peers.filter(peer => !peer.busy)) {
      await click(ui.locator(`#clusterPeerList input[value="${peer.id}"]`));
      await wait(650);
    }
    assert.equal(await ui.locator("#clusterPeerList input:checked").count(), 2);
    assert.equal(await ui.locator("#clusterPeerList input:disabled").count(), 1);
    await checkpoint("04-selected", "#clusterStart:enabled");
    await wait(1500);
    await click(ui.locator("#clusterStart"));
    await ui.locator("#clusterStart").filter({hasText: "交握配對中"}).waitFor();
    await checkpoint("05-handshake", "#clusterDialog");
    await ui.locator("#clusterDialog").waitFor({state:"hidden"});
    await ui.locator("#clusterStatus").filter({hasText:"推論就緒"}).waitFor();
    await checkpoint("06-ready", "#clusterStop");
    assert.equal(demo.state.cluster.session.members.length, 3);
    assert.equal(demo.state.runtime.model, ringModel.path);
    await wait(2000);

    await click(ui.locator('.nav a[href="/chat.html"]'));
    await ui.locator("#chatInput:enabled").waitFor();
    await move(ui.locator("#chatInput"));
    await ui.locator("#chatInput").pressSequentially(fixtures.chat.prompt, {delay:55});
    await wait(750);
    await click(ui.locator("#sendChatButton"));
    await ui.locator(".chat-reasoning.is-streaming").waitFor();
    await checkpoint("07-streaming", ".chat-reasoning");
    await ui.locator("#clearChatButton:enabled").waitFor();
    assert.ok((await ui.locator("#chatMessages").innerText()).includes("OpenAI 相容 API"));
    await checkpoint("08-chat", ".chat-message.assistant");
    await wait(2800);

    stopCapture = true;
    await captureTask;
    assert.deepEqual(errors, [], "瀏覽器錯誤");
    assert.deepEqual(blocked, [], "偵測到外部連線");
    assert.deepEqual(demo.state.errors, [], "示範 API 錯誤");
    assert.deepEqual(demo.state.browserErrors, [], "頁面 JavaScript 錯誤");
    assert.equal(demo.state.cluster.session.phase, "running");

    const playlist = frames.map((frame, index) => {
      const duration = index + 1 < frames.length ? (frames[index + 1].started - frame.started) / 1000 : .1;
      return `file '${frame.filename}'\nduration ${duration.toFixed(3)}\n`;
    }).join("") + `file '${frames.at(-1).filename}'\n`;
    await fs.writeFile(path.join(artifacts, "frames.txt"), playlist);
    const encoded = path.join(artifacts, "tanpopo-demo.gif");
    const source = ["-hide_banner", "-loglevel", "error", "-y", "-f", "concat", "-safe", "0", "-i", path.join(artifacts, "frames.txt")];
    const palette = path.join(artifacts, "palette.png");
    execFileSync("ffmpeg", [...source, "-vf", "fps=10,palettegen=stats_mode=diff", "-frames:v", "1", palette], { stdio: "inherit" });
    execFileSync("ffmpeg", [...source, "-i", palette,
      "-filter_complex", "[0:v]fps=10[v];[v][1:v]paletteuse=dither=bayer:bayer_scale=5:diff_mode=rectangle",
      "-loop", "0", encoded], { stdio: "inherit" });
    const probe = JSON.parse(execFileSync("ffprobe", ["-v", "error", "-count_frames", "-show_entries", "stream=width,height,nb_read_frames:format=duration,size", "-of", "json", encoded], { encoding: "utf8" }));
    assert.equal(probe.streams[0].width, 1280);
    assert.equal(probe.streams[0].height, 900);
    assert.ok(Number(probe.streams[0].nb_read_frames) > 100);
    assert.ok(Number(probe.format.size) < 8 * 1024 * 1024, "GIF 超過 8 MiB 預算");
    await fs.mkdir(path.dirname(output), { recursive: true });
    await fs.copyFile(encoded, output);
    await fs.writeFile(path.join(artifacts, "verification.json"), JSON.stringify({ checks, errors, blocked, requests: demo.state.requests, probe }, null, 2));
    console.log(JSON.stringify({ output, artifacts, checks, probe }, null, 2));
  } finally {
    stopCapture = true;
    if (captureTask) await captureTask;
    await fs.writeFile(path.join(artifacts, "diagnostics.json"), JSON.stringify({ checks, errors, blocked, requests: demo.state.requests, apiErrors: demo.state.errors, browserErrors: demo.state.browserErrors }, null, 2));
    await context?.close();
    await browser?.close();
    await demo.close();
  }
}
main().catch((error) => { console.error(error); process.exitCode = 1; });
