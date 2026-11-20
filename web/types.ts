import type { R2LinkCamera, R2LinkState } from "/r2/index.js";

export interface OpenOptions {
  dimension?: string;
  bedrockFix?: boolean;
  structureMemoryMiB?: number;
  structureWorkers?: number;
  structureRoot?: string;
  survivalcraftMods?: string[];
}

export interface Connection {
  url: string;
  state: R2LinkState;
  message: string;
}

export interface ViewerWorld extends OpenOptions {
  id: number;
  loaded: boolean;
  name: string;
  position: number[];
  kind?: string;
  textureName?: string;
  scTexturePath?: string;
  scTextureUpdate?: boolean;
  scModNames?: string[];
  scModBlocks?: number;
  path?: string;
  dimensions?: string[];
  scene?: { ocean: number; camera: R2LinkCamera; time: number };
  camera?: R2LinkCamera;
  connection?: Connection;
  reusedStructure?: boolean;
}

export interface Progress {
  stage: string;
  completed: number;
  total: number;
}

export type OpenWorld = (
  path: string,
  options?: OpenOptions,
  progress?: (value: Progress) => void,
  signal?: AbortSignal,
) => Promise<void>;

const dimensions: Record<string, string> = {
  "": "主世界（打开后可切换维度）", ".": "主世界", "DIM-1": "下界", DIM1: "末地",
  "0": "主世界", "1": "下界", "2": "末地",
};
export function dimensionName(value: string): string {
  return dimensions[value] ?? value;
}
