// <reference types="vitest/config" />
import path from "path"
import { EventEmitter } from "events"

// Suppress MaxListenersExceededWarning in Vite dev server
// (e.g. from TanStack Router + Tailwind plugins adding close listeners)
EventEmitter.defaultMaxListeners = 20

import { tanstackRouter } from "@tanstack/router-plugin/vite"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, loadEnv, type Plugin } from "vite"
import { visualizer } from "rollup-plugin-visualizer"

// meta http-equiv CSP can't carry frame-ancestors/report-uri (browsers ignore
// them there) — clickjacking protection still needs an X-Frame-Options /
// frame-ancestors header from whatever serves the built static files.
function cspPlugin(env: Record<string, string>): Plugin {
  const origin = (url?: string) => {
    try {
      return url ? new URL(url).origin : ""
    } catch {
      return ""
    }
  }
  const connectSrc = [
    "'self'",
    origin(env.VITE_SERVER_URL),
    origin(env.VITE_SUPABASE_URL),
    "https://app.posthog.com",
  ]
    .filter(Boolean)
    .join(" ")

  const csp = [
    "default-src 'self'",
    "script-src 'self' https://app.posthog.com",
    "style-src 'self' 'unsafe-inline'",
    "img-src 'self' data:",
    "font-src 'self' data:",
    `connect-src ${connectSrc}`,
    "base-uri 'self'",
    "object-src 'none'",
    "form-action 'self'",
  ].join("; ")

  return {
    name: "inject-csp",
    // dev server injects an inline React-refresh preamble script that a
    // strict script-src blocks (HMR breaks, app renders blank); the CSP
    // only matters for what actually ships, so build-only is correct, not a compromise.
    apply: "build",
    transformIndexHtml(html) {
      return html.replace(
        "<head>",
        `<head>\n    <meta http-equiv="Content-Security-Policy" content="${csp}" />`
      )
    },
  }
}

// Groups low-churn library code (changes only on a dependency bump, not on
// every app deploy) into its own cacheable chunks, instead of it all living
// in the main entry chunk alongside app code that changes constantly. Chunk
// boundaries were chosen from the actual `stats.html` treemap (`npm run
// build`), not guessed from package.json — each one is a real >10kB slice
// of what was previously inside index-*.js.
function vendorChunk(id: string): string | undefined {
  const marker = "/node_modules/"
  const idx = id.lastIndexOf(marker)
  if (idx === -1) return undefined
  const rest = id.slice(idx + marker.length)
  const pkg = rest.startsWith("@")
    ? rest.split("/").slice(0, 2).join("/")
    : rest.split("/")[0]

  if (pkg === "react" || pkg === "react-dom" || pkg === "scheduler")
    return "vendor-react"
  // iceberg-js is a transitive dependency of @supabase/storage-js.
  if (pkg.startsWith("@supabase/") || pkg === "iceberg-js")
    return "vendor-supabase"
  if (pkg === "axios") return "vendor-axios"
  if (pkg === "i18next" || pkg === "react-i18next") return "vendor-i18n"
  // @tanstack/* and @base-ui/react are deliberately NOT grouped wholesale:
  // most of each already ships in per-route/per-component chunks that only
  // load when that route/dialog is actually visited (measured via
  // stats.html — e.g. base-ui's Toolbar/Composite internals, tanstack-
  // form's useForm). Force-merging the package into one chunk would pull
  // that lazy-only code into the always-eager vendor bundle, growing
  // first-load size for a cacheability win nobody visiting most pages gets.
  return undefined
}

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "VITE_")

  return {
    plugins: [
      cspPlugin(env),
      tanstackRouter({
        target: "react",
        autoCodeSplitting: true,
        generatedRouteTree: "./src/router.gen.ts",
      }),
      react(),
      tailwindcss(),
      visualizer({
        filename: "stats.html",
        open: true,
        gzipSize: true,
        brotliSize: true,
      }),
    ],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: { port: 3000 },
    build: {
      sourcemap: false,
      rollupOptions: {
        output: { manualChunks: vendorChunk },
      },
    },
    // Pure-logic tests only (no component rendering) — plain node environment,
    // no jsdom/@testing-library needed until something actually renders.
    test: {
      environment: "node",
      include: ["src/**/*.test.ts"],
    },
  }
})
