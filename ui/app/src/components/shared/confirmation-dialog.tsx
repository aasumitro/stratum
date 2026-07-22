import { useState, type ReactElement, type ReactNode } from "react"
import { IconLoader2 } from "@tabler/icons-react"
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
  AlertDialogTrigger,
} from "@/components/ui/alert-dialog"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"

interface ConfirmationDialogProps {
  /** the trigger element styling (e.g. <Button variant="destructive" size="sm" />), per the AlertDialogTrigger `render` convention */
  render: ReactElement
  /** set false when `render` isn't a real <button> (e.g. a hidden <span>
   * for a dialog opened programmatically via `open`/`onOpenChange`) —
   * must match what `render` actually is, Base UI warns either way if not */
  nativeButton?: boolean
  /** trigger visible content (label/icon) */
  children: ReactNode
  title: string
  description: ReactNode
  /** consequences enumerated, one per line */
  consequences?: string[]
  /** if set, the confirm button stays locked until the user types this exact string */
  confirmPhrase?: string
  confirmPhraseLabel?: string
  confirmLabel: string
  cancelLabel?: string
  destructive?: boolean
  onConfirm: () => void
  pending?: boolean
  open?: boolean
  onOpenChange?: (open: boolean) => void
}

/**
 * Destructive/consequential confirmation pattern: enumerate
 * consequences → optional type-to-confirm → button enables → caller fires
 * onConfirm. Replaces the many hand-rolled AlertDialogs across
 * members/organization/webhooks/account.
 */
export function ConfirmationDialog({
  render,
  nativeButton = true,
  children,
  title,
  description,
  consequences,
  confirmPhrase,
  confirmPhraseLabel,
  confirmLabel,
  cancelLabel = "Cancel",
  destructive = true,
  onConfirm,
  pending = false,
  open,
  onOpenChange,
}: ConfirmationDialogProps) {
  const [typed, setTyped] = useState("")
  const locked = !!confirmPhrase && typed !== confirmPhrase

  return (
    <AlertDialog
      open={open}
      onOpenChange={(next) => {
        if (!next) setTyped("")
        onOpenChange?.(next)
      }}
    >
      <AlertDialogTrigger render={render} nativeButton={nativeButton}>
        {children}
      </AlertDialogTrigger>
      <AlertDialogContent>
        <AlertDialogHeader>
          <AlertDialogTitle>{title}</AlertDialogTitle>
          <AlertDialogDescription>{description}</AlertDialogDescription>
        </AlertDialogHeader>

        {consequences && consequences.length > 0 && (
          <ul className="list-disc space-y-1 rounded-xl bg-muted px-6 py-3 text-left text-sm text-muted-foreground">
            {consequences.map((c) => (
              <li key={c}>{c}</li>
            ))}
          </ul>
        )}

        {confirmPhrase && (
          <div className="flex flex-col gap-1.5 text-left">
            <Label htmlFor="confirm-phrase" className="text-xs">
              {confirmPhraseLabel ?? `Type "${confirmPhrase}" to confirm`}
            </Label>
            <Input
              id="confirm-phrase"
              value={typed}
              onChange={(e) => setTyped(e.target.value)}
              autoComplete="off"
              placeholder={confirmPhrase}
            />
          </div>
        )}

        <AlertDialogFooter>
          <AlertDialogCancel>{cancelLabel}</AlertDialogCancel>
          <AlertDialogAction
            variant={destructive ? "destructive" : "default"}
            disabled={locked || pending}
            onClick={() => {
              onConfirm()
            }}
          >
            {pending && (
              <IconLoader2 data-icon="inline-start" className="animate-spin" />
            )}
            {confirmLabel}
          </AlertDialogAction>
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  )
}
