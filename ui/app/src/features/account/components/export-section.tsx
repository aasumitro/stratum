import { useState, useEffect } from "react"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { IconLoader2, IconDownload } from "@tabler/icons-react"
import { Button } from "@/components/ui/button"
import { useTasks, usePollTask, useExportData } from "@/features/account/hooks"

function downloadJson(data: unknown, filename: string) {
  const blob = new Blob([JSON.stringify(data, null, 2)], {
    type: "application/json",
  })
  const url = URL.createObjectURL(blob)
  const a = document.createElement("a")
  a.href = url
  a.download = filename
  a.click()
  URL.revokeObjectURL(url)
}

export function ExportSection() {
  const { t } = useTranslation()
  const [exportTaskId, setExportTaskId] = useState<string | null>(null)

  const { data: tasksData } = useTasks()
  const { data: exportTask } = usePollTask(exportTaskId)
  const { mutate: startExport, isPending } = useExportData()

  const taskStatus = exportTask?.data?.status
  const isPolling =
    !!exportTaskId && taskStatus !== "completed" && taskStatus !== "failed"

  // derive exportedData from current poll result or latest completed from history
  const exportedData: unknown =
    (taskStatus === "completed" ? exportTask?.data?.result : null) ??
    (tasksData?.data ?? [])
      .filter(
        (task) => task.kind === "export_data" && task.status === "completed"
      )
      .sort(
        (a, b) =>
          new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
      )[0]?.result ??
    null

  useEffect(() => {
    if (!taskStatus) return
    if (taskStatus === "completed") {
      toast.success(t("account.dangerZone.exportReady"))
    } else if (taskStatus === "failed") {
      toast.error(t("account.dangerZone.exportFailed"))
    }
  }, [taskStatus, t])

  return (
    <div className="flex flex-col gap-1 sm:flex-row sm:items-start sm:justify-between sm:gap-4">
      <div>
        <p className="text-sm font-medium">
          {t("account.dangerZone.exportTitle")}
        </p>
        <p className="text-sm text-muted-foreground">
          {t("account.dangerZone.exportDescription")}
        </p>
      </div>
      <div className="flex shrink-0 gap-2">
        {exportedData != null && (
          <Button
            variant="default"
            size="sm"
            onClick={() => downloadJson(exportedData, "stratum-export.json")}
          >
            <IconDownload data-icon="inline-start" />
            {t("account.dangerZone.downloadExport")}
          </Button>
        )}
        <Button
          variant="outline"
          size="sm"
          disabled={isPending || isPolling}
          onClick={() =>
            startExport(undefined, {
              onSuccess: (res) => {
                if (res.data?.id) {
                  setExportTaskId(res.data.id)
                  toast.info(t("account.dangerZone.preparing"))
                }
              },
              onError: () =>
                toast.error(t("account.dangerZone.exportStartFailed")),
            })
          }
        >
          {isPending || isPolling ? (
            <IconLoader2 data-icon="inline-start" className="animate-spin" />
          ) : (
            <IconDownload data-icon="inline-start" />
          )}
          {isPolling
            ? t("account.dangerZone.preparing")
            : t("account.dangerZone.requestExport")}
        </Button>
      </div>
    </div>
  )
}
