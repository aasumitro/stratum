import React from "react"

const isMac =
  typeof navigator !== "undefined" && /^Mac/.test(navigator.platform)

export function TitleBar() {
  if (!isMac) return null

  return (
    <div
      className="flex h-12.5 shrink-0 items-center justify-center select-none"
      style={{ WebkitAppRegion: "drag" } as React.CSSProperties}
    >
      <span className="text-xs font-medium tracking-wide text-muted-foreground">
        Stratum Studio
      </span>
    </div>
  )
}
