import { useEffect, useState, type ReactNode } from "react"
import { useNavigate, useRouterState } from "@tanstack/react-router"
import { useTranslation } from "react-i18next"
import {
  IconBuilding,
  IconPlus,
  IconDoorEnter,
  IconPinned,
  IconPin,
  IconAlertTriangle,
} from "@tabler/icons-react"
import {
  Popover,
  PopoverContent,
  PopoverTrigger,
} from "@/components/ui/popover"
import {
  Command,
  CommandEmpty,
  CommandGroup,
  CommandInput,
  CommandItem,
  CommandList,
  CommandSeparator,
} from "@/components/ui/command"
import { useTransientStore } from "@/hooks/use-transient"
import { useUpdatePreferences } from "@/features/account/hooks"
import { cn } from "@/lib/ui"
import type { OrganizationView } from "@/types/organization"

interface Props {
  organizations: OrganizationView[]
  activeOrganizationId?: string
  defaultOrganizationId?: string
  children: ReactNode
  triggerClassName?: string
  /**
   * listens for the global ⌘O shortcut. Only one mounted instance owns this
   * at a time: the sidebar instance in Personal context (no active org), the
   * breadcrumb instance in Organization context — never both at once, since
   * the sidebar shows static branding instead of this component once an org
   * is active.
   */
  enableShortcut?: boolean
  align?: "start" | "end"
}

/**
 * Organization switcher: search, arrow+Enter (native to cmdk),
 * preserves the current section when switching (Members -> Members), and a
 * "Set default" pin per row. Sole switcher in Personal context (sidebar
 * header) and sole switcher in Organization context (breadcrumb) — the
 * sidebar shows static branding instead of a second instance once an org is
 * active, so exactly one is ever mounted.
 */
export function OrganizationSwitcher({
  organizations,
  activeOrganizationId,
  defaultOrganizationId,
  children,
  triggerClassName,
  enableShortcut = false,
  align = "start",
}: Props) {
  const { t } = useTranslation()
  const navigate = useNavigate()
  const { location } = useRouterState()
  const { mutate: updatePreferences } = useUpdatePreferences()
  const [open, setOpen] = useState(false)

  useEffect(() => {
    if (!enableShortcut) return
    function onKeydown(e: KeyboardEvent) {
      if (e.key !== "o" || !(e.metaKey || e.ctrlKey)) return
      e.preventDefault()
      setOpen((v) => !v)
    }
    document.addEventListener("keydown", onKeydown)
    return () => document.removeEventListener("keydown", onKeydown)
  }, [enableShortcut])

  function currentTail(): string {
    // pathname segments after "organization" and the org id itself.
    return location.pathname.split("/").filter(Boolean).slice(2).join("/")
  }

  function switchTo(id: string) {
    setOpen(false)
    const tail = currentTail()
    void navigate({
      to: (tail
        ? `/organization/${id}/${tail}`
        : `/organization/${id}`) as never,
    })
  }

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger className={triggerClassName}>{children}</PopoverTrigger>
      <PopoverContent align={align} className="w-72 p-0">
        <Command>
          <CommandInput placeholder={t("nav.findOrganization")} />
          <CommandList>
            <CommandEmpty>{t("nav.noOrganizations")}</CommandEmpty>
            <CommandGroup>
              {organizations.map((org) => (
                <CommandItem
                  key={org.id}
                  value={org.name}
                  data-checked={
                    org.id === activeOrganizationId ? "true" : undefined
                  }
                  onSelect={() => switchTo(org.id)}
                  className="justify-between"
                >
                  <span className="flex min-w-0 items-center gap-2">
                    <IconBuilding className="size-3.5 shrink-0 text-muted-foreground" />
                    <span
                      className={cn(
                        "truncate",
                        org.status === "suspended" && "text-muted-foreground"
                      )}
                    >
                      {org.name}
                    </span>
                    {org.status === "suspended" && (
                      <IconAlertTriangle className="size-3.5 shrink-0 text-destructive" />
                    )}
                  </span>
                  <button
                    type="button"
                    title={t("nav.setDefault")}
                    className={cn(
                      "shrink-0 rounded p-0.5 opacity-0 group-hover/command-item:opacity-100 hover:bg-accent",
                      org.id === defaultOrganizationId && "opacity-100"
                    )}
                    onClick={(e) => {
                      e.stopPropagation()
                      updatePreferences({ default_organization_id: org.id })
                    }}
                  >
                    {org.id === defaultOrganizationId ? (
                      <IconPinned className="size-3.5 text-primary" />
                    ) : (
                      <IconPin className="size-3.5" />
                    )}
                  </button>
                </CommandItem>
              ))}
            </CommandGroup>
            <CommandSeparator />
            <CommandGroup>
              <CommandItem
                onSelect={() => {
                  setOpen(false)
                  void navigate({ to: "/organizations" })
                }}
              >
                <IconBuilding className="size-3.5" />
                {t("nav.allOrganizations")}
              </CommandItem>
              <CommandItem
                onSelect={() => {
                  setOpen(false)
                  useTransientStore.setValue("create-organization", "open")
                }}
              >
                <IconPlus className="size-3.5" />
                {t("dashboard.newOrganization")}
              </CommandItem>
              <CommandItem
                onSelect={() => {
                  setOpen(false)
                  useTransientStore.setValue("join-organization", "open")
                }}
              >
                <IconDoorEnter className="size-3.5" />
                {t("organization.inviteCode.joinBtn")}
              </CommandItem>
            </CommandGroup>
          </CommandList>
        </Command>
      </PopoverContent>
    </Popover>
  )
}
