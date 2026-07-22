import { useEffect, useRef } from "react"
import { useNavigate } from "@tanstack/react-router"
import { useActiveOrganization } from "@/hooks/use-active-organization"

const CHORD_WINDOW_MS = 900

type ChordMap = Record<string, string>

// "g m" go-to-Members style chords. Only the
// second key matters for dispatch; the map below is context-sensitive
// (organization nav vs. personal nav) since only one is ever visible at a
// time, so key reuse across the two maps (e.g. "a" = audit log in both) is
// intentional, not a collision.
function orgChords(base: string): ChordMap {
  return {
    o: base,
    m: `${base}/members`,
    b: `${base}/billing`,
    f: `${base}/files`,
    w: `${base}/webhooks`,
    a: `${base}/audit-log`,
    s: `${base}/settings`,
  }
}

const PERSONAL_CHORDS: ChordMap = {
  p: "/account?tab=profile",
  e: "/account?tab=preferences",
  s: "/account?tab=security",
  n: "/notifications",
  a: "/account?tab=auditLog",
}

// "g" then a second key within CHORD_WINDOW_MS navigates. Ignored while
// typing in an input/textarea/contenteditable, or while a dialog/sheet has
// focus trapped (checked via the same tag-based guard the rest of this
// app's keyboard shortcuts use).
export function useNavigationChords() {
  const navigate = useNavigate()
  const { organizationId } = useActiveOrganization()
  const pendingRef = useRef(false)
  const timerRef = useRef<ReturnType<typeof setTimeout> | undefined>(undefined)

  useEffect(() => {
    function reset() {
      pendingRef.current = false
      clearTimeout(timerRef.current)
    }

    function onKeyDown(e: KeyboardEvent) {
      const target = e.target as HTMLElement
      if (
        target.tagName === "INPUT" ||
        target.tagName === "TEXTAREA" ||
        target.isContentEditable
      ) {
        return
      }
      if (e.metaKey || e.ctrlKey || e.altKey) return

      if (!pendingRef.current) {
        if (e.key === "g") {
          pendingRef.current = true
          timerRef.current = setTimeout(reset, CHORD_WINDOW_MS)
        }
        return
      }

      reset()
      const chords = organizationId
        ? orgChords(`/organization/${organizationId}`)
        : PERSONAL_CHORDS
      const to = chords[e.key]
      if (to) {
        e.preventDefault()
        void navigate({ to: to as never })
      }
    }

    window.addEventListener("keydown", onKeyDown)
    return () => {
      window.removeEventListener("keydown", onKeyDown)
      clearTimeout(timerRef.current)
    }
  }, [navigate, organizationId])
}
