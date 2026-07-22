import { useState, useEffect } from "react"
import { useTranslation } from "react-i18next"
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog"

function Keys({ keys }: { keys: string[] }) {
  return (
    <span className="flex items-center gap-1">
      {keys.map((k) => (
        <kbd
          key={k}
          className="inline-flex h-6 items-center rounded border bg-muted px-1.5 font-mono text-xs text-muted-foreground"
        >
          {k}
        </kbd>
      ))}
    </span>
  )
}

export function KeyboardShortcuts() {
  const { t } = useTranslation()
  const [open, setOpen] = useState(false)

  const SHORTCUTS = [
    { keys: ["?"], label: t("shortcuts.show") },
    { keys: ["⌘", "K"], label: t("shortcuts.commandPalette") },
    { keys: ["⌘", "O"], label: t("shortcuts.switchOrganization") },
    { keys: ["/"], label: t("shortcuts.focusSearch") },
    { keys: ["g", "m"], label: t("shortcuts.goToMembers") },
    { keys: ["g", "b"], label: t("shortcuts.goToBilling") },
    { keys: ["g", "s"], label: t("shortcuts.goToSettings") },
  ]

  useEffect(() => {
    function onKeyDown(e: KeyboardEvent) {
      const tag = (e.target as HTMLElement).tagName
      if (
        tag === "INPUT" ||
        tag === "TEXTAREA" ||
        (e.target as HTMLElement).isContentEditable
      )
        return

      if (e.key === "?" && !e.metaKey && !e.ctrlKey) {
        e.preventDefault()
        setOpen((v) => !v)
        return
      }
      if (e.key === "k" && (e.metaKey || e.ctrlKey)) {
        e.preventDefault()
        setOpen((v) => !v)
      }
    }
    window.addEventListener("keydown", onKeyDown)
    return () => window.removeEventListener("keydown", onKeyDown)
  }, [])

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <DialogContent className="max-w-sm">
        <DialogHeader>
          <DialogTitle>{t("shortcuts.title")}</DialogTitle>
        </DialogHeader>
        <ul className="flex flex-col gap-3">
          {SHORTCUTS.map((s) => (
            <li
              key={s.label}
              className="flex items-center justify-between text-sm"
            >
              <span className="text-muted-foreground">{s.label}</span>
              <Keys keys={s.keys} />
            </li>
          ))}
        </ul>
      </DialogContent>
    </Dialog>
  )
}
