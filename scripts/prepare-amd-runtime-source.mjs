// 將 Tanpopo 存取控制移植到 Strix Halo（包含 Halo 通用修正）的獨立副本。
// 僅供建置入口使用；不改動標準版或呼叫者提供的原始碼。
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const project = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
const [sourceArgument, destinationArgument] = process.argv.slice(2);
if (!sourceArgument || !destinationArgument || process.argv.length !== 4) {
  throw new Error("用法：node scripts/prepare-amd-runtime-source.mjs <Halo 原始碼> <不存在的暫存目錄>");
}
const source = fs.realpathSync(sourceArgument);
const destination = path.resolve(destinationArgument);
if (fs.existsSync(destination) || destination === source || destination.startsWith(source + path.sep)) {
  throw new Error("目的地必須不存在，而且不可位於來源內部");
}
const read = (root, name) => fs.readFileSync(path.join(root, name), "utf8");
const local = (name) => read(path.join(project, "llama-server"), name);
// 同一個執行檔以環境參數切換模式；不可拿缺少此開關的一般 Halo 原始碼冒充。
if (!read(source, "ggml/src/ggml-vulkan/ggml-vulkan.cpp").includes('getenv("GGML_VK_MMV_NO_SPLIT")')) {
  throw new Error("原始碼缺少 Strix Halo Vulkan 模式切換介面");
}
function replaceOnce(text, anchor, replacement, description) {
  if (!text.includes(anchor) || text.indexOf(anchor) !== text.lastIndexOf(anchor)) {
    throw new Error(`上游介面已變更或不唯一，拒絕套用：${description}`);
  }
  return text.replace(anchor, () => replacement);
}
function between(text, start, end) {
  const index = text.indexOf(start);
  const limit = text.indexOf(end, index + start.length);
  if (index < 0 || limit < 0) throw new Error("標準版存取控制來源結構已變更");
  return text.slice(index, limit);
}

// 先在記憶體完成所有移植檢查，再複製；不接受無法確認來源的部分補丁。
const files = ["common/common.h", "common/arg.cpp", "tools/server/CMakeLists.txt", "tools/server/server-http.cpp"];
const updated = new Map(files.map((name) => [name, read(source, name)]));
if ([...updated.values()].some((text) => /openloader[_-]access/.test(text))) {
  throw new Error("請提供未修改的 Halo 原始碼；建置入口會移植目前版本的完整存取控制");
}
updated.set(files[0], replaceOnce(updated.get(files[0]),
  "    std::vector<std::string> api_keys;",
  "    std::vector<std::string> api_keys;\n    std::string openloader_access_control;", "API Key 參數欄位"));
const argumentAnchor = '    add_opt(common_arg(\n        {"--ssl-key-file"}';
const argumentBlock = between(local(files[1]),
  '    add_opt(common_arg(\n        {"--openloader-access-control"}', argumentAnchor);
updated.set(files[1], replaceOnce(updated.get(files[1]), argumentAnchor, argumentBlock + argumentAnchor, "CLI 參數"));
const cmakeAnchor = "set(TARGET llama-server-impl)\n\nadd_library(${TARGET}\n";
updated.set(files[2], replaceOnce(updated.get(files[2]), cmakeAnchor,
  cmakeAnchor + "    openloader-access-control.cpp\n    openloader-access-control.h\n", "HTTP 程式庫建置"));
const localHTTP = local(files[3]);
let http = updated.get(files[3]);
http = replaceOnce(http, '#include "server-http.h"', '#include "server-http.h"\n#include "openloader-access-control.h"', "HTTP 標頭");
for (const header of ["algorithm", "cctype", "memory"]) {
  if (!http.includes(`#include <${header}>`)) http = `#include <${header}>\n` + http;
}
const helperAnchor = "// For Google Cloud Platform deployment compatibility";
http = replaceOnce(http, helperAnchor,
  between(localHTTP, "static std::string openloader_trim", helperAnchor) + helperAnchor, "API Key 擷取");
const middlewareAnchor = "    auto middleware_validate_api_key =";
http = replaceOnce(http, middlewareAnchor,
  between(localHTTP, "    const auto openloader_access =", middlewareAnchor) + middlewareAnchor, "存取控制 middleware");
const routingAnchor = "srv->set_pre_routing_handler([&params, ";
http = replaceOnce(http, routingAnchor, routingAnchor + "middleware_validate_openloader_access, ", "middleware 捕獲");
const optionsAnchor = '        if (req.method == "OPTIONS") {';
const accessCheck = between(localHTTP,
  "        // CORS preflight has no API key", "        // If this is OPTIONS request");
http = replaceOnce(http, optionsAnchor, accessCheck + optionsAnchor, "所有請求先通過 IP 政策");
if (!http.includes("get_public_endpoints")) throw new Error("上游缺少公開端點定義");
updated.set(files[3], http);

fs.cpSync(source, destination, {
  recursive: true, errorOnExist: true, force: false,
  filter: (name) => ![".git", "build", "node_modules"].includes(path.basename(name)),
});
for (const [name, text] of updated) fs.writeFileSync(path.join(destination, name), text);
for (const name of ["openloader-access-control.cpp", "openloader-access-control.h"]) {
  fs.copyFileSync(path.join(project, "llama-server/tools/server", name), path.join(destination, "tools/server", name));
}
console.log("已在獨立建置副本移植 Tanpopo 存取控制；來源與標準版保持不變。");
