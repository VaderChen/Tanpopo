const { test } = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const os = require("node:os");
const path = require("node:path");
const { spawnSync } = require("node:child_process");

function fixture(t) {
  const root = fs.mkdtempSync(path.join(os.tmpdir(), "tanpopo-clean-test-"));
  t.after(() => fs.rmSync(root, { recursive: true, force: true }));
  const project = path.join(root, "project");
  const external = path.join(root, "external");
  fs.mkdirSync(project);
  fs.mkdirSync(external);
  fs.copyFileSync(path.join(__dirname, "../clean.command"), path.join(project, "clean.command"));
  return { project, external, run: (...args) => spawnSync("bash", [path.join(project, "clean.command"), ...args], { encoding: "utf8" }) };
}

test("清理不可穿過父目錄符號連結", (t) => {
  const { project, external, run } = fixture(t);
  fs.mkdirSync(path.join(external, ".build"));
  const sentinel = path.join(external, ".build", "keep");
  fs.writeFileSync(sentinel, "keep");
  fs.symlinkSync(external, path.join(project, "mlx-server"));
  const result = run();
  assert.notEqual(result.status, 0, result.stdout);
  assert.equal(fs.readFileSync(sentinel, "utf8"), "keep");
});

test("dist 清理包含隱藏檔，保留目錄且不跟隨子項符號連結", (t) => {
  const { project, external, run } = fixture(t);
  const dist = path.join(project, "dist");
  fs.mkdirSync(path.join(dist, "nested"), { recursive: true });
  fs.writeFileSync(path.join(dist, ".hidden"), "remove");
  fs.writeFileSync(path.join(dist, "nested", "old.zip"), "remove");
  fs.writeFileSync(path.join(external, "keep"), "keep");
  fs.symlinkSync(external, path.join(dist, "link"));
  const result = run("--dist");
  assert.equal(result.status, 0, result.stderr);
  assert.deepEqual(fs.readdirSync(dist), []);
  assert.equal(fs.readFileSync(path.join(external, "keep"), "utf8"), "keep");
});

test("拒絕 dist 本身為符號連結", (t) => {
  const { project, external, run } = fixture(t);
  fs.writeFileSync(path.join(external, "keep"), "keep");
  fs.symlinkSync(external, path.join(project, "dist"));
  assert.notEqual(run("--dist").status, 0);
  assert.equal(fs.readFileSync(path.join(external, "keep"), "utf8"), "keep");
});
