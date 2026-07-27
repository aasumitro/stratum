import path from "path"
import { EventEmitter } from "events"

// Suppress MaxListenersExceededWarning in Vite dev server
// (e.g. from TanStack Router + Tailwind plugins adding close listeners)
EventEmitter.defaultMaxListeners = 20

import { tanstackRouter } from "@tanstack/router-plugin/vite"
import tailwindcss from "@tailwindcss/vite"
import react from "@vitejs/plugin-react"
import { defineConfig } from "vite"

export default defineConfig({
  plugins: [
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
})
