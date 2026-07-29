package organization

import (
	"context"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/db"
)

// --- folders ---

type folderRecord struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	ParentFolderID *string   `json:"parent_folder_id,omitempty"`
	Name           string    `json:"name"`
	CreatedBy      string    `json:"created_by"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

const folderColumns = "id, organization_id, parent_folder_id, name, created_by, created_at, updated_at"

func scanFolder(row interface{ Scan(dest ...any) error }, f *folderRecord) error {
	return row.Scan(&f.ID, &f.OrganizationID, &f.ParentFolderID, &f.Name, &f.CreatedBy, &f.CreatedAt, &f.UpdatedAt)
}

func (r *repository) insertFolder(ctx context.Context, q db.Querier, organizationID string, parentFolderID *string, name, createdBy string) (*folderRecord, error) {
	f := new(folderRecord)
	err := scanFolder(q.QueryRow(ctx, `
		INSERT INTO organization.folders (organization_id, parent_folder_id, name, created_by)
		VALUES ($1, $2, $3, $4)
		RETURNING `+folderColumns,
		organizationID, parentFolderID, name, createdBy,
	), f)
	if err != nil {
		return nil, fmt.Errorf("organization.insertFolder: %w", err)
	}
	return f, nil
}

// listFolders returns every folder in the organization — the frontend
// assembles the tree client-side from the flat parent_folder_id list
// (mirrors how listFeatures/listAddons hand the frontend a flat catalog).
func (r *repository) listFolders(ctx context.Context, q db.Querier, organizationID string) ([]folderRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT `+folderColumns+` FROM organization.folders
		WHERE organization_id = $1 ORDER BY name`,
		organizationID,
	)
	if err != nil {
		return nil, fmt.Errorf("organization.listFolders: %w", err)
	}
	defer rows.Close()

	out := []folderRecord{}
	for rows.Next() {
		var f folderRecord
		if err := scanFolder(rows, &f); err != nil {
			return nil, fmt.Errorf("organization.listFolders: scan: %w", err)
		}
		out = append(out, f)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("organization.listFolders: %w", err)
	}
	return out, nil
}

func (r *repository) findFolder(ctx context.Context, q db.Querier, organizationID, folderID string) (*folderRecord, error) {
	f := new(folderRecord)
	err := scanFolder(q.QueryRow(ctx, `
		SELECT `+folderColumns+` FROM organization.folders WHERE id = $1 AND organization_id = $2`,
		folderID, organizationID,
	), f)
	if err != nil {
		return nil, fmt.Errorf("organization.findFolder: %w", err)
	}
	return f, nil
}

func (r *repository) updateFolder(
	ctx context.Context, q db.Querier, organizationID, folderID string,
	name *string, parentFolderID *string, moveToParent bool,
) (*folderRecord, error) {
	f := new(folderRecord)
	var err error
	switch {
	case name != nil && moveToParent:
		err = scanFolder(q.QueryRow(ctx, `
			UPDATE organization.folders SET name = $3, parent_folder_id = $4, updated_at = now()
			WHERE id = $1 AND organization_id = $2 RETURNING `+folderColumns,
			folderID, organizationID, *name, parentFolderID,
		), f)
	case name != nil:
		err = scanFolder(q.QueryRow(ctx, `
			UPDATE organization.folders SET name = $3, updated_at = now()
			WHERE id = $1 AND organization_id = $2 RETURNING `+folderColumns,
			folderID, organizationID, *name,
		), f)
	case moveToParent:
		err = scanFolder(q.QueryRow(ctx, `
			UPDATE organization.folders SET parent_folder_id = $3, updated_at = now()
			WHERE id = $1 AND organization_id = $2 RETURNING `+folderColumns,
			folderID, organizationID, parentFolderID,
		), f)
	default:
		return r.findFolder(ctx, q, organizationID, folderID)
	}
	if err != nil {
		return nil, fmt.Errorf("organization.updateFolder: %w", err)
	}
	return f, nil
}

// isDescendantOf reports whether candidateParentID is folderID itself or one
// of folderID's descendants, by walking candidateParentID's ancestor chain
// up to the root — used to reject a move that would make a folder its own
// ancestor (a cycle the parent_folder_id chain has no other guard against).
func (r *repository) isDescendantOf(
	ctx context.Context, q db.Querier,
	organizationID, folderID, candidateParentID string,
) (bool, error) {
	var exists bool
	err := q.QueryRow(ctx, `
		WITH RECURSIVE ancestors AS (
			SELECT id, parent_folder_id FROM organization.folders
			WHERE id = $2 AND organization_id = $1
			UNION ALL
			SELECT f.id, f.parent_folder_id FROM organization.folders f
			JOIN ancestors a ON f.id = a.parent_folder_id
			WHERE f.organization_id = $1
		)
		SELECT EXISTS(SELECT 1 FROM ancestors WHERE id = $3)`,
		organizationID, candidateParentID, folderID,
	).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("organization.isDescendantOf: %w", err)
	}
	return exists, nil
}

// folderIsEmpty reports whether folderID has no live files and no
// subfolders — deleteFolder refuses to cascade-delete a non-empty folder.
func (r *repository) folderIsEmpty(ctx context.Context, q db.Querier, organizationID, folderID string) (bool, error) {
	var hasFiles, hasSubfolders bool
	if err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM organization.files WHERE organization_id = $1 AND folder_id = $2 AND deleted_at IS NULL)`,
		organizationID, folderID,
	).Scan(&hasFiles); err != nil {
		return false, fmt.Errorf("organization.folderIsEmpty: %w", err)
	}
	if err := q.QueryRow(ctx,
		`SELECT EXISTS(SELECT 1 FROM organization.folders WHERE organization_id = $1 AND parent_folder_id = $2)`,
		organizationID, folderID,
	).Scan(&hasSubfolders); err != nil {
		return false, fmt.Errorf("organization.folderIsEmpty: %w", err)
	}
	return !hasFiles && !hasSubfolders, nil
}

func (r *repository) deleteFolder(ctx context.Context, q db.Querier, organizationID, folderID string) error {
	if _, err := q.Exec(ctx, `DELETE FROM organization.folders WHERE id = $1 AND organization_id = $2`, folderID, organizationID); err != nil {
		return fmt.Errorf("organization.deleteFolder: %w", err)
	}
	return nil
}
