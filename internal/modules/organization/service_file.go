package organization

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/platform/apperr"
	"github.com/aasumitro/stratum/internal/platform/db"
)

var ErrStorageLimitReached = errors.New("storage limit reached")

// ErrFolderCycle guards updateFolder: moving a folder under one of its own
// descendants would make the folder its own ancestor, a cycle the
// parent_folder_id chain has no other way to detect or unwind.
var ErrFolderCycle = errors.New("cannot move a folder into its own subfolder")

// ErrFolderNotEmpty: deleting a folder is blocked while it still has
// live files or subfolders, so the ON DELETE CASCADE on
// folders.parent_folder_id never fires against non-empty content.
var ErrFolderNotEmpty = errors.New("folder is not empty")

// syncStorageUsage records the current total file bytes for organizationID
// as a fire-and-forget background op.
func (s *service) syncStorageUsage(ctx context.Context, organizationID string) {
	if s.billingWriter == nil {
		return
	}
	// See syncMemberUsage in service_member.go for why the querier must be
	// cleared before this context crosses into a background goroutine.
	ctx = db.WithoutQuerier(context.WithoutCancel(ctx))
	go func() {
		// Bounds the background write so a congested DB connection or a
		// hanging cross-module billing call can't leak this goroutine
		// indefinitely — context.WithoutCancel alone has no deadline.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		total, err := s.repo.sumStorageBytes(ctx, s.pool, organizationID)
		if err != nil {
			return
		}
		_ = s.billingWriter.RecordUsage(ctx, organizationID, "storage_bytes", total)
	}()
}

func (s *service) uploadLogo(ctx context.Context, organizationID string, r io.Reader, contentType string) (err error) {
	defer func() {
		if err != nil {
			err = apperr.Internal("LOGO_UPLOAD_FAILED", "failed to upload logo", err)
		}
	}()

	if s.store == nil {
		return fmt.Errorf("organization.uploadLogo: storage not configured")
	}
	path := organizationID + "/logo"
	if err := s.store.Upload(ctx, "organization", path, r, contentType); err != nil {
		return fmt.Errorf("organization.uploadLogo: %w", err)
	}
	logoURL := fmt.Sprintf("%s/storage/v1/object/public/organization/%s", strings.TrimSuffix(s.store.BaseURL(), "/"), path)
	return s.repo.updateLogoURL(ctx, s.pool, organizationID, logoURL)
}

func (s *service) uploadFile(
	ctx context.Context,
	organizationID, authSub, fileName, fileID string,
	folderID *string,
	r io.Reader,
	sizeBytes int64,
	contentType string,
) (rec *fileRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		rec = nil
		switch {
		case errors.Is(err, ErrStorageLimitReached):
			err = apperr.PaymentRequired("STORAGE_LIMIT_REACHED", "storage limit reached — upgrade your plan")
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("FOLDER_NOT_FOUND", "folder not found", err)
		default:
			err = apperr.Internal("FILE_UPLOAD_FAILED", "failed to upload file", err)
		}
	}()

	if s.store == nil {
		return nil, fmt.Errorf("organization.uploadFile: storage not configured")
	}
	if folderID != nil {
		if _, err := s.repo.findFolder(ctx, s.pool, organizationID, *folderID); err != nil {
			return nil, err
		}
	}

	// fileName is attacker-controlled (client-supplied multipart filename) —
	// never put it in the storage key. It's kept only in the name column
	// (via insertFile below) for display.
	path := fmt.Sprintf("%s/%s", organizationID, fileID)

	// The quota check and the file-row insert are wrapped in one transaction
	// holding a per-organization advisory lock, so two uploads racing for
	// the same organization serialize on this section instead of both
	// reading the same "current usage" before either's insert lands. This
	// doesn't make the check itself perfectly fresh — CheckUsageLimit reads
	// billing's asynchronously-synced usage counter (see syncStorageUsage
	// below), which can still lag a completed upload by a brief window —
	// but it closes the specific race of two simultaneous requests both
	// passing the same stale check.
	err = db.WithTx(ctx, s.pool, func(tx db.Querier) error {
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext($1))`, organizationID); err != nil {
			return err
		}
		if s.billingReader != nil {
			current, limit, err := s.billingReader.CheckUsageLimit(ctx, organizationID, "storage_bytes")
			if err == nil && limit >= 0 && current+sizeBytes > int64(limit) {
				return ErrStorageLimitReached
			}
		}
		if err := s.store.Upload(ctx, "organization-files", path, r, contentType); err != nil {
			return fmt.Errorf("organization.uploadFile: %w", err)
		}
		var mt *string
		if contentType != "" {
			mt = &contentType
		}
		var err error
		rec, err = s.repo.insertFile(ctx, tx, organizationID, folderID, fileName, path, sizeBytes, mt, authSub)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.syncStorageUsage(ctx, organizationID)
	return rec, nil
}

// listFiles fire-and-forgets an expired-trash sweep before listing — see
// maybePurgeExpiredTrash. folderID nil = root; search non-empty + searchAll
// widens the scope across every folder (the "search all" toggle).
func (s *service) listFiles(
	ctx context.Context, organizationID string, folderID *string,
	search string, searchAll bool, cursor string, limit int,
) ([]fileRecord, error) {
	s.maybePurgeExpiredTrash(ctx, organizationID)
	files, err := s.repo.listFiles(ctx, s.pool, organizationID, folderID, search, searchAll, cursor, limit)
	if err != nil {
		return nil, apperr.Internal("FILES_FETCH_FAILED", "failed to list files", err)
	}
	return files, nil
}

func (s *service) listTrash(ctx context.Context, organizationID string, limit int) ([]fileRecord, error) {
	s.maybePurgeExpiredTrash(ctx, organizationID)
	files, err := s.repo.listTrash(ctx, s.pool, organizationID, limit)
	if err != nil {
		return nil, apperr.Internal("TRASH_FETCH_FAILED", "failed to list trash", err)
	}
	return files, nil
}

func (s *service) fileDownloadURL(ctx context.Context, organizationID, fileID string) (url string, err error) {
	defer func() {
		if err == nil {
			return
		}
		url = ""
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperr.NotFound("FILE_NOT_FOUND", "file not found", err)
			return
		}
		err = apperr.Internal("FILE_DOWNLOAD_FAILED", "failed to generate download URL", err)
	}()

	if s.store == nil {
		return "", fmt.Errorf("organization.fileDownloadURL: storage not configured")
	}
	f, err := s.repo.findFile(ctx, s.pool, organizationID, fileID)
	if err != nil {
		return "", err
	}
	return s.store.SignedURL(ctx, "organization-files", f.Path, 5*time.Minute)
}

func (s *service) moveFile(ctx context.Context, organizationID, fileID string, folderID *string) (f *fileRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		f = nil
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperr.NotFound("FILE_NOT_FOUND", "file or target folder not found", err)
			return
		}
		err = apperr.Internal("FILE_MOVE_FAILED", "failed to move file", err)
	}()

	if folderID != nil {
		if _, err := s.repo.findFolder(ctx, s.pool, organizationID, *folderID); err != nil {
			return nil, err
		}
	}
	return s.repo.moveFile(ctx, s.pool, organizationID, fileID, folderID)
}

// deleteFile soft-deletes (30-day trash) — storage stays untouched until
// purgeFile or the expired-trash sweep actually removes the object.
func (s *service) deleteFile(ctx context.Context, organizationID, fileID string) (*fileRecord, error) {
	f, err := s.repo.softDeleteFile(ctx, s.pool, organizationID, fileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("FILE_NOT_FOUND", "file not found", err)
		}
		return nil, apperr.Internal("FILE_DELETE_FAILED", "failed to delete file", err)
	}
	s.syncStorageUsage(ctx, organizationID)
	return f, nil
}

// bulkDeleteFiles soft-deletes every fileID in one statement and returns how
// many actually changed (fewer than requested is not an error — see
// repository.bulkSoftDeleteFiles).
func (s *service) bulkDeleteFiles(ctx context.Context, organizationID string, fileIDs []string) (int, error) {
	rows, err := s.repo.bulkSoftDeleteFiles(ctx, s.pool, organizationID, fileIDs)
	if err != nil {
		return 0, apperr.Internal("FILES_DELETE_FAILED", "failed to delete files", err)
	}
	s.syncStorageUsage(ctx, organizationID)
	return len(rows), nil
}

func (s *service) restoreFile(ctx context.Context, organizationID, fileID string) (*fileRecord, error) {
	f, err := s.repo.restoreFile(ctx, s.pool, organizationID, fileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, apperr.NotFound("FILE_NOT_FOUND", "file not found in trash", err)
		}
		return nil, apperr.Internal("FILE_RESTORE_FAILED", "failed to restore file", err)
	}
	s.syncStorageUsage(ctx, organizationID)
	return f, nil
}

// purgeFile permanently removes a trashed file — the explicit "delete
// forever" action from the trash view.
func (s *service) purgeFile(ctx context.Context, organizationID, fileID string) error {
	f, err := s.repo.purgeFile(ctx, s.pool, organizationID, fileID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return apperr.NotFound("FILE_NOT_FOUND", "file not found in trash", err)
		}
		return apperr.Internal("FILE_PURGE_FAILED", "failed to permanently delete file", err)
	}
	if s.store != nil {
		_ = s.store.Delete(ctx, "organization-files", f.Path)
	}
	return nil
}

// maybePurgeExpiredTrash is a fire-and-forget best-effort sweep run
// opportunistically on every list/trash read, rather than a scheduled
// worker — this codebase has no cron-style periodic-sweep infrastructure
// today (RabbitMQ delayed messages are used for one-shot future events like
// dunning reminders, not recurring table sweeps), so adding one exclusively
// for a 30-day trash purge wasn't judged worth the new infra. Errors are
// silently dropped, same convention as syncStorageUsage/syncMemberUsage —
// a missed sweep just means slightly-stale trash until the next read.
func (s *service) maybePurgeExpiredTrash(ctx context.Context, organizationID string) {
	if s.store == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	go func() {
		expired, err := s.repo.purgeExpiredTrash(ctx, s.pool, organizationID)
		if err != nil {
			return
		}
		for _, f := range expired {
			_ = s.store.Delete(ctx, "organization-files", f.Path)
		}
	}()
}

// --- folders ---

func (s *service) createFolder(
	ctx context.Context, organizationID string,
	parentFolderID *string, name, authSub string,
) (f *folderRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		f = nil
		if errors.Is(err, pgx.ErrNoRows) {
			err = apperr.NotFound("FOLDER_NOT_FOUND", "parent folder not found", err)
			return
		}
		err = apperr.Internal("FOLDER_CREATE_FAILED", "failed to create folder", err)
	}()

	if parentFolderID != nil {
		if _, err := s.repo.findFolder(ctx, s.pool, organizationID, *parentFolderID); err != nil {
			return nil, err
		}
	}
	return s.repo.insertFolder(ctx, s.pool, organizationID, parentFolderID, name, authSub)
}

func (s *service) listFolders(ctx context.Context, organizationID string) ([]folderRecord, error) {
	folders, err := s.repo.listFolders(ctx, s.pool, organizationID)
	if err != nil {
		return nil, apperr.Internal("FOLDERS_FETCH_FAILED", "failed to list folders", err)
	}
	return folders, nil
}

func (s *service) updateFolder(
	ctx context.Context, organizationID, folderID string,
	name *string, parentFolderID *string, moveToParent bool,
) (f *folderRecord, err error) {
	defer func() {
		if err == nil {
			return
		}
		f = nil
		switch {
		case errors.Is(err, ErrFolderCycle):
			err = apperr.Validation("FOLDER_CYCLE", ErrFolderCycle.Error())
		case errors.Is(err, pgx.ErrNoRows):
			err = apperr.NotFound("FOLDER_NOT_FOUND", "folder not found", err)
		default:
			err = apperr.Internal("FOLDER_UPDATE_FAILED", "failed to update folder", err)
		}
	}()

	if moveToParent && parentFolderID != nil {
		if *parentFolderID == folderID {
			return nil, fmt.Errorf("organization.updateFolder: a folder cannot be its own parent")
		}
		if _, err := s.repo.findFolder(ctx, s.pool, organizationID, *parentFolderID); err != nil {
			return nil, err
		}
		isDescendant, err := s.repo.isDescendantOf(ctx, s.pool, organizationID, folderID, *parentFolderID)
		if err != nil {
			return nil, err
		}
		if isDescendant {
			return nil, ErrFolderCycle
		}
	}
	return s.repo.updateFolder(ctx, s.pool, organizationID, folderID, name, parentFolderID, moveToParent)
}

func (s *service) deleteFolder(ctx context.Context, organizationID, folderID string) (err error) {
	defer func() {
		if err == nil {
			return
		}
		if errors.Is(err, ErrFolderNotEmpty) {
			err = apperr.Conflict("FOLDER_NOT_EMPTY", "folder still has files or subfolders")
			return
		}
		err = apperr.Internal("FOLDER_DELETE_FAILED", "failed to delete folder", err)
	}()

	empty, err := s.repo.folderIsEmpty(ctx, s.pool, organizationID, folderID)
	if err != nil {
		return err
	}
	if !empty {
		return ErrFolderNotEmpty
	}
	return s.repo.deleteFolder(ctx, s.pool, organizationID, folderID)
}
