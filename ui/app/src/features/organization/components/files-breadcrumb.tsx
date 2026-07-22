import { IconChevronRight } from "@tabler/icons-react"

export interface FilesBreadcrumbItem {
  id: string | undefined
  name: string
}

interface Props {
  items: FilesBreadcrumbItem[]
  onNavigate: (folderId: string | undefined) => void
}

export function FilesBreadcrumb({ items, onNavigate }: Props) {
  return (
    <div className="flex items-center gap-1 text-sm text-muted-foreground">
      {items.map((crumb, i) => (
        <span key={crumb.id ?? "root"} className="flex items-center gap-1">
          {i > 0 && <IconChevronRight className="size-3.5" />}
          <button
            type="button"
            className="hover:text-foreground hover:underline"
            onClick={() => onNavigate(crumb.id)}
          >
            {crumb.name}
          </button>
        </span>
      ))}
    </div>
  )
}
