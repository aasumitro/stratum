import { useTranslation } from "react-i18next"
import { IconExternalLink } from "@tabler/icons-react"
import {
  Tooltip,
  TooltipContent,
  TooltipTrigger,
} from "@/components/ui/tooltip"

interface DocumentationLinksProps {
  labels: string[]
}

/**
 * Placeholder documentation links for sections whose docs haven't been
 * written yet. Deliberately non-interactive — a disabled control that does
 * nothing shouldn't be a keyboard tab stop — with a shared "coming soon"
 * tooltip for sighted/mouse users.
 */
export function DocumentationLinks({ labels }: DocumentationLinksProps) {
  const { t } = useTranslation()
  if (!labels.length) return null

  return (
    <div className="flex flex-col gap-1.5 md:mt-auto">
      <p className="text-xs font-medium tracking-wide text-muted-foreground uppercase">
        {t("organization.settings.moreInformation")}
      </p>
      {labels.map((label) => (
        <Tooltip key={label}>
          <TooltipTrigger
            render={
              <span className="flex w-fit cursor-not-allowed items-center gap-1 text-sm text-muted-foreground/60" />
            }
          >
            {label}
            <IconExternalLink className="size-3.5" />
          </TooltipTrigger>
          <TooltipContent>
            {t("organization.settings.docsComingSoon")}
          </TooltipContent>
        </Tooltip>
      ))}
    </div>
  )
}
