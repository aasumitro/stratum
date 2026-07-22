package db_test

import (
	"reflect"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/db"
)

func TestArgs_AddReturnsOneBasedPlaceholderPosition(t *testing.T) {
	a := db.NewArgs("org-1")
	if got := a.Add("folder-1"); got != 2 {
		t.Errorf("first Add after a 1-value seed: want 2, got %d", got)
	}
	if got := a.Add("search-term"); got != 3 {
		t.Errorf("second Add: want 3, got %d", got)
	}
}

func TestArgs_NewArgsWithNoSeed_FirstAddIsOne(t *testing.T) {
	a := db.NewArgs()
	if got := a.Add("x"); got != 1 {
		t.Errorf("first Add with no seed: want 1, got %d", got)
	}
}

func TestArgs_Next_PreviewsWithoutAdding(t *testing.T) {
	a := db.NewArgs("a", "b")
	if got := a.Next(); got != 3 {
		t.Fatalf("Next after 2-value seed: want 3, got %d", got)
	}
	if got := a.Next(); got != 3 {
		t.Errorf("Next must not mutate state: want 3 again, got %d", got)
	}
	if got := a.Add("c"); got != 3 {
		t.Errorf("Add after Next: want 3 (matching what Next predicted), got %d", got)
	}
	if got := a.Next(); got != 4 {
		t.Errorf("Next after Add: want 4, got %d", got)
	}
}

func TestArgs_Values_ReturnsInPlaceholderOrder(t *testing.T) {
	a := db.NewArgs("org-1")
	a.Add("folder-1")
	a.Add(42)

	want := []any{"org-1", "folder-1", 42}
	if got := a.Values(); !reflect.DeepEqual(got, want) {
		t.Errorf("Values() = %#v, want %#v", got, want)
	}
}
