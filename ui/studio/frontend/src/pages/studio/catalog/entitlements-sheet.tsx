import { useState } from "react"
import { IconPlus, IconTrash } from "@tabler/icons-react"
import type { Feature } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table"
import { Textarea } from "@/components/ui/textarea"
import { FieldHint, TableSkeleton } from "../components/table-helpers"
import { formatMetricValue } from "../components/table-utils"

interface EntitlementsSheetProps {
  title: string
  mode: "plan" | "addon"
  projectId: string
  features: Feature[] | null | undefined
  entitlements:
    | {
        feature_id: string
        limit_value: number | null
        config_value?: string
      }[]
    | null
    | undefined
  isLoading: boolean
  onUpsert: (input: {
    feature_id: string
    limit_value: number | null
    config_value: string
  }) => void
  onRemove: (featureId: string) => void
  onClose: () => void
}

export function EntitlementsSheet({
  title,
  mode,
  features,
  entitlements,
  isLoading,
  onUpsert,
  onRemove,
  onClose,
}: EntitlementsSheetProps) {
  const [featureId, setFeatureId] = useState("")
  const [limitValue, setLimitValue] = useState("")
  const [configValue, setConfigValue] = useState("")

  const selectedFeature = features?.find((f) => f.id === featureId)
  const featureById = (id: string) => features?.find((f) => f.id === id)

  const handleAdd = () => {
    if (!featureId) return
    onUpsert({
      feature_id: featureId,
      limit_value: limitValue === "" ? null : Number(limitValue),
      config_value: configValue,
    })
    setFeatureId("")
    setLimitValue("")
    setConfigValue("")
  }

  return (
    <Sheet
      open
      onOpenChange={(o) => {
        if (!o) onClose()
      }}
    >
      <SheetContent className="flex w-full flex-col gap-0 overflow-y-auto sm:max-w-xl">
        <SheetHeader className="pb-4">
          <SheetTitle>{title}</SheetTitle>
          <SheetDescription>
            {mode === "plan"
              ? "Attach or remove the features this plan grants, and set each one's limit."
              : "Attach or remove the features this addon grants on top of a subscriber's base plan."}
          </SheetDescription>
        </SheetHeader>

        <div className="mx-6 flex flex-col gap-4 py-4">
          {isLoading ? (
            <TableSkeleton cols={3} />
          ) : !entitlements || entitlements.length === 0 ? (
            <p className="py-4 text-center text-sm text-muted-foreground">
              No entitlements yet
            </p>
          ) : (
            <Table>
              <TableHeader>
                <TableRow>
                  <TableHead>Feature</TableHead>
                  <TableHead>Value</TableHead>
                  <TableHead className="w-14 text-right">—</TableHead>
                </TableRow>
              </TableHeader>
              <TableBody>
                {entitlements.map((e) => {
                  const f = featureById(e.feature_id)
                  return (
                    <TableRow key={e.feature_id}>
                      <TableCell className="text-sm">
                        {f?.name ?? e.feature_id}
                      </TableCell>
                      <TableCell className="font-mono text-xs">
                        {e.limit_value !== null
                          ? `${mode === "addon" ? "+" : ""}${formatMetricValue(e.limit_value, f?.metric_key)}`
                          : e.config_value || "included"}
                      </TableCell>
                      <TableCell className="text-right">
                        <Button
                          variant="ghost"
                          size="sm"
                          className="h-7 w-7 p-0 text-destructive hover:text-destructive"
                          onClick={() => onRemove(e.feature_id)}
                        >
                          <IconTrash className="size-3.5" />
                        </Button>
                      </TableCell>
                    </TableRow>
                  )
                })}
              </TableBody>
            </Table>
          )}

          <div className="flex flex-col gap-2 border-t pt-4">
            <Label>Add entitlement</Label>
            <Select
              value={featureId}
              onValueChange={(v) => setFeatureId(v ?? "")}
            >
              <SelectTrigger className="w-full">
                <SelectValue placeholder="Select a feature" />
              </SelectTrigger>
              <SelectContent>
                {features?.map((f) => (
                  <SelectItem key={f.id} value={f.id}>
                    <div className="flex flex-col">
                      <span>
                        {f.name}{" "}
                        <span className="text-muted-foreground">
                          ({f.type})
                        </span>
                      </span>
                      {f.description && (
                        <span className="text-xs text-muted-foreground">
                          {f.description}
                        </span>
                      )}
                    </div>
                  </SelectItem>
                ))}
              </SelectContent>
            </Select>
            <FieldHint>
              {mode === "plan"
                ? "Pick a feature to grant on this plan."
                : "Pick a feature this addon adds on top of the base plan."}
            </FieldHint>

            {selectedFeature &&
              selectedFeature.type !== "static" &&
              selectedFeature.type !== "boolean" && (
                <>
                  <Input
                    type="number"
                    placeholder="Limit value (-1 = unlimited)"
                    value={limitValue}
                    onChange={(e) => setLimitValue(e.target.value)}
                  />
                  <FieldHint>
                    {mode === "plan"
                      ? "The absolute cap this plan grants (e.g. 25 members). Use -1 for unlimited."
                      : "An ADDITIVE amount on top of whatever the subscriber's base plan already grants (e.g. +5 members) — not a replacement value."}
                    {selectedFeature?.metric_key?.endsWith("_bytes") && (
                      <>
                        {" "}
                        Enter this in raw bytes, not GB/MB — e.g. 10 GB ={" "}
                        <code className="font-mono">10737418240</code>.
                      </>
                    )}
                  </FieldHint>
                </>
              )}
            {selectedFeature?.type === "config" && (
              <>
                <Textarea
                  rows={3}
                  className="font-mono text-xs"
                  placeholder='{"rate_limit": 1000}'
                  value={configValue}
                  onChange={(e) => setConfigValue(e.target.value)}
                />
                <FieldHint>
                  JSON delivered to the app as this feature's entitlement value.
                </FieldHint>
              </>
            )}
            {selectedFeature &&
              (selectedFeature.type === "static" ||
                selectedFeature.type === "boolean") && (
                <FieldHint>
                  {selectedFeature.type === "boolean"
                    ? "Boolean feature — no value needed. Adding it here is what turns access on."
                    : 'Static feature — no value needed. Adding it here is purely informational (e.g. "SLA guarantee included").'}
                </FieldHint>
              )}

            <Button size="sm" onClick={handleAdd} disabled={!featureId}>
              <IconPlus className="size-4" />
              Add / update
            </Button>
          </div>
        </div>

        <SheetFooter className="mt-auto pt-4">
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
