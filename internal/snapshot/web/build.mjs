// 生成 Go 要嵌入的播放组件。模块和 worker 分别打包，导出的 HTML 在 file:// 下也能启动。
import { execFileSync } from "node:child_process";
import { createRequire } from "node:module";
import { mkdirSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { gzipSync } from "node:zlib";
import { defaultAssets } from "../../../../../neomega-r2/r2/scripts/default-assets.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const core = fileURLToPath(new URL("../../../../../neomega-r2/r2/", import.meta.url));
const dist = join(core, "dist");
// Install this Viewer shader overlay before embedding shader resources.
execFileSync(process.execPath, [fileURLToPath(new URL("../../../web/scmod/build.mjs", import.meta.url)), core], { stdio: "inherit" });
const { build } = createRequire(join(core, "package.json"))("esbuild");
async function bundle(entry, format = "esm") {
  const result = await build({
    entryPoints: [entry], write: false, bundle: true, format, target: "es2022",
    plugins: [{
      name: "r2",
      setup(build) {
        build.onResolve({ filter: /^\/r2\// }, ({ path }) => ({ path: join(dist, path.slice(4)) }));
      },
    }],
  });
  return result.outputFiles[0].text;
}

const resources = {};
for (const entry of defaultAssets.getEntries()) {
  const file = entry.name;
  if ((!file.startsWith("base-") && file.endsWith(".bin")) || ["catalog.json", "textures-info.json"].includes(file)) {
    resources[file] = gzipSync(entry.getData()).toString("base64");
  }
}
for (const file of readdirSync(join(dist,"render")).filter(name => name.endsWith(".wgsl") || name.endsWith(".frag"))) {
  resources[file] = gzipSync(readFileSync(join(dist, "render", file))).toString("base64");
}
const shared = name => readFileSync(join(dist, "common_ui", name), "utf8");
const actions = '<span id="building-name"></span><select id="export-dimension" aria-label="维度" hidden></select>';
const layout = shared("layout.html").replace('<slot name="leading-actions"></slot>', "").replace('<slot name="actions"></slot>', actions).replace('<slot name="settings-extra"></slot>', "");
const [main, worker] = await Promise.all([bundle(join(here, "main.ts")), bundle(join(dist, "scene/worker.js"), "iife")]);
const player = `<style>${shared("style.css")}
#building-name { max-width: 24ch; overflow: hidden; text-overflow: ellipsis; }
</style>
${layout}
<script type="application/json" id="r2-resources">${JSON.stringify(resources)}</script>
<script type="text/plain" id="r2-worker">${worker}</script>
<script type="module">${main}</script>`;
mkdirSync(join(here, "dist"), { recursive: true });
writeFileSync(join(here, "dist/player.html"), player);
