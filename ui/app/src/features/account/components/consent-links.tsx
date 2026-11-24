import { useTranslation } from "react-i18next"
import type { ConsentStatus } from "@/types/account"

// Links to the documents to accept, opened in a new tab.
export function ConsentLinks({
  items,
}: {
  items: Pick<ConsentStatus, "document" | "url">[]
}) {
  const { t } = useTranslation()
  return (
    <span className="inline-flex flex-wrap gap-x-2">
      {items.map((c) => (
        <a
          key={c.document}
          href={c.url}
          target="_blank"
          rel="noopener noreferrer"
          className="font-medium text-foreground underline underline-offset-4"
        >
          {t(`legal.documents.${c.document}`)}
        </a>
      ))}
    </span>
  )
}
