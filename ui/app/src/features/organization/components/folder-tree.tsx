import { useState } from "react"
import {
  IconChevronRight,
  IconChevronDown,
  IconFolder,
  IconX,
} from "@tabler/icons-react"
import { cn } from "@/lib/ui"
import type { Folder } from "@/types/organization"

interface Props {
  folders: Folder[]
  activeFolderId: string | undefined
  onSelect: (folderId: string | undefined) => void
  allLabel: string
  /** hover-reveal delete affordance per folder — omit for a read-only tree
   * (e.g. the move-file picker, which shouldn't let you delete a folder
   * mid-move). Errors (like "folder not empty") surface via the caller's
   * own mutation toast, not here. */
  onDelete?: (folderId: string) => void
}

// F-rules — nested folder tree, assembled client-side from the API's flat
// parent_folder_id list (mirrors listFolders' own comment on why it's flat).
export function FolderTree({
  folders,
  activeFolderId,
  onSelect,
  allLabel,
  onDelete,
}: Props) {
  const [expanded, setExpanded] = useState<Set<string>>(new Set())
  const children = new Map<string | undefined, Folder[]>()
  for (const f of folders) {
    const key = f.parent_folder_id
    if (!children.has(key)) children.set(key, [])
    children.get(key)!.push(f)
  }

  function toggle(id: string) {
    setExpanded((prev) => {
      const next = new Set(prev)
      if (next.has(id)) next.delete(id)
      else next.add(id)
      return next
    })
  }

  function renderLevel(parentId: string | undefined, depth: number) {
    const items = (children.get(parentId) ?? []).sort((a, b) =>
      a.name.localeCompare(b.name)
    )
    return items.map((folder) => {
      const hasChildren = (children.get(folder.id) ?? []).length > 0
      const isExpanded = expanded.has(folder.id)
      const isActive = folder.id === activeFolderId
      return (
        <div key={folder.id} className="group/folder-row flex items-center">
          <button
            type="button"
            className={cn(
              "flex flex-1 items-center gap-1 rounded-md px-1.5 py-1 text-left text-sm transition-colors",
              isActive
                ? "bg-primary/10 font-medium text-primary"
                : "text-muted-foreground hover:bg-accent hover:text-foreground"
            )}
            style={{ paddingLeft: `${depth * 14 + 6}px` }}
            onClick={() => onSelect(folder.id)}
          >
            <span
              role="button"
              tabIndex={-1}
              className="flex size-4 shrink-0 items-center justify-center"
              onClick={(e) => {
                e.stopPropagation()
                if (hasChildren) toggle(folder.id)
              }}
            >
              {hasChildren &&
                (isExpanded ? (
                  <IconChevronDown className="size-3" />
                ) : (
                  <IconChevronRight className="size-3" />
                ))}
            </span>
            <IconFolder className="size-3.5 shrink-0" />
            <span className="truncate">{folder.name}</span>
          </button>
          {onDelete && (
            <button
              type="button"
              className="mr-1 hidden shrink-0 text-muted-foreground group-hover/folder-row:block hover:text-destructive"
              onClick={(e) => {
                e.stopPropagation()
                onDelete(folder.id)
              }}
            >
              <IconX className="size-3.5" />
            </button>
          )}
        </div>
      )
    })
  }

  return (
    <div className="flex flex-col gap-0.5">
      <button
        type="button"
        className={cn(
          "flex items-center gap-1.5 rounded-md px-1.5 py-1 text-left text-sm transition-colors",
          activeFolderId === undefined
            ? "bg-primary/10 font-medium text-primary"
            : "text-muted-foreground hover:bg-accent hover:text-foreground"
        )}
        onClick={() => onSelect(undefined)}
      >
        <IconFolder className="size-3.5 shrink-0" />
        {allLabel}
      </button>
      {renderLevel(undefined, 1)}
    </div>
  )
}
