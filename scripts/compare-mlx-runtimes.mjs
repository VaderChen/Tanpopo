// 同一模型依序比較兩份原生 Runtime；不修改使用者設定或啟停既有服務。
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import net from 'node:net';
import crypto from 'node:crypto';
import { spawn } from 'node:child_process';
import { once } from 'node:events';
import { setTimeout as sleep } from 'node:timers/promises';

const [configPath, outputPath] = process.argv.slice(2);
assert(configPath && outputPath, '用法：node scripts/compare-mlx-runtimes.mjs 設定.json 新結果目錄');
const config = JSON.parse(fs.readFileSync(configPath, 'utf8'));
assert(Number.isInteger(config.samples ?? 3) && (config.samples ?? 3) >= 1 && (config.samples ?? 3) <= 100,
  'samples 必須為 1–100 的整數');
const output = path.resolve(outputPath);
fs.mkdirSync(output, { recursive: true });
const reportFile = path.join(output, 'result.json');
const reportFD = fs.openSync(reportFile, 'wx');
fs.closeSync(reportFD);
const report = { started_at: new Date().toISOString(), rounds: [], passed: false };
const save = () => fs.writeFileSync(reportFile, JSON.stringify(report, null, 2) + '\n');
const say = value => console.log(new Date().toISOString(), value);
const hash = file => crypto.createHash('sha256').update(fs.readFileSync(file)).digest('hex');
const normalized = response => ({ choices: response.choices.map(c => ({ message: c.message, finish_reason: c.finish_reason })),
  usage: { prompt_tokens: response.usage.prompt_tokens, completion_tokens: response.usage.completion_tokens, total_tokens: response.usage.total_tokens } });
let child;
async function stop() {
  if (!child) return;
  const owned = child;
  if (owned.pid && owned.exitCode === null && owned.signalCode === null) {
    const exited = once(owned, 'exit');
    owned.kill('SIGTERM');
    const watchdog = setTimeout(() => owned.kill('SIGKILL'), 10000);
    try { await exited; } finally { clearTimeout(watchdog); }
  }
  child = undefined;
}
async function start(binary, model, label) {
  const listener = net.createServer();
  listener.listen(0, '127.0.0.1');
  await once(listener, 'listening');
  const port = listener.address().port;
  await new Promise(resolve => listener.close(resolve));
  const log = fs.openSync(path.join(output, label + '.log'), 'wx');
  const args = ['--model', model.path, '--model-type', 'text', '--host', '127.0.0.1', '--port', String(port),
    '--context-size', '8192', '--prefill-step-size', '128', '--no-thinking', '--temperature', '0', '--no-mmap', ...(model.args ?? [])];
  child = spawn(binary, args, { stdio: ['ignore', log, log] });
  fs.closeSync(log);
  let error;
  child.on('error', e => { error = e; });
  const base = `http://127.0.0.1:${port}`;
  for (let i = 0; i < 240; i++) {
    if (error) throw error;
    assert(child.exitCode === null && child.signalCode === null, 'Runtime 提前結束：' + label);
    try {
      const response = await fetch(base + '/health', { signal: AbortSignal.timeout(1500) });
      if (response.ok) return { base, health: await response.json(), args };
    } catch {}
    await sleep(500);
  }
  throw Error('模型載入逾時：' + label);
}
async function chat(base, body) {
  const started = performance.now();
  const response = await fetch(base + '/v1/chat/completions', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ temperature: 0, seed: 42, ...body }), signal: AbortSignal.timeout(120000) });
  const result = await response.json();
  assert.equal(response.status, 200, JSON.stringify(result));
  assert(result.choices?.[0]?.message?.content || result.choices?.[0]?.message?.reasoning_content, '模型沒有輸出內容');
  return { elapsed_ms: performance.now() - started, tokens_per_second: result.usage.tokens_per_second,
    response: normalized(result) };
}
async function stream(base, body) {
  const started = performance.now();
  const response = await fetch(base + '/v1/chat/completions', { method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ temperature: 0, seed: 42, ...body, stream: true, stream_options: { include_usage: true } }), signal: AbortSignal.timeout(120000) });
  assert.equal(response.status, 200);
  let pending = '', content = '', reasoning = '', finish, usage, firstContent, done = false;
  const decoder = new TextDecoder();
  function consume(line) {
    if (!line.startsWith('data: ')) return;
    if (line.slice(6) === '[DONE]') { done = true; return; }
    const event = JSON.parse(line.slice(6));
    assert(!event.error, JSON.stringify(event.error));
    for (const choice of event.choices ?? []) {
      if (firstContent === undefined && (choice.delta?.content || choice.delta?.reasoning_content)) {
        firstContent = performance.now() - started;
      }
      content += choice.delta?.content ?? '';
      reasoning += choice.delta?.reasoning_content ?? '';
      if (choice.finish_reason) finish = choice.finish_reason;
    }
    if (event.usage) usage = event.usage;
  }
  for await (const chunk of response.body) {
    pending += decoder.decode(chunk, { stream: true });
    const lines = pending.split(/\r?\n/);
    pending = lines.pop();
    lines.forEach(consume);
  }
  (pending + decoder.decode()).split(/\r?\n/).forEach(consume);
  assert(done && usage, 'SSE 缺少完成事件或用量');
  return { elapsed_ms: performance.now() - started, first_content_ms: firstContent, content, reasoning, finish_reason: finish, usage };
}
const prompts = [
  { name: '英文短提示', body: { messages: [{ role: 'user', content: 'Describe how a small bird finds food in a forest. Use at least 150 words and no heading.' }], max_tokens: 64 } },
  { name: '中文多輪', body: { messages: [{ role: 'user', content: '請記住密語是藍色松鼠。' }, { role: 'assistant', content: '我記住了。' }, { role: 'user', content: '剛才的密語是什麼？只輸出密語。' }], max_tokens: 24 } },
  { name: '長提示', body: { messages: [{ role: 'user', content: 'Summarize the following observations in English.\n' + 'A small bird collected seeds near a forest stream. The weather was calm and the water was clear.\n'.repeat(80) }], max_tokens: 64 } },
];
try {
  assert(Array.isArray(config.models) && config.models.length > 0);
  report.binaries = Object.fromEntries(['baseline', 'candidate'].map(key => [key, { path: path.resolve(config[key]), sha256: hash(config[key]) }]));
  report.models = config.models.map(model => ({ name: model.name, path: model.path, args: model.args ?? [], config_sha256: hash(path.join(model.path, 'config.json')) }));
  const expected = new Map();
  // ABBA：每次重新載入，暖機不列入；同一時間只運行一份 Runtime。
  for (const model of config.models) for (const [round, variant] of ['baseline', 'candidate', 'candidate', 'baseline'].entries()) {
    const label = String(report.rounds.length).padStart(2, '0') + '-' + variant;
    const row = { model: model.name, variant, round, checks: [] };
    report.rounds.push(row); save();
    try {
      const runtime = await start(report.binaries[variant].path, model, label);
      row.health = runtime.health;
      for (const prompt of prompts) {
        const key = model.name + '/' + prompt.name;
        const warm = await chat(runtime.base, prompt.body);
        if (!expected.has(key)) expected.set(key, warm.response);
        assert.deepEqual(warm.response, expected.get(key), key + ' 暖機輸出不同');
        for (let sample = 0; sample < (config.samples ?? 3); sample++) {
          const result = await chat(runtime.base, prompt.body);
          assert.deepEqual(result.response, expected.get(key), key + ' 輸出不同');
          row.checks.push({ name: prompt.name, ...result }); save();
        }
      }
      row.stream = await stream(runtime.base, prompts[0].body);
      const reference = expected.get(model.name + '/' + prompts[0].name);
      assert.equal(row.stream.content, reference.choices[0].message.content);
      assert.equal(row.stream.reasoning, reference.choices[0].message.reasoning_content ?? '');
      assert.equal(row.stream.finish_reason, reference.choices[0].finish_reason);
      for (const key of ['prompt_tokens', 'completion_tokens', 'total_tokens']) assert.equal(row.stream.usage[key], reference.usage[key]);
      row.passed = true;
      say(model.name + ' ' + variant + ' 回答、Token 與 SSE 一致');
    } finally { await stop(); row.stopped = true; save(); }
  }
  report.passed = true;
} catch (error) {
  report.error = error.stack; process.exitCode = 1; console.error(error.stack);
} finally {
  await stop(); report.completed_at = new Date().toISOString(); save();
}
