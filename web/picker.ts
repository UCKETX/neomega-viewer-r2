// 存档与材质包共用服务端目录浏览器；Electron 也可提交用户拖入的本机路径。
import { dimensionName, type OpenWorld, type Progress, type ViewerWorld } from "./types.js";

type Kind = "world" | "pack" | "sc-texture";
interface Entry { name: string; path: string; kind: string; directory: boolean; openable: boolean }
interface Listing { path: string; parent: string; selected: string; kind: string; roots: Entry[]; entries: Entry[] }
interface PickerOptions { kind?: Kind; choose?: OpenWorld; world?: Partial<ViewerWorld> }

declare global {
  interface Window { desktopFiles?: { pathForFile(file: File): string } }
}

/** 文件选择与打开分开，让用户在转换前选定维度、修复和预算。 */
export function createPicker(openWorld: OpenWorld) {
  const dialog = document.querySelector<HTMLDialogElement>("#files")!,
    input = document.querySelector<HTMLInputElement>("#directory")!,
    search = document.querySelector<HTMLInputElement>("#file-search")!,
    mods = document.querySelector<HTMLTextAreaElement>("#scmod-paths")!,
    modRow = document.querySelector<HTMLElement>("#scmod-row")!,
    list = document.querySelector<HTMLElement>("#file-list")!,
    message = document.querySelector<HTMLOutputElement>("#file-message")!,
    dimension = document.querySelector<HTMLSelectElement>("#source-dimension")!;
  let listing: Listing, selected = "", kind: Kind = "world", onChoose = openWorld;
  let currentWorld: Partial<ViewerWorld> = {};
  let opening: AbortController | null = null, browsing: AbortController | null = null;
  let dropping = false;
  const dropZone = document.querySelector<HTMLElement>("#file-drop")!;
  const desktopFiles = window.desktopFiles;
  const destination = document.querySelector<HTMLOutputElement>("#structure-root")!;
  const resetRoot = document.querySelector<HTMLButtonElement>("#reset-structure-root")!;
  const rootKey = "neomega-r2-structure-root";
  function showRoot(path: string) {
    destination.value = path || "源文件旁";
    resetRoot.disabled = !path;
  }
  function setRoot(path: string) {
    if (path) localStorage.setItem(rootKey, path);
    else localStorage.removeItem(rootKey);
    showRoot(path);
    message.textContent = path ? "转换输出目录：" + path : "转换结果将放在源文件旁。";
  }
  resetRoot.onclick = () => setRoot("");
  dropZone.hidden = typeof desktopFiles?.pathForFile !== "function";
  const paths: Record<Kind, string> = { world: "", pack: "", "sc-texture": "" };
  const labels: Record<string, string> = {
    directory: "Java 存档", bedrock: "Bedrock 存档", folder: "目录",
    pack: "Java PackFS", texture: "材质包", level: "存档入口", "sc-texture": "生存战争方块图集",
    schem: "Java 结构 · schem", schematic: "Java 结构 · schematic",
    litematic: "Java 结构 · litematic", nbt: "Java 结构 · nbt",
    mcstructure: "Bedrock 结构", mcworld: "Bedrock 世界包", scworld: "生存战争地图", scmod: "生存战争模组 · 添加",
  };
  // 列表图标只区分三类：可直接打开的存档、需要转换的结构文件、普通目录和其他文件。
  const icons: Record<string, string> = {
    directory: "i-world", bedrock: "i-world", level: "i-world", mcworld: "i-world", scworld: "i-world", pack: "i-world",
    schem: "i-structure", schematic: "i-structure", litematic: "i-structure", nbt: "i-structure", mcstructure: "i-structure",
    folder: "i-folder", texture: "i-image", "sc-texture": "i-image",
  };
  function icon(name: string) {
    const svg = document.createElementNS("http://www.w3.org/2000/svg", "svg"),
      use = document.createElementNS("http://www.w3.org/2000/svg", "use");
    svg.setAttribute("aria-hidden", "true");
    use.setAttribute("href", "#" + name);
    svg.append(use);
    return svg;
  }
  /** Bedrock 固定三个维度，Java 首次打开主世界，再使用服务端扫描的列表。 */
  function setDimensions(values: string[] = [""]) {
    dimension.replaceChildren(...values.map((value) => {
      const option = document.createElement("option");
      option.value = value;
      option.textContent = dimensionName(value);
      return option;
    }));
  }
  const isMod = (path: string) => /\.scmod["\s]*$/i.test(path);
  const isSCWorld = (path: string) => /\.scworld["\s]*$/i.test(path);
  function addMod(path: string) {
    const choices = mods.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean);
    if (!choices.includes(path)) mods.value = [...choices, path].join("\n");
    modRow.hidden = false;
    message.textContent = "已添加模组；随 .scworld 一同打开。";
  }
  function setSourceDimensions(sourceKind: string, path?: string) {
    modRow.hidden = kind !== "world" || !(sourceKind === "scworld" || currentWorld.kind === "scworld" || !!mods.value);
    dimension.disabled = kind === "world" && sourceKind === "scworld";
    setDimensions(sourceKind === "scworld" ? ["."]
      : ["bedrock", "mcstructure", "mcworld"].includes(sourceKind) ? ["0", "1", "2"]
      : path === currentWorld.path ? currentWorld.dimensions ?? [""] : [""]);
    if (currentWorld.dimension && path === currentWorld.path) dimension.value = currentWorld.dimension;
  }
  // 只匹配当前列表中的名称，避免类型标签也被当成文件名命中。
  search.oninput = () => {
    const query = search.value.trim().toLowerCase();
    let matches = 0;
    for (const row of list.querySelectorAll<HTMLElement>(".file-row")) {
      row.hidden = !row.dataset.name!.includes(query);
      if (!row.hidden) matches++;
    }
    message.textContent = query ? `匹配 ${matches} / ${list.childElementCount} 个项目` : `${matches} 个项目`;
  };
  /** 浏览错误留在选择器中，用户可以修改路径继续。 */
  async function browse(path = "") {
    if (opening) return false;
    browsing?.abort();
    const controller = new AbortController();
    browsing = controller;
    message.textContent = "正在读取目录…";
    search.disabled = true;
    input.disabled = true;
    document.querySelector<HTMLButtonElement>("#open-directory")!.disabled = true;
    document.querySelector<HTMLButtonElement>("#parent")!.disabled = true;
    try {
      const response = await fetch("/api/files?kind=" + kind + "&path=" + encodeURIComponent(path), { signal: controller.signal });
      if (!response.ok) throw Error(await response.text());
      listing = await response.json();
      selected = listing.selected;
      input.value = selected || listing.path;
      setSourceDimensions(listing.kind, input.value);
      search.value = "";
      list.replaceChildren();
      document.querySelector<HTMLElement>("#roots")!.replaceChildren();
      for (const root of listing.roots) {
        const button = document.createElement("button");
        button.className = "chip";
        button.textContent = root.name;
        button.onclick = () => browse(root.path);
        document.querySelector<HTMLElement>("#roots")!.append(button);
      }
      for (const entry of listing.entries) {
        const item = document.createElement("div");
        item.className = "file-row";
        item.dataset.name = entry.name.toLowerCase();
        const row = document.createElement("button");
        row.className = "file";
        row.setAttribute("aria-selected", String(entry.path === selected));
        const name = document.createElement("span"), label = document.createElement("span");
        name.textContent = entry.name;
        label.className = "kind";
        label.textContent = labels[entry.kind] || entry.kind;
        name.className = "name";
        row.append(icon(icons[entry.kind] ?? "i-file"), name, label);
        row.onclick = () => {
          if (kind === "world" && entry.kind === "scmod") return addMod(entry.path);
          if (!entry.openable) return browse(entry.path);
          selected = input.value = entry.path;
          for (const other of list.querySelectorAll(".file")) other.setAttribute("aria-selected", String(other === row));
          const sourceKind = entry.kind === "level" ? listing.kind : entry.kind;
          setSourceDimensions(sourceKind, entry.path);
          message.textContent = "已选择 " + entry.name;
        };
        row.ondblclick = () => entry.directory ? browse(entry.path) : choose(entry.path);
        item.append(row);
        if (kind === "world" && entry.directory) {
          const use = document.createElement("button");
          use.className = "use-output-dir icon-button";
          use.title = "设为转换输出目录";
          use.setAttribute("aria-label", `把 ${entry.name} 设为转换输出目录`);
          use.append(icon("i-outdir"));
          use.onclick = () => setRoot(entry.path);
          item.append(use);
        }
        list.append(item);
      }
      message.textContent = listing.entries.length + " 个项目";
      return true;
    } catch (error) {
      if (!(error instanceof Error && error.name === "AbortError")) message.textContent = String(error);
      return false;
    }
    finally {
      if (browsing === controller) {
        browsing = null;
        search.disabled = false;
        input.disabled = false;
        document.querySelector<HTMLButtonElement>("#open-directory")!.disabled = false;
        document.querySelector<HTMLButtonElement>("#parent")!.disabled = !listing;
      }
    }
  }
  /** 进度来自转换器的阶段，不能把读取字节进度当作整个转换的完成比例。 */
  function progress(value: Progress) {
    const stages: Record<string, string> = {reading:"读取结构", writing:"写出区块", extracting:"解包世界", finishing:"发布存档", complete:"转换完成"};
    message.textContent = `${stages[value.stage] || value.stage} ${value.total ? Math.round(value.completed / value.total * 100) + "%" : value.completed || ""}`;
  }
  /** 保持对话框直到来源准备完成，关闭对话框会取消仍在进行的转换。 */
  async function choose(path: string) {
    if (opening) return;
    if (kind === "world" && isMod(path)) { addMod(path); input.value = currentWorld.path ?? listing?.path ?? ""; return; }
    browsing?.abort();
    browsing = null;
    message.textContent = kind !== "world" ? "正在读取材质…" : "正在打开来源…";
    opening = new AbortController();
    search.disabled = true;
    document.querySelector<HTMLButtonElement>("#open-directory")!.disabled = true;
    try {
      if (kind !== "world") await onChoose(path, undefined, undefined, opening.signal);
      else await onChoose(path, {
        dimension: isSCWorld(path) ? "" : dimension.value,
        survivalcraftMods: isSCWorld(path) ? mods.value.split(/\r?\n/).map(s => s.trim()).filter(Boolean) : undefined,
        structureRoot: localStorage.getItem(rootKey) ?? "",
        bedrockFix: document.querySelector<HTMLInputElement>("#source-fix")!.checked,
        structureMemoryMiB: Number(document.querySelector<HTMLInputElement>("#source-memory")!.value),
        structureWorkers: Number(document.querySelector<HTMLInputElement>("#source-workers")!.value),
      }, progress, opening.signal);
      paths[kind] = path;
      opening = null;
      dialog.close();
    } catch (error) {
      if (!(error instanceof Error && error.name === "AbortError")) message.textContent = String(error);
    } finally {
      search.disabled = false;
      opening = null;
      document.querySelector<HTMLButtonElement>("#open-directory")!.disabled = false;
    }
  }
  dialog.addEventListener("close", () => {
    opening?.abort();
    browsing?.abort();
    dropZone.classList.remove("drag-over");
  });
  dropZone.addEventListener("dragover", (event) => {
    event.preventDefault();
    if (event.dataTransfer) event.dataTransfer.dropEffect = opening || dropping ? "none" : "copy";
    if (!opening && !dropping) dropZone.classList.add("drag-over");
  });
  dropZone.addEventListener("dragleave", () => dropZone.classList.remove("drag-over"));
  dropZone.addEventListener("drop", async (event) => {
    event.preventDefault();
    dropZone.classList.remove("drag-over");
    if (opening || dropping || !desktopFiles || !event.dataTransfer) return;
    const files = [...event.dataTransfer.files];
    if (files.length !== 1) {
      message.textContent = "请一次拖入一个目录或文件。";
      return;
    }
    dropping = true;
    dropZone.setAttribute("aria-busy", "true");
    try {
      const path = desktopFiles.pathForFile(files[0]);
      if (!path) throw Error("无法读取路径，请从文件资源管理器拖入本地目录或文件。");
      const chosenDimension = dimension.value;
      // 先由 Go 识别来源种类、规范化路径，再走原有打开、转换与取消流程。
      if (!await browse(path) || !dialog.open) return;
      if ([...dimension.options].some((option) => option.value === chosenDimension)) dimension.value = chosenDimension;
      await choose(input.value);
    } catch (error) { message.textContent = String(error); }
    finally {
      dropping = false;
      dropZone.setAttribute("aria-busy", "false");
    }
  });
  document.querySelector<HTMLButtonElement>("#browse")!.onclick = () => browse(input.value);
  input.addEventListener("input", () => { setSourceDimensions(""); });
  input.addEventListener("keydown", (event) => {
    if (event.key === "Enter") { event.preventDefault(); browse(input.value); }
  });
  document.querySelector<HTMLButtonElement>("#parent")!.onclick = () => browse(listing.parent);
  document.querySelector<HTMLButtonElement>("#open-directory")!.onclick = () => choose(input.value);
  return ({ kind: nextKind = "world", choose: callback = openWorld, world = {} }: PickerOptions = {}) => {
    kind = nextKind;
    currentWorld = world;
    mods.value = world.survivalcraftMods?.join("\n") ?? "";
    showRoot(localStorage.getItem(rootKey) ?? "");
    onChoose = callback;
    setSourceDimensions(world.kind || "", world.path);
    dimension.value = world.dimension || "";
    document.querySelector<HTMLInputElement>("#source-fix")!.checked = world.bedrockFix ?? true;
    document.querySelector<HTMLInputElement>("#source-memory")!.value = String(world.structureMemoryMiB || 512);
    document.querySelector<HTMLInputElement>("#source-workers")!.value = String(world.structureWorkers || 0);
    document.querySelector<HTMLElement>("#source-options")!.hidden = kind !== "world";
    document.querySelector<HTMLElement>("#file-drop-title")!.textContent = kind === "sc-texture"
      ? "将生存战争 .scbtex 或 PNG 图集拖到这里"
      : kind === "pack" ? "将材质包 ZIP 或目录拖到这里使用" : "将存档目录或文件拖到这里打开";
    dialog.querySelector("h2")!.textContent = kind === "sc-texture" ? "选择生存战争材质"
      : kind === "pack" ? "选择材质包" : "打开存档或结构";
    document.querySelector<HTMLButtonElement>("#open-directory")!.textContent = kind !== "world" ? "使用材质包" : "打开所选来源";
    document.querySelector<HTMLElement>("#source-hint")!.textContent = kind === "sc-texture"
      ? "选择 .scbtex 或 PNG。图集为 16×16 格，支持 128、256、512 或 1024 像素的正方形。"
      : kind === "pack" ? "选择 zip 或包含 assets 的目录，也可直接输入路径。"
      : "选中后点「打开所选来源」；双击存档目录可浏览内部。支持生存战争 .scworld；可点击列表里的 .scmod 作为预览资源。目录右侧的图标把它设为结构转换的输出目录。";
    dialog.showModal();
    browse(world.path || paths[kind]);
  };
}
