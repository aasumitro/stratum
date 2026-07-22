package organization

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

// --- file records ---

const fileColumns = "id, organization_id, folder_id, name, path, size_bytes, mime_type, created_by, deleted_at, created_at"

type fileRecord struct {
	ID             string     `json:"id"`
	OrganizationID string     `json:"organization_id"`
	FolderID       *string    `json:"folder_id,omitempty"`
	Name           string     `json:"name"`
	Path           string     `json:"path"`
	SizeBytes      int64      `json:"size_bytes"`
	MimeType       *string    `json:"mime_type,omitempty"`
	CreatedBy      string     `json:"created_by"`
	DeletedAt      *time.Time `json:"deleted_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
}

func scanFile(row interface{ Scan(dest ...any) error }, f *fileRecord) error {
	return row.Scan(&f.ID, &f.OrganizationID, &f.FolderID, &f.Name, &f.Path, &f.SizeBytes, &f.MimeType, &f.CreatedBy, &f.DeletedAt, &f.CreatedAt)
}

func (r *repository) insertFile(
	ctx context.Context, q db.Querier,
	organizationID string, folderID *string, name, path string,
	sizeBytes int64,
	mimeType *string,
	createdBy string,
) (*fileRecord, error) {
	f := new(fileRecord)
	err := scanFile(q.QueryRow(ctx, `
		INSERT INTO organization.files (organization_id, folder_id, name, path, size_bytes, mime_type, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING `+fileColumns,
		organizationID, folderID, name, path, sizeBytes, mimeType, createdBy,
	), f)
	return f, err
}

// listFiles scopes to a single folder by default (folderID nil = root).
// search, when non-empty, ILIKE-matches the file name; searchAll widens the
// scope to every folder in the organization instead of just folderID (the
// "search all" toggle) — folderID is ignored in that case.
func (r *repository) listFiles(
	ctx context.Context, q db.Querier,
	organizationID string, folderID *string, search string, searchAll bool,
	cursor string, limit int,
) ([]fileRecord, error) {
	conds := []string{"organization_id = $1", "deleted_at IS NULL"}
	args := db.NewArgs(organizationID)

	if !searchAll {
		if folderID != nil {
			conds = append(conds, fmt.Sprintf("folder_id = $%d", args.Add(*folderID)))
		} else {
			conds = append(conds, "folder_id IS NULL")
		}
	}
	if search != "" {
		conds = append(conds, fmt.Sprintf("name ILIKE $%d", args.Add("%"+search+"%")))
	}
	if cursor != "" {
		conds = append(conds, fmt.Sprintf("id < $%d", args.Add(cursor)))
	}

	query := `SELECT ` + fileColumns + ` FROM organization.files WHERE ` +
		strings.Join(conds, " AND ") + fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", args.Add(limit))

	rows, err := q.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []fileRecord{}
	for rows.Next() {
		var f fileRecord
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// listTrash returns soft-deleted files not yet purged, newest-deleted first.
func (r *repository) listTrash(ctx context.Context, q db.Querier, organizationID string, limit int) ([]fileRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT `+fileColumns+`
		FROM organization.files
		WHERE organization_id = $1 AND deleted_at IS NOT NULL
		ORDER BY deleted_at DESC LIMIT $2`,
		organizationID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []fileRecord{}
	for rows.Next() {
		var f fileRecord
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (r *repository) findFile(ctx context.Context, q db.Querier, organizationID, fileID string) (*fileRecord, error) {
	f := new(fileRecord)
	err := scanFile(q.QueryRow(ctx, `
		SELECT `+fileColumns+`
		FROM organization.files WHERE id = $1 AND organization_id = $2`,
		fileID, organizationID,
	), f)
	return f, err
}

// moveFile reassigns a file's folder (nil = move to root). Only affects
// live (non-deleted) files.
func (r *repository) moveFile(ctx context.Context, q db.Querier, organizationID, fileID string, folderID *string) (*fileRecord, error) {
	f := new(fileRecord)
	err := scanFile(q.QueryRow(ctx, `
		UPDATE organization.files SET folder_id = $3
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING `+fileColumns,
		fileID, organizationID, folderID,
	), f)
	return f, err
}

// softDeleteFile marks a file trashed (30-day recoverable delete) — storage
// is untouched until purgeExpiredTrash actually removes the object.
func (r *repository) softDeleteFile(ctx context.Context, q db.Querier, organizationID, fileID string) (*fileRecord, error) {
	f := new(fileRecord)
	err := scanFile(q.QueryRow(ctx, `
		UPDATE organization.files SET deleted_at = now()
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NULL
		RETURNING `+fileColumns,
		fileID, organizationID,
	), f)
	return f, err
}

// bulkSoftDeleteFiles trashes every fileID that belongs to organizationID
// and isn't already deleted; IDs that don't match (wrong org, already
// trashed, don't exist) are silently skipped rather than erroring the
// whole batch — the caller reports which ones actually changed.
func (r *repository) bulkSoftDeleteFiles(ctx context.Context, q db.Querier, organizationID string, fileIDs []string) ([]fileRecord, error) {
	rows, err := q.Query(ctx, `
		UPDATE organization.files SET deleted_at = now()
		WHERE organization_id = $1 AND id = ANY($2) AND deleted_at IS NULL
		RETURNING `+fileColumns,
		organizationID, fileIDs,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []fileRecord
	for rows.Next() {
		var f fileRecord
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// restoreFile clears a trashed file's deleted_at, moving it back to root
// (not its original folder — the folder may have been deleted or moved
// since; restoring to root is the safe default, same as most file managers).
func (r *repository) restoreFile(ctx context.Context, q db.Querier, organizationID, fileID string) (*fileRecord, error) {
	f := new(fileRecord)
	err := scanFile(q.QueryRow(ctx, `
		UPDATE organization.files SET deleted_at = NULL, folder_id = NULL
		WHERE id = $1 AND organization_id = $2 AND deleted_at IS NOT NULL
		RETURNING `+fileColumns,
		fileID, organizationID,
	), f)
	return f, err
}

// purgeFile permanently removes a single trashed file's DB row — used both
// by the explicit "delete forever" action and by purgeExpiredTrash's sweep.
func (r *repository) purgeFile(ctx context.Context, q db.Querier, organizationID, fileID string) (*fileRecord, error) {
	f := new(fileRecord)
	err := scanFile(q.QueryRow(ctx, `
		DELETE FROM organization.files WHERE id = $1 AND organization_id = $2 AND deleted_at IS NOT NULL
		RETURNING `+fileColumns,
		fileID, organizationID,
	), f)
	return f, err
}

// purgeExpiredTrash hard-deletes every file trashed more than 30 days ago
// and returns their storage paths so the caller can wipe the objects too.
// Called opportunistically (fire-and-forget) rather than via a scheduled
// worker — see service.go's maybePurgeExpiredTrash.
func (r *repository) purgeExpiredTrash(ctx context.Context, q db.Querier, organizationID string) ([]fileRecord, error) {
	rows, err := q.Query(ctx, `
		DELETE FROM organization.files
		WHERE organization_id = $1 AND deleted_at IS NOT NULL AND deleted_at < now() - interval '30 days'
		RETURNING `+fileColumns,
		organizationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []fileRecord
	for rows.Next() {
		var f fileRecord
		if err := scanFile(rows, &f); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

// deleteAllFilesForOrganization removes all file records (live and trashed)
// for an organization and returns their storage paths so the caller can
// wipe the objects from the storage bucket.
func (r *repository) deleteAllFilesForOrganization(ctx context.Context, q db.Querier, organizationID string) ([]string, error) {
	rows, err := q.Query(ctx, `
		DELETE FROM organization.files WHERE organization_id = $1 RETURNING path`,
		organizationID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			return nil, err
		}
		paths = append(paths, p)
	}
	return paths, rows.Err()
}

func (r *repository) updateLogoURL(ctx context.Context, q db.Querier, organizationID, logoURL string) error {
	logoJSON, _ := json.Marshal(map[string]string{"logo_url": logoURL})
	_, err := q.Exec(ctx, `
		UPDATE organization.organizations SET settings = settings || $2::jsonb, updated_at = now() WHERE id = $1`,
		organizationID, string(logoJSON),
	)
	return err
}

// sumStorageBytes only counts live files — a trashed file frees quota
// immediately ("delete frees space, recoverable for 30 days").
func (r *repository) sumStorageBytes(ctx context.Context, q db.Querier, organizationID string) (int64, error) {
	var total int64
	err := q.QueryRow(ctx,
		`SELECT COALESCE(SUM(size_bytes), 0) FROM organization.files WHERE organization_id = $1 AND deleted_at IS NULL`,
		organizationID,
	).Scan(&total)
	return total, err
}
