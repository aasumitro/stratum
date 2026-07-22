import { useEffect, useRef } from "react"
import { IconSearch } from "@tabler/icons-react"
import { Input } from "@/components/ui/input"
import { cn } from "@/lib/ui"

interface SearchBarProps {
  value: string
  onChange: (value: string) => void
  placeholder?: string
  className?: string
  /** register as the target for the global `/` focus shortcut */
  autoFocusShortcut?: boolean
}

export function SearchBar({
  value,
  onChange,
  placeholder,
  className,
  autoFocusShortcut = true,
}: SearchBarProps) {
  const ref = useRef<HTMLInputElement>(null)

  useEffect(() => {
    if (!autoFocusShortcut) return
    function onKeydown(e: KeyboardEvent) {
      if (e.key !== "/" || e.metaKey || e.ctrlKey) return
      const target = e.target as HTMLElement
      if (["INPUT", "TEXTAREA"].includes(target.tagName)) return
      e.preventDefault()
      ref.current?.focus()
    }
    document.addEventListener("keydown", onKeydown)
    return () => document.removeEventListener("keydown", onKeydown)
  }, [autoFocusShortcut])

  return (
    <div className={cn("relative", className)}>
      <IconSearch className="pointer-events-none absolute top-1/2 left-2.5 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        ref={ref}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        placeholder={placeholder}
        className="pl-8"
      />
    </div>
  )
}
