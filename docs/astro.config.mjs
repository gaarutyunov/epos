// @ts-check
import { defineConfig } from "astro/config";

// The site is served from a project page (https://<user>.github.io/epos) and,
// for pull requests, from a preview directory beneath it
// (…/epos/pr-preview/pr-<N>). BASE_PATH lets the preview workflow point every
// asset at that subdirectory; without it a preview would load the main
// deployment's assets and silently show stale content.
const base = process.env.BASE_PATH ?? "/epos";

export default defineConfig({
  site: "https://gaarutyunov.github.io",
  base,
  trailingSlash: "ignore",
  // <ga-*> are custom elements; Astro must not try to resolve them as
  // components.
  vite: {
    build: {
      // The vendored ui-kit is plain ES modules and CSS with no dependencies.
      target: "es2022",
    },
  },
});
