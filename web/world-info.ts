import { element } from "/r2/common_ui/hud.js";

import { dimensionName, type Connection, type ViewerWorld } from "./types.js";

export function showWorldInfo(world: ViewerWorld) {
  element("world-label").textContent = world.loaded
    ? world.name
    : "未打开存档";
  element("info-name").textContent = world.loaded
    ? world.name
    : "未打开存档";
  element("info-path").textContent =
    world.path || world.connection?.url || "内置海晶灯莫比乌斯环；从文件夹按钮打开存档或结构文件。";
  const mods = element("info-scmods");
  mods.hidden = !world.scModNames?.length;
  mods.textContent = mods.hidden ? "" : `模组：${world.scModNames?.join("、")}（${world.scModBlocks} 种方块）`;
  element<HTMLButtonElement>("close-world").disabled = !world.loaded;
  const dimension = element<HTMLSelectElement>("world-dimension");
  dimension.replaceChildren(...(world.dimensions || []).map((value) => {
    const option = document.createElement("option");
    option.value = value;
    option.textContent = dimensionName(value);
    return option;
  }));
  dimension.value = world.dimension || "";
  element("world-dimension-row").hidden = !world.path || world.kind === "scworld";
  element("height-diagnostics").hidden = true;
}

export function showConnection(connection?: Connection) {
  const dot = element("link-state"), close = element<HTMLButtonElement>("close-world");
  dot.hidden = !connection;
  if (connection) {
    const status = { connecting: "正在连接", connected: "已连接", reconnecting: "正在重连", closed: "已断开" }[connection.state] || "已断开";
    dot.dataset.state = connection.state;
    dot.title = `${status} · ${connection.url}${connection.message ? "\n" + connection.message : ""}`;
    dot.setAttribute("aria-label", dot.title);
  }
  close.title = connection ? "断开连接，返回 Möbius" : "关闭存档";
  close.setAttribute("aria-label", connection ? "断开连接" : "关闭存档");
}

/** 场景统计使用配对的标签和值，长路径留在单独的段落。 */
export function showWorldDetails(rows: ReadonlyArray<readonly [string, string]>) {
  element("details").replaceChildren(
    ...rows.map(([name, value]) => {
      const row = document.createElement("div"),
        dt = document.createElement("dt"),
        dd = document.createElement("dd");
      dt.textContent = name;
      dd.textContent = value;
      row.append(dt, dd);
      return row;
    }),
  );
}
