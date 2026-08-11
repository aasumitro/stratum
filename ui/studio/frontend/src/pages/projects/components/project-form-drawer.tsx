import { useState } from "react"
import { IconCheck, IconLoader2, IconX } from "@tabler/icons-react"
import type { Project } from "../../../../bindings/github.com/aasumitro/stratum/studio/app/models.js"
import {
  useAddProject,
  useTestConnection,
  useUpdateProject,
} from "@/hooks/use-projects"
import { Button } from "@/components/ui/button"
import { Input } from "@/components/ui/input"
import { Label } from "@/components/ui/label"
import {
  Sheet,
  SheetContent,
  SheetDescription,
  SheetFooter,
  SheetHeader,
  SheetTitle,
} from "@/components/ui/sheet"

const ACCENT_COLORS = [
  "#6366f1",
  "#8b5cf6",
  "#ec4899",
  "#ef4444",
  "#f97316",
  "#eab308",
  "#22c55e",
  "#14b8a6",
  "#0ea5e9",
  "#64748b",
]

type TestStatus = "idle" | "testing" | "ok" | "error"

interface DsnFieldProps {
  id: string
  label: string
  placeholder: string
  value: string
  required?: boolean
  onType: "database" | "mq" | "redis"
  onChange: (value: string) => void
}

function DsnField({
  id,
  label,
  placeholder,
  value,
  required,
  onType,
  onChange,
}: DsnFieldProps) {
  const [status, setStatus] = useState<TestStatus>("idle")
  const [errorMsg, setErrorMsg] = useState("")
  const [prevValue, setPrevValue] = useState(value)
  const test = useTestConnection()

  if (value !== prevValue) {
    setPrevValue(value)
    setStatus("idle")
    setErrorMsg("")
  }

  const handleTest = async () => {
    if (!value.trim()) return
    setStatus("testing")
    setErrorMsg("")
    try {
      await test.mutateAsync({ type: onType, dsn: value.trim() })
      setStatus("ok")
    } catch (err) {
      setStatus("error")
      setErrorMsg(String(err))
    }
  }

  return (
    <div className="flex flex-col gap-1.5">
      <Label htmlFor={id}>
        {label}
        {required && <span className="ml-0.5 text-destructive">*</span>}
      </Label>
      <div className="flex gap-2">
        <Input
          id={id}
          value={value}
          onChange={(e) => onChange(e.target.value)}
          placeholder={placeholder}
          className="font-mono text-xs"
        />
        <Button
          type="button"
          variant="outline"
          size="sm"
          disabled={!value.trim() || status === "testing"}
          onClick={handleTest}
          className="w-16 shrink-0"
        >
          {status === "testing" ? (
            <IconLoader2 className="size-3 animate-spin" />
          ) : status === "ok" ? (
            <IconCheck className="size-3 text-green-500" />
          ) : status === "error" ? (
            <IconX className="size-3 text-destructive" />
          ) : (
            "Test"
          )}
        </Button>
      </div>
      {status === "error" && errorMsg && (
        <p className="text-xs text-destructive">{errorMsg}</p>
      )}
    </div>
  )
}

interface ProjectFormDrawerProps {
  open: boolean
  project?: Project
  onClose: () => void
}

export function ProjectFormDrawer({
  open,
  project,
  onClose,
}: ProjectFormDrawerProps) {
  const isEdit = project !== undefined
  const add = useAddProject()
  const update = useUpdateProject()

  const [name, setName] = useState("")
  const [apiUrl, setApiUrl] = useState("")
  const [dbDsn, setDbDsn] = useState("")
  const [mqDsn, setMqDsn] = useState("")
  const [redisDsn, setRedisDsn] = useState("")
  const [statsToken, setStatsToken] = useState("")
  const [color, setColor] = useState(ACCENT_COLORS[0])
  const [wasOpen, setWasOpen] = useState(open)

  if (open !== wasOpen) {
    setWasOpen(open)
    if (open) {
      setName(project?.name ?? "")
      setApiUrl(project?.api_url ?? "")
      setDbDsn(project?.db_dsn ?? "")
      setMqDsn(project?.mq_dsn ?? "")
      setRedisDsn(project?.redis_dsn ?? "")
      setStatsToken(project?.stats_token ?? "")
      setColor(project?.color ?? ACCENT_COLORS[0])
    }
  }

  const isPending = add.isPending || update.isPending
  const isValid = name.trim() && apiUrl.trim() && dbDsn.trim() && mqDsn.trim()

  const handleSubmit = async () => {
    if (!isValid) return
    const input = {
      name: name.trim(),
      api_url: apiUrl.trim(),
      db_dsn: dbDsn.trim(),
      mq_dsn: mqDsn.trim(),
      redis_dsn: redisDsn.trim(),
      stats_token: statsToken.trim(),
      color,
    }
    try {
      if (isEdit && project) {
        await update.mutateAsync({ id: project.id, input })
      } else {
        await add.mutateAsync(input)
      }
      onClose()
    } catch {
      // errors are shown by the mutation state
    }
  }

  const error = add.error ?? update.error

  return (
    <Sheet open={open} onOpenChange={(v: boolean) => !v && onClose()}>
      <SheetContent className="flex flex-col gap-0 overflow-y-auto sm:max-w-md">
        <SheetHeader className="pb-4">
          <SheetTitle>{isEdit ? "Edit Project" : "Add Project"}</SheetTitle>
          <SheetDescription>
            {isEdit
              ? "Update the connection details for this project."
              : "Register a Stratum deployment to manage from Studio."}
          </SheetDescription>
        </SheetHeader>

        <div className="mx-6 flex flex-col gap-5 py-4">
          <div className="flex flex-col gap-1.5">
            <Label htmlFor="name">
              Name <span className="text-destructive">*</span>
            </Label>
            <Input
              id="name"
              value={name}
              onChange={(e) => setName(e.target.value)}
              placeholder="Production"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="api-url">
              API URL <span className="text-destructive">*</span>
            </Label>
            <Input
              id="api-url"
              value={apiUrl}
              onChange={(e) => setApiUrl(e.target.value)}
              placeholder="https://api.yourproject.com"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <Label htmlFor="stats-token">Stats token</Label>
            <Input
              id="stats-token"
              value={statsToken}
              onChange={(e) => setStatsToken(e.target.value)}
              placeholder="matches the project's STATS_TOKEN (optional)"
              className="font-mono text-xs"
            />
          </div>

          <DsnField
            id="db-dsn"
            label="Database DSN"
            placeholder="postgres://user:pass@host:5432/db"
            value={dbDsn}
            required
            onType="database"
            onChange={setDbDsn}
          />

          <DsnField
            id="mq-dsn"
            label="Message Queue DSN"
            placeholder="amqp://user:pass@host:5672/"
            value={mqDsn}
            required
            onType="mq"
            onChange={setMqDsn}
          />

          <DsnField
            id="redis-dsn"
            label="Redis DSN"
            placeholder="redis://host:6379 (optional)"
            value={redisDsn}
            onType="redis"
            onChange={setRedisDsn}
          />

          <div className="flex flex-col gap-2">
            <Label>Accent color</Label>
            <div className="flex flex-wrap gap-2">
              {ACCENT_COLORS.map((c) => (
                <button
                  key={c}
                  type="button"
                  className="size-6 rounded-full ring-offset-2 transition-all focus-visible:outline-hidden"
                  style={{
                    backgroundColor: c,
                    outline:
                      color === c ? `2px solid ${c}` : "2px solid transparent",
                    outlineOffset: 2,
                  }}
                  onClick={() => setColor(c)}
                  aria-label={`Select color ${c}`}
                />
              ))}
            </div>
          </div>

          {error && <p className="text-sm text-destructive">{String(error)}</p>}
        </div>

        <SheetFooter className="mt-auto pt-4">
          <Button variant="outline" onClick={onClose} disabled={isPending}>
            Cancel
          </Button>
          <Button onClick={handleSubmit} disabled={!isValid || isPending}>
            {isPending ? (
              <IconLoader2 className="mr-2 size-4 animate-spin" />
            ) : null}
            {isEdit ? "Save changes" : "Add project"}
          </Button>
        </SheetFooter>
      </SheetContent>
    </Sheet>
  )
}
