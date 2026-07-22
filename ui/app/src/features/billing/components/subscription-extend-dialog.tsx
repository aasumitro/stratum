import { useTranslation } from "react-i18next"
import { IconLoader2 } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"

interface Props {
  open: boolean
  onOpenChange: (open: boolean) => void
  months: string
  onMonthsChange: (months: string) => void
  extending: boolean
  onExtend: () => void
}

export function SubscriptionExtendDialog({
  open,
  onOpenChange,
  months,
  onMonthsChange,
  extending,
  onExtend,
}: Props) {
  const { t } = useTranslation()
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogTrigger render={<Button variant="outline" size="sm" />}>
        {t("billing.subscription.extend")}
      </DialogTrigger>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("billing.subscription.extendTitle")}</DialogTitle>
          <DialogDescription>
            {t("billing.subscription.extendDescription")}
          </DialogDescription>
        </DialogHeader>
        <div className="flex flex-col gap-1.5 py-2">
          <label className="text-sm font-medium">
            {t("billing.subscription.extendMonths")}
          </label>
          <Select
            value={months}
            onValueChange={(v) => onMonthsChange(v ?? "1")}
          >
            <SelectTrigger>
              <SelectValue />
            </SelectTrigger>
            <SelectContent>
              {[1, 3, 6, 12, 24].map((m) => (
                <SelectItem key={m} value={String(m)}>
                  {m}
                </SelectItem>
              ))}
            </SelectContent>
          </Select>
        </div>
        <DialogFooter>
          <Button disabled={extending} onClick={onExtend}>
            {extending && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {extending
              ? t("billing.subscription.extending")
              : t("billing.subscription.extendConfirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  )
}
