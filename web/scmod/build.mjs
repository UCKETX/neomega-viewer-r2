// Static SCMod triangles use reference.w=2, leaving R2's existing quads
// (flags 0/1) unchanged. Patch the built shader, not the pinned dependency.
import { execFileSync } from "node:child_process";
import { cpSync, mkdirSync, mkdtempSync, readFileSync, rmSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const root = resolve(process.argv[2]);
const scratch = mkdtempSync(join(tmpdir(), "r2-scmod-"));
try {
  for (const folder of ["src/render", "webgl2gpu", "dist/render"]) mkdirSync(join(scratch, folder), { recursive: true });
  for (const file of ["src/render/objects.frag", "webgl2gpu/convert.cjs"]) cpSync(join(root, file), join(scratch, file));
  symlinkSync(join(root, "node_modules"), join(scratch, "node_modules"), process.platform === "win32" ? "junction" : "dir");
  const target = join(root, "dist/render/ray.frag");
  const original = readFileSync(target, "utf8");
  const guard = "if(u<0.0||u>1.0||v<0.0||v>1.0)continue;";
  const clip = "if(u<0.0||u>1.0||v<0.0||v>1.0||(reference.w==2u&&u+v>1.00003))continue;";
  if (!original.includes(clip) && original.split(guard).length !== 2) throw Error("SCMod triangle shader hook changed");
  const shader = original.replace(guard, clip);
  writeFileSync(join(scratch, "src/render/ray.frag"), shader);
  const variants = ["basic", "basic-pick", "shading", "shading-retry", "accumulation", "accumulation-retry", "photo", "photo-retry"];
  execFileSync(process.execPath, ["-e",
    "const {convert}=require('./webgl2gpu/convert.cjs'); for (const name of JSON.parse(process.argv[1])) convert(name, 'dist/render');",
    JSON.stringify(variants)], { cwd: scratch, stdio: "inherit" });
  writeFileSync(target, shader);
  for (const name of variants) cpSync(join(scratch, "dist/render", name + ".wgsl"), join(root, "dist/render", name + ".wgsl"));
} finally {
  rmSync(scratch, { recursive: true, force: true });
}
