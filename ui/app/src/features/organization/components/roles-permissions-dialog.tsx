import { IconCheck, IconMinus } from "@tabler/icons-react"
import { useTranslation } from "react-i18next"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { hasPermission, PERMISSION_MATRIX } from "@/lib/permissions-matrix"
import type {
  OrganizationRole,
  PermissionAction,
  PermissionFeature,
} from "@/types/organization"

const ROLES: OrganizationRole[] = ["member", "admin", "owner"]

// Every (feature, action) pair the matrix defines, read from the matrix itself,
// so this reference can never drift from what the app enforces.
const ROWS = (Object.keys(PERMISSION_MATRIX) as PermissionFeature[]).flatMap(
  (feature) =>
    (Object.keys(PERMISSION_MATRIX[feature]) as PermissionAction[]).map(
      (action) => ({ feature, action })
    )
)

// A read-only reference of what each role can do, open to every member, so
// a role change can be understood before it is made.
export function RolesPermissionsDialog() {
  const { t } = useTranslation()
  return (
    <Dialog>
      <DialogTrigger render={<Button variant="ghost" size="sm" />}>
        {t("organization.permissions.open")}
      </DialogTrigger>
      <DialogContent className="max-h-[85vh] overflow-y-auto sm:max-w-lg">
        <DialogHeader>
          <DialogTitle>{t("organization.permissions.title")}</DialogTitle>
          <DialogDescription>
            {t("organization.permissions.description")}
          </DialogDescription>
        </DialogHeader>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>{t("organization.permissions.capability")}</TableHead>
              {ROLES.map((role) => (
                <TableHead key={role} className="text-center">
                  {t(`organization.roles.${role}`)}
                </TableHead>
              ))}
            </TableRow>
          </TableHeader>
          <TableBody>
            {ROWS.map(({ feature, action }) => (
              <TableRow key={`${feature}.${action}`}>
                <th
                  scope="row"
                  className="p-2 text-left align-middle font-normal whitespace-nowrap"
                >
                  {t(`organization.permissions.features.${feature}`)}
                  <span aria-hidden> · </span>
                  <span className="sr-only">, </span>
                  {t(`organization.permissions.actions.${action}`)}
                </th>
                {ROLES.map((role) => (
                  <TableCell key={role} className="text-center">
                    {hasPermission(role, feature, action) ? (
                      <>
                        <IconCheck
                          aria-hidden
                          className="mx-auto size-4 text-primary"
                        />
                        <span className="sr-only">
                          {t("organization.permissions.allowed")}
                        </span>
                      </>
                    ) : (
                      <>
                        <IconMinus
                          aria-hidden
                          className="mx-auto size-4 text-muted-foreground"
                        />
                        <span className="sr-only">
                          {t("organization.permissions.notAllowed")}
                        </span>
                      </>
                    )}
                  </TableCell>
                ))}
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </DialogContent>
    </Dialog>
  )
}
