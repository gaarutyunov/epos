// @ts-check
import { defineConfig } from "astro/config";

// The site is served from the root of its own subdomain
// (https://epos.garutyunov.com/) and, for pull requests, from a preview
// directory beneath it (…/pr-preview/pr-<N>). BASE_PATH lets the preview
// workflow point every asset at that subdirectory; without it a preview would
// load the main deployment's assets and silently show stale content.
const base = process.env.BASE_PATH ?? "/";

export default defineConfig({
  site: "https://epos.garutyunov.com",
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
