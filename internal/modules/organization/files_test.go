package organization_test

// Integration tests for files (folders, move, bulk-delete, 30-day
// trash). Actual object upload needs a real Supabase storage backend not
// available in this environment (same documented limitation as everywhere
// else in this project) — uploadFile itself is therefore not exercised
// here. Every other file row is seeded directly via SQL (mirrors this
// project's seedInvoice-style pattern for bypassing an unavailable
// dependency), then tested through the real HTTP handlers, which is where
// all the actual logic (folder scoping, soft-delete, trash,
// restore, purge, bulk operations) lives.

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/platform/httpserver"
)

const filesTestOrgSlugPrefix = "integ-ws-files-"

func filesURL(orgID string, parts ...string) string {
	url := "/api/organizations/" + orgID + "/files"
	if len(parts) > 0 {
		url += "/" + strings.Join(parts, "/")
	}
	return url
}

func TestIntegration_Folders_CreateListRenameMove(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`folders","name":"Files Folders WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// create root folder "Design"
	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "folders"), `{"name":"Design"}`))
	if wCreate.Code != http.StatusCreated {
		t.Fatalf("create folder: want 201, got %d: %s", wCreate.Code, wCreate.Body)
	}
	var createResp map[string]any
	json.NewDecoder(wCreate.Body).Decode(&createResp)
	designID := createResp["data"].(map[string]any)["id"].(string)

	// create nested subfolder "Logos" under Design
	wSub := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "folders"), `{"name":"Logos","parent_folder_id":"`+designID+`"}`))
	if wSub.Code != http.StatusCreated {
		t.Fatalf("create subfolder: want 201, got %d: %s", wSub.Code, wSub.Body)
	}
	var subResp map[string]any
	json.NewDecoder(wSub.Body).Decode(&subResp)
	logosID := subResp["data"].(map[string]any)["id"].(string)

	// list folders — expect both, with correct parent linkage
	wList := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID, "folders"), ""))
	if wList.Code != http.StatusOK {
		t.Fatalf("list folders: want 200, got %d", wList.Code)
	}
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	folders := listResp["data"].([]any)
	if len(folders) != 2 {
		t.Fatalf("want 2 folders, got %d", len(folders))
	}
	found := map[string]string{}
	for _, f := range folders {
		m := f.(map[string]any)
		parent, _ := m["parent_folder_id"].(string)
		found[m["id"].(string)] = parent
	}
	if found[logosID] != designID {
		t.Errorf("Logos.parent_folder_id: want %q, got %q", designID, found[logosID])
	}

	// rename Logos -> Brand Logos
	wRename := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, filesURL(orgID, "folders", logosID), `{"name":"Brand Logos"}`))
	if wRename.Code != http.StatusOK {
		t.Fatalf("rename folder: want 200, got %d: %s", wRename.Code, wRename.Body)
	}
	var renameResp map[string]any
	json.NewDecoder(wRename.Body).Decode(&renameResp)
	if renameResp["data"].(map[string]any)["name"] != "Brand Logos" {
		t.Errorf("want renamed folder name = Brand Logos, got %v", renameResp["data"].(map[string]any)["name"])
	}

	// move Brand Logos to root (parent_folder_id: null)
	wMove := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, filesURL(orgID, "folders", logosID), `{"parent_folder_id":null}`))
	if wMove.Code != http.StatusOK {
		t.Fatalf("move folder to root: want 200, got %d: %s", wMove.Code, wMove.Body)
	}
	var moveResp map[string]any
	json.NewDecoder(wMove.Body).Decode(&moveResp)
	if moveResp["data"].(map[string]any)["parent_folder_id"] != nil {
		t.Errorf("want parent_folder_id cleared, got %v", moveResp["data"].(map[string]any)["parent_folder_id"])
	}

	// delete empty Design folder — should succeed now that Logos moved out
	wDel := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, filesURL(orgID, "folders", designID), ""))
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("delete empty folder: want 204, got %d: %s", wDel.Code, wDel.Body)
	}
}

// TestIntegration_UpdateFolder_MoveUnderDescendant_Rejected regression-tests
// that moving a folder under one of its own descendants is rejected — the
// only prior guard checked parentFolderID == folderID (the immediate,
// one-level case), which let a multi-level cycle (A -> B -> C, then move A
// under C) slip through and orphan the whole subtree from the tree root.
func TestIntegration_UpdateFolder_MoveUnderDescendant_Rejected(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`cycle","name":"Files Cycle WS","plan":"solo","cycle":"monthly"}`))
	if w.Code != http.StatusCreated {
		t.Fatalf("setup: want 201, got %d", w.Code)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	// A -> B -> C
	aID := createTestFolder(t, pool, orgID, "A", "")
	bID := createTestFolder(t, pool, orgID, "B", aID)
	cID := createTestFolder(t, pool, orgID, "C", bID)

	// Move A under C — C is A's grandchild, so this would create a cycle.
	wMove := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, filesURL(orgID, "folders", aID), `{"parent_folder_id":"`+cID+`"}`))
	if wMove.Code != http.StatusUnprocessableEntity {
		t.Fatalf("move A under its own descendant C: want 422, got %d: %s", wMove.Code, wMove.Body)
	}

	// The tree must be untouched — A still has no parent.
	wList := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID, "folders"), ""))
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	for _, f := range listResp["data"].([]any) {
		m := f.(map[string]any)
		if m["id"] == aID && m["parent_folder_id"] != nil {
			t.Errorf("A.parent_folder_id changed despite the rejected move: %v", m["parent_folder_id"])
		}
	}
}

// createTestFolder creates a folder via the real HTTP handler and returns its
// ID — parentID empty means root.
func createTestFolder(t *testing.T, pool *pgxpool.Pool, orgID, name, parentID string) string {
	t.Helper()
	body := `{"name":"` + name + `"}`
	if parentID != "" {
		body = `{"name":"` + name + `","parent_folder_id":"` + parentID + `"}`
	}
	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "folders"), body))
	if w.Code != http.StatusCreated {
		t.Fatalf("create folder %q: want 201, got %d: %s", name, w.Code, w.Body)
	}
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	return resp["data"].(map[string]any)["id"].(string)
}

func TestIntegration_DeleteFolder_NotEmpty_Rejected(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`nonempty","name":"Files NonEmpty WS","plan":"solo","cycle":"monthly"}`))
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "folders"), `{"name":"Parent"}`))
	var createResp map[string]any
	json.NewDecoder(wCreate.Body).Decode(&createResp)
	parentID := createResp["data"].(map[string]any)["id"].(string)

	// seed a subfolder to make Parent non-empty
	serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "folders"), `{"name":"Child","parent_folder_id":"`+parentID+`"}`))

	wDel := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, filesURL(orgID, "folders", parentID), ""))
	if wDel.Code != http.StatusConflict {
		t.Errorf("delete non-empty folder: want 409, got %d: %s", wDel.Code, wDel.Body)
	}
}

// seedFile inserts a live (non-deleted) file row directly, bypassing the
// storage upload this environment can't exercise (see file header comment).
func seedFile(t *testing.T, pool *pgxpool.Pool, orgID string, folderID *string, name string, sizeBytes int64) string {
	t.Helper()
	var id string
	err := pool.QueryRow(t.Context(), `
		INSERT INTO organization.files (organization_id, folder_id, name, path, size_bytes, mime_type, created_by)
		VALUES ($1, $2, $3, $4, $5, 'text/plain', $6)
		RETURNING id`,
		orgID, folderID, name, orgID+"/"+name, sizeBytes, testAuthSub,
	).Scan(&id)
	if err != nil {
		t.Fatalf("seedFile: %v", err)
	}
	return id
}

func TestIntegration_ListFiles_ScopedToFolder(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`scope","name":"Files Scope WS","plan":"solo","cycle":"monthly"}`))
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "folders"), `{"name":"Design"}`))
	var createResp map[string]any
	json.NewDecoder(wCreate.Body).Decode(&createResp)
	folderID := createResp["data"].(map[string]any)["id"].(string)

	seedFile(t, pool, orgID, nil, "root-file.txt", 100)
	seedFile(t, pool, orgID, &folderID, "in-folder.txt", 200)

	// root scope (no folder_id param) should only show root-file.txt
	wRoot := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID), ""))
	var rootResp map[string]any
	json.NewDecoder(wRoot.Body).Decode(&rootResp)
	rootItems := rootResp["data"].(map[string]any)["items"].([]any)
	if len(rootItems) != 1 || rootItems[0].(map[string]any)["name"] != "root-file.txt" {
		t.Errorf("root scope: want exactly [root-file.txt], got %+v", rootItems)
	}

	// folder scope
	wFolder := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID)+"?folder_id="+folderID, ""))
	var folderResp map[string]any
	json.NewDecoder(wFolder.Body).Decode(&folderResp)
	folderItems := folderResp["data"].(map[string]any)["items"].([]any)
	if len(folderItems) != 1 || folderItems[0].(map[string]any)["name"] != "in-folder.txt" {
		t.Errorf("folder scope: want exactly [in-folder.txt], got %+v", folderItems)
	}

	// search_all scope should see both regardless of folder
	wAll := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID)+"?search_all=true", ""))
	var allResp map[string]any
	json.NewDecoder(wAll.Body).Decode(&allResp)
	allItems := allResp["data"].(map[string]any)["items"].([]any)
	if len(allItems) != 2 {
		t.Errorf("search_all scope: want 2 items, got %d", len(allItems))
	}
}

func TestIntegration_MoveFile(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`move","name":"Files Move WS","plan":"solo","cycle":"monthly"}`))
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	wCreate := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "folders"), `{"name":"Target"}`))
	var createResp map[string]any
	json.NewDecoder(wCreate.Body).Decode(&createResp)
	targetFolderID := createResp["data"].(map[string]any)["id"].(string)

	fileID := seedFile(t, pool, orgID, nil, "movable.txt", 50)

	wMove := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPatch, filesURL(orgID, fileID), `{"folder_id":"`+targetFolderID+`"}`))
	if wMove.Code != http.StatusOK {
		t.Fatalf("move file: want 200, got %d: %s", wMove.Code, wMove.Body)
	}
	var moveResp map[string]any
	json.NewDecoder(wMove.Body).Decode(&moveResp)
	if moveResp["data"].(map[string]any)["folder_id"] != targetFolderID {
		t.Errorf("want folder_id=%q after move, got %v", targetFolderID, moveResp["data"].(map[string]any)["folder_id"])
	}
}

func TestIntegration_DeleteFile_TrashRestorePurge(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`trash","name":"Files Trash WS","plan":"solo","cycle":"monthly"}`))
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	fileID := seedFile(t, pool, orgID, nil, "doomed.txt", 999)

	// soft delete
	wDel := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, filesURL(orgID, fileID), ""))
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("soft delete: want 204, got %d: %s", wDel.Code, wDel.Body)
	}

	// gone from the live list
	wList := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID), ""))
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	if items := listResp["data"].(map[string]any)["items"].([]any); len(items) != 0 {
		t.Errorf("want soft-deleted file excluded from live list, got %+v", items)
	}

	// present in trash
	wTrash := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID, "trash"), ""))
	if wTrash.Code != http.StatusOK {
		t.Fatalf("list trash: want 200, got %d", wTrash.Code)
	}
	var trashResp map[string]any
	json.NewDecoder(wTrash.Body).Decode(&trashResp)
	trashItems := trashResp["data"].([]any)
	if len(trashItems) != 1 || trashItems[0].(map[string]any)["name"] != "doomed.txt" {
		t.Fatalf("want [doomed.txt] in trash, got %+v", trashItems)
	}

	// restore
	wRestore := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, fileID, "restore"), ""))
	if wRestore.Code != http.StatusOK {
		t.Fatalf("restore: want 200, got %d: %s", wRestore.Code, wRestore.Body)
	}

	// back in the live list
	wList2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID), ""))
	var listResp2 map[string]any
	json.NewDecoder(wList2.Body).Decode(&listResp2)
	if items := listResp2["data"].(map[string]any)["items"].([]any); len(items) != 1 {
		t.Errorf("want restored file back in live list, got %+v", items)
	}

	// delete again, then purge forever
	serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, filesURL(orgID, fileID), ""))
	wPurge := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, filesURL(orgID, fileID, "permanent"), ""))
	if wPurge.Code != http.StatusNoContent {
		t.Fatalf("purge: want 204, got %d: %s", wPurge.Code, wPurge.Body)
	}

	// gone from trash too now
	wTrash2 := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID, "trash"), ""))
	var trashResp2 map[string]any
	json.NewDecoder(wTrash2.Body).Decode(&trashResp2)
	if items := trashResp2["data"].([]any); len(items) != 0 {
		t.Errorf("want trash empty after purge, got %+v", items)
	}
}

func TestIntegration_BulkDeleteFiles(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`bulk","name":"Files Bulk WS","plan":"solo","cycle":"monthly"}`))
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	id1 := seedFile(t, pool, orgID, nil, "a.txt", 10)
	id2 := seedFile(t, pool, orgID, nil, "b.txt", 20)
	seedFile(t, pool, orgID, nil, "c.txt", 30) // left alone

	wBulk := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, filesURL(orgID, "bulk-delete"),
		`{"file_ids":["`+id1+`","`+id2+`"]}`))
	if wBulk.Code != http.StatusOK {
		t.Fatalf("bulk delete: want 200, got %d: %s", wBulk.Code, wBulk.Body)
	}
	var bulkResp map[string]any
	json.NewDecoder(wBulk.Body).Decode(&bulkResp)
	if int(bulkResp["data"].(map[string]any)["deleted_count"].(float64)) != 2 {
		t.Errorf("want deleted_count=2, got %v", bulkResp["data"])
	}

	wList := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodGet, filesURL(orgID), ""))
	var listResp map[string]any
	json.NewDecoder(wList.Body).Decode(&listResp)
	items := listResp["data"].(map[string]any)["items"].([]any)
	if len(items) != 1 || items[0].(map[string]any)["name"] != "c.txt" {
		t.Errorf("want only c.txt left live, got %+v", items)
	}
}

func TestIntegration_SumStorageBytes_ExcludesDeletedFiles(t *testing.T) {
	pool := testPool(t)

	var orgID string
	t.Cleanup(func() {
		if orgID != "" {
			pool.Exec(context.Background(), `DELETE FROM organization.organizations WHERE id = $1`, orgID)
		}
	})

	w := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodPost, "/api/organizations", `{"slug":"`+filesTestOrgSlugPrefix+`quota","name":"Files Quota WS","plan":"solo","cycle":"monthly"}`))
	var resp map[string]any
	json.NewDecoder(w.Body).Decode(&resp)
	orgID = resp["data"].(map[string]any)["id"].(string)

	keepID := seedFile(t, pool, orgID, nil, "keep.txt", 1000)
	seedFile(t, pool, orgID, nil, "trash-me.txt", 2000)

	// sanity: this query used to reference a nonexistent "file_size" column
	// and would error every time — confirm it now actually runs and sums
	// correctly, live + deleted split.
	var totalBeforeDelete int64
	if err := pool.QueryRow(t.Context(),
		`SELECT COALESCE(SUM(size_bytes), 0) FROM organization.files WHERE organization_id = $1 AND deleted_at IS NULL`,
		orgID,
	).Scan(&totalBeforeDelete); err != nil {
		t.Fatalf("sumStorageBytes query: %v", err)
	}
	if totalBeforeDelete != 3000 {
		t.Fatalf("before delete: want total=3000, got %d", totalBeforeDelete)
	}

	// soft-delete one file — quota should drop immediately
	wDel := serveWS(t, pool, httpserver.JSONTestRequest(http.MethodDelete, filesURL(orgID)+"/"+mustFindFileIDByName(t, pool, orgID, "trash-me.txt"), ""))
	if wDel.Code != http.StatusNoContent {
		t.Fatalf("soft delete: want 204, got %d", wDel.Code)
	}

	var totalAfterDelete int64
	pool.QueryRow(t.Context(),
		`SELECT COALESCE(SUM(size_bytes), 0) FROM organization.files WHERE organization_id = $1 AND deleted_at IS NULL`,
		orgID,
	).Scan(&totalAfterDelete)
	if totalAfterDelete != 1000 {
		t.Errorf("after delete: want total=1000 (keep.txt only), got %d", totalAfterDelete)
	}
	_ = keepID
}

// TestIntegration_StorageAdvisoryLock_SerializesSameOrganization regression-
// tests the primitive uploadFile's quota-check race fix depends on:
// pg_advisory_xact_lock(hashtext(organizationID)) must block a second
// transaction taking the same key until the first releases it (commit or
// rollback), and release promptly once it does. uploadFile itself can't be
// exercised here (no real storage backend in this environment, see the
// file-level doc comment above), so this verifies the lock mechanism
// directly instead.
func TestIntegration_StorageAdvisoryLock_SerializesSameOrganization(t *testing.T) {
	pool := testPool(t)
	const orgKey = "lock-test-org-f01"

	tx1, err := pool.Begin(t.Context())
	if err != nil {
		t.Fatalf("begin tx1: %v", err)
	}
	if _, err := tx1.Exec(t.Context(), `SELECT pg_advisory_xact_lock(hashtext($1))`, orgKey); err != nil {
		t.Fatalf("tx1 acquire lock: %v", err)
	}

	acquired := make(chan struct{})
	go func() {
		tx2, err := pool.Begin(context.Background())
		if err != nil {
			return
		}
		defer tx2.Rollback(context.Background())
		tx2.Exec(context.Background(), `SELECT pg_advisory_xact_lock(hashtext($1))`, orgKey)
		close(acquired)
	}()

	select {
	case <-acquired:
		t.Fatal("second lock acquisition should have blocked while the first transaction still holds it")
	case <-time.After(300 * time.Millisecond):
		// still blocked, as expected
	}

	if err := tx1.Rollback(t.Context()); err != nil {
		t.Fatalf("release tx1: %v", err)
	}

	select {
	case <-acquired:
		// unblocked after release, as expected
	case <-time.After(2 * time.Second):
		t.Fatal("second lock acquisition should have unblocked after the first transaction released it")
	}
}

func mustFindFileIDByName(t *testing.T, pool *pgxpool.Pool, orgID, name string) string {
	t.Helper()
	var id string
	if err := pool.QueryRow(t.Context(),
		`SELECT id FROM organization.files WHERE organization_id = $1 AND name = $2`, orgID, name,
	).Scan(&id); err != nil {
		t.Fatalf("mustFindFileIDByName(%q): %v", name, err)
	}
	return id
}
