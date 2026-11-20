// Compile a Viewer-only overlay against the pinned R2 shader without editing
// the dependency's tracked files. Core's regular WGSL compiler is reused.
import { execFileSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = resolve(process.argv[2]);
const scratch = mkdtempSync(join(tmpdir(), "r2-dawnlight-"));
try {
  for (const folder of ["src/render", "webgl2gpu", "dist/render"]) {
    mkdirSync(join(scratch, folder), { recursive: true });
  }
  for (const file of ["src/render/ray.frag", "src/render/objects.frag", "webgl2gpu/convert.cjs"]) {
    cpSync(join(root, file), join(scratch, file));
  }
  symlinkSync(join(root, "node_modules"), join(scratch, "node_modules"), process.platform === "win32" ? "junction" : "dir");
  const patch = fileURLToPath(new URL("./ray.patch", import.meta.url));
  execFileSync("git", ["apply", patch], { cwd: scratch, stdio: "inherit" });
  const variants = ["shading", "shading-retry", "accumulation", "accumulation-retry", "photo", "photo-retry"];
  execFileSync(process.execPath, ["-e",
    "const {convert}=require('./webgl2gpu/convert.cjs'); for (const name of JSON.parse(process.argv[1])) convert(name, 'dist/render');",
    JSON.stringify(variants)], { cwd: scratch, stdio: "inherit" });
  cpSync(join(scratch, "src/render/ray.frag"), join(root, "dist/render/ray.frag"));
  for (const name of variants) {
    cpSync(join(scratch, "dist/render", name + ".wgsl"), join(root, "dist/render", name + ".wgsl"));
  }
  const layout = join(root, "dist/common_ui/layout.html");
  const html = readFileSync(layout, "utf8")
    .replace("累积渲染专属", "大气效果")
    .replace("这三项只在静止累积和拍照时出现。", "同时启用太阳辉光与薄雾可在光影档预览体积散射；启用云投影后也会画出云层。");
  writeFileSync(layout, html);

} finally {
  rmSync(scratch, { recursive: true, force: true });
}
