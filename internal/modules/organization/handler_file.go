package organization

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/reqctx"
	"github.com/aasumitro/stratum/internal/platform/httpserver/request"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
)

// uploadLogo godoc
// @Summary      Upload organization logo
// @Description  Admin/owner only. Max 2MB, image/* content type.
// @Tags         organization
// @Accept       multipart/form-data
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        logo            formData  file    true  "Logo image file"
// @Success      204             "no content"
// @Failure      422             {object}  response.Payload  "missing file, too large, or not an image"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/logo [post]
func (h *handler) uploadLogo(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	file, header, err := c.Request.FormFile("logo")
	if err != nil {
		response.Error("LOGO_MISSING", "logo file is required").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	defer file.Close()

	if header.Size > maxLogoSize {
		response.Error("LOGO_TOO_LARGE", "logo must be under 2 MB").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	ct, ok := request.SniffImageType(file)
	if !ok {
		response.Error("LOGO_INVALID_TYPE", "logo must be an image").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	if err := h.svc.uploadLogo(c.Request.Context(), ws.ID, file, ct); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// uploadFile godoc
// @Summary      Upload a file
// @Description  Admin/owner only. Max 50MB.
// @Tags         organization
// @Accept       multipart/form-data
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true   "Organization ID"
// @Param        file            formData  file    true   "File to upload"
// @Param        folder_id       formData  string  false  "Destination folder ID (root if omitted)"
// @Success      201             {object}  response.Payload{data=fileRecord}
// @Failure      422             {object}  response.Payload  "missing file or too large"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files [post]
func (h *handler) uploadFile(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		response.Error("FILE_MISSING", "file is required").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	defer file.Close()

	if header.Size > maxFileSize {
		response.Error("FILE_TOO_LARGE", "file must be under 50 MB").JSON(c, http.StatusUnprocessableEntity)
		return
	}
	if header.Filename == "" || len(header.Filename) > 255 {
		response.Error("FILE_NAME_INVALID", "file name must be between 1 and 255 characters").JSON(c, http.StatusUnprocessableEntity)
		return
	}

	fileID := uuid.New().String()
	ct := header.Header.Get("Content-Type")
	if ct == "" {
		ct = "application/octet-stream"
	}

	var folderID *string
	if v := c.PostForm("folder_id"); v != "" {
		folderID = &v
	}

	f, err := h.svc.uploadFile(
		c.Request.Context(),
		ws.ID, reqctx.Subject(c), header.Filename, fileID, folderID,
		file, header.Size, ct,
	)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(f).JSON(c, http.StatusCreated)
}

// listFiles godoc
// @Summary      List files
// @Description  Cursor-paginated file listing, optionally scoped to a folder or filtered by search. Admin/owner only.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string   true   "Organization ID"
// @Param        folder_id       query     string   false  "filter by folder"
// @Param        search          query     string   false  "filter by name"
// @Param        search_all      query     boolean  false  "search across all folders instead of just folder_id"
// @Param        cursor          query     string   false  "pagination cursor from a previous response"
// @Param        limit           query     int      false  "max results (default 20, max 100)"
// @Success      200             {object}  response.Payload{data=object{items=[]fileRecord,next_cursor=string}}
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files [get]
func (h *handler) listFiles(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	cursor := c.Query("cursor")
	limit := 20
	if l := c.Query("limit"); l != "" {
		if v, err := strconv.Atoi(l); err == nil && v > 0 && v <= 100 {
			limit = v
		}
	}
	var folderID *string
	if v := c.Query("folder_id"); v != "" {
		folderID = &v
	}
	search := c.Query("search")
	searchAll := c.Query("search_all") == "true"

	files, err := h.svc.listFiles(c.Request.Context(), ws.ID, folderID, search, searchAll, cursor, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}

	var nextCursor string
	if len(files) == limit {
		nextCursor = files[len(files)-1].ID
	}
	response.Success(map[string]any{"items": files, "next_cursor": nextCursor}).JSON(c, http.StatusOK)
}

// listTrash godoc
// @Summary      List deleted files
// @Description  Returns up to the 50 most recently soft-deleted files. Admin/owner only.
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]fileRecord}
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/trash [get]
func (h *handler) listTrash(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	limit := 50

	files, err := h.svc.listTrash(c.Request.Context(), ws.ID, limit)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(files).JSON(c, http.StatusOK)
}

// downloadFile godoc
// @Summary      Download a file
// @Description  Redirects to a short-lived signed download URL. Admin/owner only.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        fileID          path  string  true  "File ID"
// @Success      302             "redirect to signed URL"
// @Failure      404             {object}  response.Payload  "file not found"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/{fileID}/download [get]
func (h *handler) downloadFile(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	fileID := c.Param("fileID")

	signedURL, err := h.svc.fileDownloadURL(c.Request.Context(), ws.ID, fileID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	c.Redirect(http.StatusFound, signedURL)
}

type moveFileRequest struct {
	FolderID *string `json:"folder_id"`
}

// moveFile godoc
// @Summary      Move a file
// @Description  Changes a file's folder (null moves it to root). Admin/owner only.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string           true  "Organization ID"
// @Param        fileID          path      string           true  "File ID"
// @Param        body            body      moveFileRequest  true  "Destination folder"
// @Success      200             {object}  response.Payload{data=fileRecord}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/{fileID} [patch]
func (h *handler) moveFile(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	fileID := c.Param("fileID")

	var req moveFileRequest
	if !request.Bind(c, &req) {
		return
	}

	f, err := h.svc.moveFile(c.Request.Context(), ws.ID, fileID, req.FolderID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(f).JSON(c, http.StatusOK)
}

// deleteOrganizationFile godoc
// @Summary      Soft-delete a file
// @Description  Moves a file to trash. Admin/owner only.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        fileID          path  string  true  "File ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/{fileID} [delete]
func (h *handler) deleteOrganizationFile(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	fileID := c.Param("fileID")

	if _, err := h.svc.deleteFile(c.Request.Context(), ws.ID, fileID); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

type bulkDeleteFilesRequest struct {
	FileIDs []string `json:"file_ids" binding:"required,min=1"`
}

// bulkDeleteFiles godoc
// @Summary      Bulk soft-delete files
// @Description  Admin/owner only.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string                  true  "Organization ID"
// @Param        body            body      bulkDeleteFilesRequest  true  "File IDs to delete"
// @Success      200             {object}  response.Payload{data=object{deleted_count=integer}}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/bulk-delete [post]
func (h *handler) bulkDeleteFiles(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req bulkDeleteFilesRequest
	if !request.Bind(c, &req) {
		return
	}

	deleted, err := h.svc.bulkDeleteFiles(c.Request.Context(), ws.ID, req.FileIDs)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(gin.H{"deleted_count": deleted}).JSON(c, http.StatusOK)
}

// restoreFile godoc
// @Summary      Restore a file from trash
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        fileID          path      string  true  "File ID"
// @Success      200             {object}  response.Payload{data=fileRecord}
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/{fileID}/restore [post]
func (h *handler) restoreFile(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	fileID := c.Param("fileID")

	f, err := h.svc.restoreFile(c.Request.Context(), ws.ID, fileID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(f).JSON(c, http.StatusOK)
}

// purgeFile godoc
// @Summary      Permanently delete a file
// @Description  Irreversibly deletes a soft-deleted file and its stored object. Admin/owner only.
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        fileID          path  string  true  "File ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/{fileID}/permanent [delete]
func (h *handler) purgeFile(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	fileID := c.Param("fileID")

	if err := h.svc.purgeFile(c.Request.Context(), ws.ID, fileID); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// --- folders ---

type createFolderRequest struct {
	Name           string  `json:"name" binding:"required,min=1,max=255"`
	ParentFolderID *string `json:"parent_folder_id"`
}

// createFolder godoc
// @Summary      Create a folder
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string               true  "Organization ID"
// @Param        body            body      createFolderRequest  true  "Folder fields"
// @Success      201             {object}  response.Payload{data=folderRecord}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/folders [post]
func (h *handler) createFolder(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	var req createFolderRequest
	if !request.Bind(c, &req) {
		return
	}

	f, err := h.svc.createFolder(c.Request.Context(), ws.ID, req.ParentFolderID, req.Name, reqctx.Subject(c))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(f).JSON(c, http.StatusCreated)
}

// listFolders godoc
// @Summary      List folders
// @Tags         organization
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]folderRecord}
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/folders [get]
func (h *handler) listFolders(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	folders, err := h.svc.listFolders(c.Request.Context(), ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(folders).JSON(c, http.StatusOK)
}

type updateFolderRequest struct {
	Name *string `json:"name"`
	// ParentFolderID is only applied when explicitly present in the JSON
	// body — moveToParent distinguishes "move to root" (field present,
	// null) from "don't touch the parent" (field absent entirely).
	ParentFolderID *string `json:"parent_folder_id"`
	MoveToParent   bool    `json:"-"`
}

// updateFolder godoc
// @Summary      Update a folder
// @Description  Rename and/or move a folder. parent_folder_id is only applied when the field is explicitly present in the body (null moves to root; omitted leaves the parent unchanged). Admin/owner only.
// @Tags         organization
// @Accept       json
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string               true  "Organization ID"
// @Param        folderID        path      string               true  "Folder ID"
// @Param        body            body      updateFolderRequest  true  "Fields to update"
// @Success      200             {object}  response.Payload{data=folderRecord}
// @Failure      422             {object}  response.Payload  "validation failed"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/folders/{folderID} [patch]
func (h *handler) updateFolder(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	folderID := c.Param("folderID")

	var raw map[string]json.RawMessage
	if err := c.ShouldBindJSON(&raw); err != nil {
		request.ValidationError(err).JSON(c, http.StatusUnprocessableEntity)
		return
	}
	var req updateFolderRequest
	if v, ok := raw["name"]; ok {
		_ = json.Unmarshal(v, &req.Name)
	}
	if v, ok := raw["parent_folder_id"]; ok {
		req.MoveToParent = true
		_ = json.Unmarshal(v, &req.ParentFolderID)
	}

	f, err := h.svc.updateFolder(c.Request.Context(), ws.ID, folderID, req.Name, req.ParentFolderID, req.MoveToParent)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(f).JSON(c, http.StatusOK)
}

// deleteFolder godoc
// @Summary      Delete a folder
// @Tags         organization
// @Security     BearerAuth
// @Param        organizationID  path  string  true  "Organization ID"
// @Param        folderID        path  string  true  "Folder ID"
// @Success      204             "no content"
// @Failure      403             {object}  response.Payload  "admin role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/files/folders/{folderID} [delete]
func (h *handler) deleteFolder(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	folderID := c.Param("folderID")

	if err := h.svc.deleteFolder(c.Request.Context(), ws.ID, folderID); err != nil {
		response.FromError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
