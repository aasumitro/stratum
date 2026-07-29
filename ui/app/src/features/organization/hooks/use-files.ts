import { useQueryClient } from "@tanstack/react-query"
import { useTranslation } from "react-i18next"
import { toast } from "sonner"
import { useHTTPQuery } from "@/lib/api/query"
import {
  useHTTPActionPost,
  useHTTPActionPatch,
  useHTTPActionDelete,
} from "@/lib/api/action"
import { queryKeys } from "@/lib/api/keys"
import { API } from "@/lib/api/path"
import { api } from "@/lib/api/axios"
import type { HTTPResponse } from "@/lib/api/response"
import type { OrganizationFile, Folder } from "@/types/organization"

// folderId scopes the listing to one folder (undefined = root);
// search + searchAll implement the "search this folder" vs "search all
// folders" toggle.
export function useOrganizationFiles(
  organizationId: string,
  folderId?: string,
  search?: string,
  searchAll?: boolean,
  cursor?: string,
  enabled = true
) {
  const params = new URLSearchParams({ limit: "20" })
  if (folderId) params.set("folder_id", folderId)
  if (search) params.set("search", search)
  if (searchAll) params.set("search_all", "true")
  if (cursor) params.set("cursor", cursor)
  return useHTTPQuery<{ items: OrganizationFile[]; next_cursor: string }>({
    queryKey: queryKeys.organizations.files(
      organizationId,
      folderId,
      search,
      searchAll,
      cursor
    ),
    url: `${API.files(organizationId)}?${params}`,
    options: { enabled },
  })
}

export function useOrganizationTrash(organizationId: string, enabled = true) {
  return useHTTPQuery<OrganizationFile[]>({
    queryKey: queryKeys.organizations.trash(organizationId),
    url: API.files(organizationId, "trash"),
    options: { enabled },
  })
}

function invalidateFileQueries(
  queryClient: ReturnType<typeof useQueryClient>,
  organizationId: string
) {
  void queryClient.invalidateQueries({
    queryKey: ["organizations", organizationId, "files"],
  })
}

// useUploadFile is deliberately NOT the shared useHTTPActionUpload wrapper
// — per-file upload progress + cancel needs a raw axios
// call with onUploadProgress and an AbortController the shared upload
// hook's fire-and-forget FormData post doesn't expose. Callers track
// progress/cancel state themselves (see files-page.tsx's upload queue).
export async function uploadOrganizationFile(
  organizationId: string,
  file: File,
  folderId: string | undefined,
  onProgress: (percent: number) => void,
  signal: AbortSignal
): Promise<OrganizationFile> {
  const form = new FormData()
  form.append("file", file)
  if (folderId) form.append("folder_id", folderId)
  const res = await api.post<HTTPResponse<OrganizationFile>>(
    API.files(organizationId),
    form,
    {
      signal,
      onUploadProgress: (e) => {
        if (e.total) onProgress(Math.round((e.loaded / e.total) * 100))
      },
    }
  )
  return res.data.data!
}

export function useDeleteOrganizationFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (fileId) => API.files(organizationId, fileId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.deletedToTrash"))
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

export function useBulkDeleteFiles(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<{ deleted_count: number }, { file_ids: string[] }>({
    url: `${API.files(organizationId, "bulk-delete")}`,
    options: {
      onSuccess: (res) => {
        toast.success(
          t("organization.files.bulkDeleted", {
            count: res.data?.deleted_count ?? 0,
          })
        )
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

export function useMoveFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    OrganizationFile,
    { fileId: string; folder_id: string | null }
  >({
    url: ({ fileId }) => API.files(organizationId, fileId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.moved"))
        invalidateFileQueries(queryClient, organizationId)
      },
      onError: () => toast.error(t("organization.files.moveFailed")),
    },
  })
}

export function useRestoreFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<OrganizationFile, string>({
    url: (fileId) => API.files(organizationId, fileId, "restore"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.restored"))
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

export function usePurgeFile(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (fileId) => API.files(organizationId, fileId, "permanent"),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.purged"))
        invalidateFileQueries(queryClient, organizationId)
      },
    },
  })
}

export function useOrganizationFolders(organizationId: string) {
  return useHTTPQuery<Folder[]>({
    queryKey: queryKeys.organizations.folders(organizationId),
    url: API.files(organizationId, "folders"),
  })
}

export function useCreateFolder(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPost<Folder, { name: string; parent_folder_id?: string }>(
    {
      url: API.files(organizationId, "folders"),
      options: {
        onSuccess: () => {
          toast.success(t("organization.files.folderCreated"))
          void queryClient.invalidateQueries({
            queryKey: queryKeys.organizations.folders(organizationId),
          })
        },
        onError: () => toast.error(t("organization.files.folderCreateFailed")),
      },
    }
  )
}

export function useUpdateFolder(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionPatch<
    Folder,
    { folderId: string; name?: string; parent_folder_id?: string | null }
  >({
    url: ({ folderId }) => API.files(organizationId, "folders", folderId),
    options: {
      onSuccess: () => {
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.folders(organizationId),
        })
      },
      onError: () => toast.error(t("organization.files.folderUpdateFailed")),
    },
  })
}

export function useDeleteFolder(organizationId: string) {
  const queryClient = useQueryClient()
  const { t } = useTranslation()
  return useHTTPActionDelete<void, string>({
    url: (folderId) => API.files(organizationId, "folders", folderId),
    options: {
      onSuccess: () => {
        toast.success(t("organization.files.folderDeleted"))
        void queryClient.invalidateQueries({
          queryKey: queryKeys.organizations.folders(organizationId),
        })
      },
      onError: (err) => {
        const code = (err as { status?: { code?: string } })?.status?.code
        toast.error(
          code === "FOLDER_NOT_EMPTY"
            ? t("organization.files.folderNotEmpty")
            : t("organization.files.folderDeleteFailed")
        )
      },
    },
  })
}
