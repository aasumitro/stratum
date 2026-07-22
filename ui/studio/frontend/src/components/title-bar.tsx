import React from "react"

const isMac = typeof navigator !== "undefined" && /^Mac/.test(navigator.platform)

export function TitleBar() {
  if (!isMac) return null

  return (
    <div
      className="flex items-center justify-center flex-shrink-0 h-[50px] select-none"
      style={{ WebkitAppRegion: "drag" } as React.CSSProperties}
    >
      <span className="text-xs font-medium text-muted-foreground tracking-wide">
        Stratum Studio
      </span>
    </div>
  )
}
