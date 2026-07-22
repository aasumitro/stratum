import { useEffect, useState } from "react"
import { RouterProvider } from "@tanstack/react-router"
import { Toaster } from "sonner"
import { TitleBar } from "./components/title-bar"
import { CommandPalette } from "./components/command-palette"
import { router } from "./router"

export default function App() {
  const [paletteOpen, setPaletteOpen] = useState(false)

  useEffect(() => {
    const handler = (e: KeyboardEvent) => {
      if ((e.metaKey || e.ctrlKey) && e.key === "k") {
        e.preventDefault()
        setPaletteOpen((v) => !v)
      }
    }
    document.addEventListener("keydown", handler)
    return () => document.removeEventListener("keydown", handler)
  }, [])

  return (
    <div className="flex flex-col h-screen overflow-hidden">
      <TitleBar />
      <div className="flex flex-col flex-1 min-h-0">
        <RouterProvider router={router} />
      </div>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} />
      <Toaster richColors position="bottom-right" />
    </div>
  )
}
