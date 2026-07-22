import { useRef } from "react"
import { useTranslation } from "react-i18next"
import { IconUpload } from "@tabler/icons-react"
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "@/components/ui/card"
import { Button } from "@/components/ui/button"
import { Avatar, AvatarImage, AvatarFallback } from "@/components/ui/avatar"
import { useUploadLogo } from "@/features/organization/hooks"
import type { Organization } from "@/types/organization"

interface OrganizationLogoFormProps {
  organization: Organization
}

export function OrganizationLogoForm({
  organization,
}: OrganizationLogoFormProps) {
  const { t } = useTranslation()
  const inputRef = useRef<HTMLInputElement>(null)
  const uploadLogo = useUploadLogo(organization.id)

  const logoSrc = (organization.settings as { logo_url?: string } | undefined)
    ?.logo_url

  function handleChange(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0]
    if (!file) return
    uploadLogo.mutate(file)
    e.target.value = ""
  }

  return (
    <Card>
      <CardHeader>
        <CardTitle>{t("organization.logoTitle")}</CardTitle>
        <CardDescription>{t("organization.logoDescription")}</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="flex items-center gap-4">
          <Avatar size="lg" className="size-14 rounded-lg">
            <AvatarImage
              src={logoSrc}
              alt={organization.name}
              className="object-cover"
            />
            <AvatarFallback className="rounded-lg text-base font-semibold">
              {organization.name.slice(0, 2).toUpperCase()}
            </AvatarFallback>
          </Avatar>

          <div>
            <input
              ref={inputRef}
              type="file"
              accept="image/*"
              className="hidden"
              onChange={handleChange}
            />
            <Button
              variant="outline"
              size="sm"
              disabled={uploadLogo.isPending}
              onClick={() => inputRef.current?.click()}
            >
              <IconUpload className="mr-1.5 size-4" />
              {t("organization.logoUpload")}
            </Button>
            <p className="mt-1.5 text-xs text-muted-foreground">
              {t("organization.logoHint")}
            </p>
          </div>
        </div>
      </CardContent>
    </Card>
  )
}
