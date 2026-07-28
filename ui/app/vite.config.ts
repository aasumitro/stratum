import path from "path"
import { EventEmitter } from "events"

// Suppress MaxListenersExceededWarning in Vite dev server
// (e.g. from TanStack Router + Tailwind plugins adding close listeners)
EventEmitter.defaultMaxListeners = 20

import { tanstackRouter } from "@tanstack/router-plugin/vite"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig, loadEnv, type Plugin } from "vite"

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
    ],
    resolve: {
      alias: {
        "@": path.resolve(__dirname, "./src"),
      },
    },
    server: { port: 3000 },
    build: { sourcemap: false },
  }
})
