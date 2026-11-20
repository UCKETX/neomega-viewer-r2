import type { Appearance } from "/r2/common_ui/appearance.js";
import type { ViewSettings } from "/r2/common_ui/view.js";

const profile = {
  // Keep the time slider and automatic daylight cycle usable.
  manualSun: false,
  directColor: "#ffddb2",
  autoSunColor: false,
  directStrength: 1.25,
  skyColor: "#dae8ff",
  skyStrength: 0.95,
  autoExposure: false,
  exposureEV: 0.3,
  airOpacity: 16,
  solarHaze: true,
  cloudShadows: true,
} satisfies Partial<Appearance>;

type Saved = { tier: ViewSettings["tier"]; appearance: Pick<Appearance, keyof typeof profile> };
const keys = Object.keys(profile) as (keyof typeof profile)[];
const key = "neomega-r2-before-dawnlight";

export function bindDawnlight(
  settings: ViewSettings,
  apply: HTMLButtonElement,
  restore: HTMLButtonElement,
  airControl: HTMLInputElement,
) {
  // createView has installed the shared appearance object. Mutate it in
  // place so photograph, offline export and the live frame all see it.
  const appearance = settings.appearance;
  if (!appearance) return;
  let previous: Saved | null = null;
  try {
    const saved = JSON.parse(localStorage.getItem(key) ?? "null") as Saved | null;
    if (saved && ["voxel", "basic", "shading"].includes(saved.tier) &&
        saved.appearance && keys.every(k => typeof saved.appearance[k] === typeof profile[k]))
      previous = saved;
  } catch {}
  function update() { restore.disabled = previous === null; }
  function syncAppearance() {
    // Reuse the shared appearance panel's own refresh handler, including
    // resetting samples, syncing every slider and saving the view settings.
    airControl.value = String(appearance!.airOpacity);
    airControl.dispatchEvent(new Event("input", { bubbles: true }));
    update();
  }
  apply.onclick = () => {
    if (!previous) {
      const before = Object.fromEntries(keys.map(k => [k, appearance[k]])) as Saved["appearance"];
      previous = { tier: settings.tier, appearance: before };
      localStorage.setItem(key, JSON.stringify(previous));
    }
    Object.assign(appearance, profile);
    settings.tier = "shading";
    syncAppearance();
  };
  restore.onclick = () => {
    if (!previous) return;
    settings.tier = previous.tier;
    Object.assign(appearance, previous.appearance);
    previous = null;
    localStorage.removeItem(key);
    syncAppearance();
  };
  update();
}
