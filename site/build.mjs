// Zero-dependency static-site build: copies public/ to dist/ with a relative
// base path so assets resolve under GitHub Pages project/PR-preview subpaths
// (SPEC §13.2 base-path note).
import { cp, mkdir, rm } from "node:fs/promises";

await rm("dist", { recursive: true, force: true });
await mkdir("dist", { recursive: true });
await cp("public", "dist", { recursive: true });
console.log("built site -> dist (relative base path ./)");
