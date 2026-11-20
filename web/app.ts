// 应用组装存档会话、飞行/地图视图与同一射线帧循环；UI 事件和投影分别放在独立模块。
import { readTextureStream, createRenderer, ObjectRegistry, enchantingBook, WorldSession, r2linkSource } from "/r2/index.js";
import { exportSnapshot } from "./export.js";
import type { TextureSet, Renderer, RendererOptions, R2LinkSource } from "/r2/index.js";
import type { Connection, OpenOptions, Progress, ViewerWorld } from "./types.js";
import { createView, TIER_NAMES, type ViewSettings, type ViewStats } from "/r2/common_ui/view.js";
import { element, closePopovers } from "/r2/common_ui/hud.js";
import { showWorldInfo, showConnection, showWorldDetails } from "./world-info.js";
import { createPicker } from "./picker.js";
import { bindDawnlight } from "./dawnlight.js";
import { startupStage } from "/r2/common_ui/startup.js";

// Electron embeds this page at r2://app. The GLSL inspection tools explicitly request WebGL.
const useWebGPU = isSecureContext && !!navigator.gpu && location.protocol !== "r2:" && new URLSearchParams(location.search).get("backend") !== "webgl";


// 帮助使用模态窗口，阅读时暂停场景键盘操作，关闭后由浏览器归还焦点。
const help = element<HTMLDialogElement>("help");
element("help-open").onclick = () => {
  closePopovers();
  help.showModal();
};
help.onkeydown = event => event.stopPropagation();

const canvas = element<HTMLCanvasElement>("view");
const camera = { position: [0, 80, 0], yaw: -0.7, pitch: -0.32 };
const savedSettings = { ...JSON.parse(localStorage.getItem("neomega-r2-settings") || "{}") };
// 清除旧版的样本上限和阴影开关。
for (const key of ["stillSamples", "photoSamples", "shadows"]) delete savedSettings[key];
const settings: ViewSettings & { texturePath: string; textureSize: number } = {
  nearRadius: 2,
  farRadius: 32,
  resolution: "1.5",
  speed: 32,
  tier: "shading",
  time: 10,
  timeFlow: true,
  fog: true,
  stillAccumulation: false,
  photoScale: 2, photoSupersample: 1,
  exportScale: 2, exportSupersample: 4,
  texturePath: "",
  textureSize: 128,
  ...savedSettings,
};
if (!["voxel", "basic", "shading"].includes(settings.tier)) settings.tier = "shading";
const registry = new ObjectRegistry().register("enchanting_book", () =>
  enchantingBook("/r2/assets/book.png"),
);
let renderer: Renderer;
let activeTextures: TextureSet | undefined;
let exporting: AbortController | null = null;
const exportButton = element<HTMLButtonElement>("export-html");
const exportStatus = element("export-status");
let session: WorldSession | null = null;
let source: R2LinkSource | null = null;
// 外部来源握手期间不跟随本机世界的断开。
let connecting = false;
let world: ViewerWorld = { loaded: false, id: -1, name: "", position: camera.position };
let continuous = false;
/** 所有世界都经 R2Link 读取：本机存档与内置场景连接同源的 /r2link，外部数据源连接用户给的地址。
 * 内置场景额外提供开场取景与时间；外部数据源带 connection 与握手给出的相机。 */
// R2 v0.1.4 keeps its hash table and geometry references fixed for a
// renderer's lifetime. Cross-atlas transitions reload the page so both
// backends start with a matched states/catalog/geometry/texture bundle.
const materialKey = (info: ViewerWorld) => info.textureName ? `scworld/${info.id}` : "minecraft";
const reconnectKey = "neomega-r2-reconnect-once";
const scViewKey = "neomega-r2-sc-texture-view-once";
interface RetainedView {
  id: number;
  path?: string;
  position: number[];
  yaw: number;
  pitch: number;
  time: number;
}
function takeSCView(info: ViewerWorld): RetainedView | undefined {
  try {
    const item = sessionStorage.getItem(scViewKey);
    sessionStorage.removeItem(scViewKey);
    if (!item || info.kind !== "scworld") return;
    const v: RetainedView = JSON.parse(item);
    if (v.id === info.id && v.path === info.path && v.position.length === 3 &&
        [...v.position, v.yaw, v.pitch, v.time].every(Number.isFinite)) return v;
  } catch {}
}
const textureControls = ["texture-browse", "texture-apply", "texture-reset", "texture-size", "texture-path"];
let worldQueue = Promise.resolve();
/** 打开、关闭、连接与启动共用切换队列；编号不比当前世界新的切换被忽略。 */
function useWorld(info: ViewerWorld, linked?: R2LinkSource) {
  const job = worldQueue.then(() => installWorld(info, linked));
  worldQueue = job.catch(() => {});
  return job;
}
/** 本机世界的数据源。服务端切换或关闭世界时断开旧连接，这时改用服务端的当前世界（可能由别的页面打开）。 */
function localSource(info: ViewerWorld) {
  const url = new URL(`/r2link?world=${info.id}`, localServiceURL);
  url.protocol = url.protocol === "https:" ? "wss:" : "ws:";
  const own = r2linkSource(url.href, {
    retry: 0,
    onState: (state, message) => {
      // 自己关闭时没有 message。
      if (state === "closed" && message && source === own && !connecting) followServer(own, message);
    },
  });
  return own;
}
async function followServer(closed: R2LinkSource, message: string) {
  try {
    const info = await (await fetch("/api/world")).json();
    if (source !== closed || connecting) return;
    if (info.id > world.id) return await useWorld(info);
  } catch {}
  if (source === closed && !connecting) hud.error(`世界连接已断开：${message}`);
}
/** 换世界时结束拍照，按新世界的开场位置摆放相机。 */
async function installWorld(info: ViewerWorld, linked?: R2LinkSource) {
  if (world.id === info.id) { world = info; return; }
  if (world.id > info.id) return linked?.close();
  if (materialKey(info) !== materialKey(initialWorld)) {
    if (info.connection) sessionStorage.setItem(reconnectKey, info.connection.url);
    if (info.scTextureUpdate && info.kind === "scworld" && world.kind === "scworld" && info.path === world.path) {
      const retained: RetainedView = { id: info.id, path: info.path, ...camera, time: settings.time };
      try { sessionStorage.setItem(scViewKey, JSON.stringify(retained)); } catch {}
    }
    linked?.close();
    // Cancel dependent rendering without trying to feed a hash table the
    // previous renderer cannot decode.
    exporting?.abort(new Error("场景已切换"));
    session?.dispose();
    location.reload();
    return;
  }
  exporting?.abort(new Error("场景已切换"));
  await view.cancelPhoto();
  // 离开开场场景或直接载入外部来源时从白天开始；来源之间切换保留当前时间。
  const startDaytime = !info.scene && (!world.loaded || !!world.scene);
  const old = session;
  session = source = null;
  old?.dispose();
  world = info;
  closePopovers();
  showWorldInfo(info);
  showConnection(info.connection);
  syncTextureControls(info);
  const current = (source = linked || localSource(info));
  view.setWorld(info.loaded, async () => {
    await current.welcome;
    return current.chunks();
  }).catch((error) => {
    if (source === current && current.state === "connected") hud.error(String(error));
  });
  const retained = takeSCView(info);
  const start = retained ?? info.scene?.camera ?? info.camera;
  camera.position = [...(start?.position || info.position)];
  camera.yaw = start?.yaw ?? -0.7;
  camera.pitch = start?.pitch ?? -0.32;
  if (retained || info.scene || startDaytime) {
    settings.time = retained?.time ?? info.scene?.time ?? 10;
    hud.settings();
  }
  session = new WorldSession(renderer, current, {
    registry,
    onError: (message) => hud.error(message),
  });
  session.focus(camera.position, settings);
  exportButton.disabled = !!exporting;
}
/** 目录选择器与验收入口共用同一存档打开操作。 */
async function openWorld(path: string, options: OpenOptions = {}, progress?: (value: Progress) => void, signal?: AbortSignal) {
  const response = await fetch("/api/world/open", {
    method: "POST",
    headers: { "Content-Type": "application/json", "Accept": "application/x-ndjson" },
    body: JSON.stringify({ path, ...options }),
    signal,
  });
  if (!response.ok) throw Error(await response.text());
  if (!response.body) throw Error("服务端未返回打开进度");
  const reader = response.body.pipeThrough(new TextDecoderStream()).getReader();
  let pending = "";
  while (true) {
    const { value, done } = await reader.read();
    if (done) break;
    pending += value;
    let newline;
    while ((newline = pending.indexOf("\n")) >= 0) {
      const message = JSON.parse(pending.slice(0, newline));
      pending = pending.slice(newline + 1);
      if (message.type === "error") throw Error(message.message);
      if (message.type === "progress") progress?.(message.progress);
      if (message.type === "world") await useWorld(message.world);
    }
  }
}
const showCompile: NonNullable<RendererOptions["onProgress"]> = ({ index, total, label }) =>
  startupStage(`${label}（${index}/${total}）`);
await startupStage("正在读取场景信息…");
const initialResponse = await fetch("/api/world", { cache: "no-store" });
if (!initialResponse.ok) throw Error(await initialResponse.text());
const initialWorld: ViewerWorld = await initialResponse.json();
if (initialWorld.textureName) {
  const response = await fetch(`/api/world/textures?world=${initialWorld.id}`);
  if (!response.ok || !response.body) throw Error(await response.text());
  activeTextures = await readTextureStream(response.body);
}
const initialFiles = activeTextures?.files;
await startupStage("正在初始化图形设备…");
renderer = await createRenderer(canvas, {
  useWebGPU, tier: settings.tier, onProgress: showCompile,
  ...(initialFiles && { readResource: async (name: string) => {
    const data = initialFiles.get(name);
    if (data) return new Response(data);
    if (/\.(?:frag|wgsl)$/.test(name)) {
      const response = await fetch("/r2/render/" + name);
      if (response.ok) return response;
    }
    throw Error("生存战争渲染资源缺失：" + name);
  } }),
});
await startupStage("正在准备场景…");
const view = createView({
  switchBackend: location.protocol !== "r2:",
  renderer, camera, settings,
  scene: () => ({ name: world.name, ocean: world.scene?.ocean, connection: world.connection }),
  onSettings: () => localStorage.setItem("neomega-r2-settings", JSON.stringify(settings)),
  onStats: showDetails,
});
const { hud, map, frames } = view;
bindDawnlight(settings, element<HTMLButtonElement>("dawnlight-apply"),
  element<HTMLButtonElement>("dawnlight-restore"), element<HTMLInputElement>("airOpacity"));
exportButton.onclick = async () => {
  if (!source || exporting) return;
  exporting = new AbortController();
  exportButton.disabled = true;
  exportStatus.hidden = false;
  exportStatus.textContent = "正在导出…";
  const name = world.name;
  try {
    // 点击时拍下整个视图；材质已经内嵌，播放器无需本机的文件路径。
    const { texturePath, textureSize, ...viewSettings } = settings;
    const file = await exportSnapshot({
      sourceURL: source.url, name, dimension: world.dimension ?? ".", ocean: world.scene?.ocean,
      camera: structuredClone(camera), settings: structuredClone(viewSettings),
      states: renderer.states, textures: activeTextures,
    }, exporting.signal, count => { exportStatus.textContent = `已导出 ${count} 个区域`; });
    const url = URL.createObjectURL(file);
    const link = document.createElement("a");
    link.href = url;
    link.download = name.replace(/[\\/:*?"<>|]/g, "_") + ".html";
    link.click();
    setTimeout(() => URL.revokeObjectURL(url), 60000);
  } catch (error) {
    if (!exporting.signal.aborted) hud.error(String(error));
  } finally {
    exporting = null;
    exportButton.disabled = !source;
    exportStatus.hidden = true;
  }
};

const picker = createPicker(openWorld);
element<HTMLButtonElement>("open-world").onclick = () => {
  closePopovers();
  picker({ world });
};
const select = element<HTMLSelectElement>("world-dimension");
select.onchange = async () => {
  if (!world.path) return;
  select.disabled = true;
  try {
    await openWorld(world.path, {
      dimension: select.value,
      bedrockFix: world.bedrockFix,
      structureMemoryMiB: world.structureMemoryMiB,
      structureWorkers: world.structureWorkers,
      structureRoot: world.structureRoot,
    });
  } catch (error) { select.value = world.dimension ?? ""; hud.error(String(error)); }
  finally { select.disabled = false; }
};
/** 先连上外部数据源，握手成功后才关闭当前存档并换成它；连不上时当前世界不受影响。 */
async function connectSource(url: string) {
  const connection: Connection = { url, state: "connecting", message: "" };
  let linked: R2LinkSource | undefined;
  try {
    linked = r2linkSource(url, {
      onState: (state, message) => {
        connection.state = state;
        connection.message = message || "";
        if (source === linked) showConnection(connection);
      },
      onScene: (scene) => {
        if (source !== linked) return;
        world.name = scene.name;
        showWorldInfo(world);
        showConnection(connection);
        map.refreshChunks().catch(() => {});
      },
      onCamera: (next) => {
        if (source !== linked) return;
        camera.position = [...next.position];
        if (next.yaw != null) camera.yaw = next.yaw;
        if (next.pitch != null) camera.pitch = next.pitch;
      },
    });
    const { scene, camera: start } = await linked.welcome;
    connecting = true;
    const response = await fetch("/api/world/close", { method: "POST" });
    if (!response.ok) throw Error(await response.text());
    const closed = await response.json();
    localStorage.setItem("neomega-r2-link", url);
    // 编号排在关闭得到的 Möbius 之后、下一次打开之前，晚到的旧世界切换因此被忽略。
    await useWorld({
      loaded: true,
      id: closed.id + 0.5,
      name: scene.name,
      kind: "R2Link",
      position: start?.position || [0, 80, 0],
      camera: start,
      connection,
    }, linked);
  } catch (error) {
    linked?.close();
    throw error;
  } finally {
    connecting = false;
  }
}
element<HTMLButtonElement>("connect-source").onclick = () => {
  closePopovers();
  element<HTMLInputElement>("link-url").value =
    world.connection?.url || localStorage.getItem("neomega-r2-link") || "ws://127.0.0.1:2414/r2link";
  element<HTMLOutputElement>("link-message").textContent = "";
  element<HTMLDialogElement>("link-dialog").showModal();
};
element<HTMLButtonElement>("link-start").onclick = async () => {
  const button = element<HTMLButtonElement>("link-start");
  button.disabled = true;
  element<HTMLOutputElement>("link-message").textContent = "正在连接…";
  try {
    await connectSource(element<HTMLInputElement>("link-url").value.trim());
    element<HTMLDialogElement>("link-dialog").close();
  } catch (error) {
    element<HTMLOutputElement>("link-message").textContent = error instanceof Error ? error.message : String(error);
  } finally {
    button.disabled = false;
  }
};
// 关闭存档或断开外部数据源，都回到 Möbius。
element<HTMLButtonElement>("close-world").onclick = async () => {
  const response = await fetch("/api/world/close", { method: "POST" });
  if (!response.ok) return hud.error(await response.text());
  await useWorld(await response.json());
};
/** 核心负责材质包合成，应用只传入路径与尺寸上限。 */
async function loadTextures(path: string, maxSize: number) {
  const response = await fetch("/api/textures", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ path, maxSize }),
  });
  if (!response.ok) throw Error(await response.text());
  if (!response.body) throw Error("服务端未返回材质数据");
  return readTextureStream(response.body);
}
function syncTextureControls(info: ViewerWorld) {
  const sc = info.kind === "scworld";
  element("texture-size-row").hidden = sc;
  element("texture-sc-hint").hidden = !sc;
  element<HTMLSelectElement>("texture-size").disabled = sc;
  element<HTMLButtonElement>("texture-reset").textContent = sc ? "恢复默认" : "恢复原版";
  const input = element<HTMLInputElement>("texture-path");
  input.value = sc ? info.scTexturePath ?? "" : settings.texturePath;
  input.placeholder = sc ? ".scbtex 或 PNG 方块图集路径" : "原版，或输入 zip / 目录路径";
  if (sc) element("texture-status").textContent = info.textureName ?? "生存战争材质";
}

async function applySCTexture(path: string, signal?: AbortSignal) {
  const current = world;
  for (const id of textureControls) element<HTMLInputElement>(id).disabled = true;
  element("texture-status").textContent = "正在读取生存战争图集…";
  try {
    const response = await fetch("/api/world/sc-texture", {
      method: "POST", headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ world: current.id, path }),
      signal,
    });
    if (!response.ok) throw Error((await response.text()).trim());
    await useWorld(await response.json());
  } catch (error) {
    if (world === current) element("texture-status").textContent = signal?.aborted ? (current.textureName ?? "") : String(error);
    throw error;
  } finally {
    for (const id of textureControls) element<HTMLInputElement>(id).disabled = id === "texture-size" && world.kind === "scworld";
  }
}

/** 旧场景持续显示，贴图和透明度目录准备好后一起切换。 */
async function applyTextures(path: string, signal?: AbortSignal) {
  if (world.kind === "scworld") return applySCTexture(path, signal);
  const buttons = textureControls;
  for (const id of buttons) element<HTMLButtonElement | HTMLSelectElement | HTMLInputElement>(id).disabled = true;
  element("texture-status").textContent = "正在合成贴图与透明度数据…";
  const maxSize = Number(element<HTMLSelectElement>("texture-size").value);
  try {
    const set = await loadTextures(path, maxSize);
    if (session) await session.setTextures(set);
    else renderer.setTextures(set);
    activeTextures = set;
    settings.texturePath = path;
    settings.textureSize = maxSize;
    element<HTMLInputElement>("texture-path").value = path;
    element("texture-status").textContent =
      `${path ? "材质包" : "原版"} · ${set.info.side}×${set.info.side} · ${set.info.levels} 级 mip`;
    view.refreshSettings();
  } catch (error) {
    element("texture-status").textContent = String(error);
    throw error;
  } finally {
    for (const id of buttons) element<HTMLButtonElement | HTMLSelectElement | HTMLInputElement>(id).disabled = false;
  }
}
element<HTMLInputElement>("texture-path").value = settings.texturePath;
element<HTMLSelectElement>("texture-size").value = String(settings.textureSize);
element<HTMLButtonElement>("texture-browse").onclick = () =>
  picker({
    kind: world.kind === "scworld" ? "sc-texture" : "pack",
    choose: (path, _options, _progress, signal) => applyTextures(path, signal),
    ...(world.kind === "scworld" && { world: { path: world.scTexturePath || world.path } }),
  });
element<HTMLButtonElement>("texture-apply").onclick = () =>
  applyTextures(element<HTMLInputElement>("texture-path").value).catch((error) =>
    hud.error(String(error)),
  );
element<HTMLButtonElement>("texture-reset").onclick = () =>
  applyTextures("").catch((error) => hud.error(String(error)));
element<HTMLSelectElement>("texture-size").onchange = () =>
  applyTextures(settings.texturePath).catch((error) =>
    hud.error(String(error)),
  );

function showDetails({ stats, shownTier, median, width, height }: ViewStats) {
  if (view.debugStatus.checkVisibility()) {
    view.debugStatus.textContent = `已加载 ${stats.loaded}/${stats.wanted} 个区域 · GPU 中位耗时 ${median?.toFixed(2) ?? "…"} ms`;
  }
  const clipped = source?.clippedSections ?? 0;
  element("height-diagnostics").hidden = clipped === 0;
  element("height-diagnostics").textContent = `有 ${clipped} 个子区块（subchunk）超出可显示高度（Y -64～447，按已收到的 r2region 统计）`;
  showWorldDetails([
    ["来源", world.kind === "scworld" ? "生存战争" : world.kind || "内置场景"],
    ...(world.textureName ? [["方块材质", world.textureName]] as const : []),
    ["预览渲染器", TIER_NAMES[shownTier]],
    ...(world.reusedStructure ? [["结构输出", "复用已有存档"]] as const : []),
    ["近景半径", `${settings.nearRadius} 区块（chunk）`],
    ["远景半径", `${settings.farRadius} 区块（chunk）`],
    [
      "窗口覆盖",
      `${settings.farRadius * 32}×${settings.farRadius * 32}×512 方块`,
    ],
    ["静态子区块（subchunk）", String(stats.sections)],
    ["已发布 r2region", `${stats.loaded}/${stats.wanted}`],
    ["待更新 / 失败 r2region", `${stats.pending} / ${stats.failed}`],
    ["特殊对象", String(stats.objects)],
    ...(stats.unknownBlocks ? [["未知方块", String(stats.unknownBlocks)]] as const : []),
    ["累计传输", `${(stats.bytes / 1024 ** 2).toFixed(1)} MiB`],
    ["r2region 显存", `${(stats.usedBytes / 1024 ** 2).toFixed(1)} MiB`],
    ["GPU 纹理", `${(renderer.textureBytes / 1024 ** 2).toFixed(1)} MiB`],
    ["GPU 中位数", `${median?.toFixed(2) || "…"} ms`],
    ["实际像素", `${width}×${height}`],
  ]);
}

function frame(now: number) {
  requestAnimationFrame(frame);
  view.frame(now, session, { continuous });
}

if (!initialWorld.textureName && (settings.texturePath || settings.textureSize !== 128)) {
  await startupStage("正在加载自定义材质…");
  await applyTextures(settings.texturePath).catch((error) =>
    hud.error(String(error)),
  );
}
await startupStage("正在读取场景信息…");
// Electron 页面使用固定来源，WebSocket 直连宿主本次启动的回环端口。
const localServiceURL = location.protocol === "r2:"
  ? await (await fetch("/desktop/service-url")).json()
  : location.href;
await useWorld(initialWorld);
const reconnect = sessionStorage.getItem(reconnectKey);
if (reconnect && !initialWorld.textureName) {
  sessionStorage.removeItem(reconnectKey);
  await connectSource(reconnect).catch(error => hud.error(String(error)));
}
await startupStage("正在加载场景，准备第一帧…");
window.addEventListener("pagehide", () => {
  view.dispose();
  session?.dispose();
  renderer.dispose();
});
// 验收入口仍使用实际会话、流式调度和绘制路径，不替换成测试场景。
const viewerR2 = {
  get state() {
    return {
      world,
      settings: { ...settings },
      camera: { ...camera, position: [...camera.position] },
      map: {
        open: map.open,
        camera: map.camera,
        center: [...map.center],
        overview: map.isOverview,
        selection: map.selection,
        pending: !!map.pending,
        tiles: map.tiles.length,
      },
      stream: session?.stats,
      gpuTimes: renderer.times,
      frames: [...frames],
      error: renderer.gl?.getError() ?? 0,
    };
  },
  /** 固定相机朝向，供像素回归与截图使用。 */
  setCamera(position: number[], target: number[]) {
    camera.position = [...position];
    const d = target.map((v, i) => v - position[i]);
    camera.yaw = Math.atan2(d[0], -d[2]);
    camera.pitch = Math.asin(d[1] / Math.hypot(...d));
    renderer.resetTiming();
  },
  set continuous(value: boolean) {
    continuous = value;
  },
  openWorld,
  connectSource,
  applyTextures,
  close: () => element<HTMLButtonElement>("close-world").click(),
  get renderer() {
    return renderer;
  },
  get session() {
    return session;
  },
  get source() {
    return source;
  },
  get map() {
    return map;
  },
};
Object.assign(globalThis, { viewerR2 });
requestAnimationFrame(frame);
