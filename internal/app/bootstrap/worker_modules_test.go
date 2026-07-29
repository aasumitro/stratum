package bootstrap_test

import (
	"testing"

	"github.com/aasumitro/stratum/internal/app/bootstrap"
	"github.com/aasumitro/stratum/internal/platform/config"
)

// TestNewWorkerModules_DoesNotPanic guards the wiring in NewWorkerModules
// itself: none of the module constructors or Set* calls it makes query the
// database, so this runs without live infra (Pool/Redis nil) and would
// still catch a regression like a nil Cfg field dereference during
// construction. It does not (and, without a pgx mock the repo doesn't have,
// can't) prove that a since-wired optional dependency changes behavior at
// call time — every one of those dependencies is nil-guarded and fails
// open, so there is no currently-exercised code path whose behavior would
// differ with or without the wiring. See worker_modules.go's comment on
// WorkerModules for why the wiring is still correct to add regardless.
func TestNewWorkerModules_DoesNotPanic(t *testing.T) {
	// MQPublisher left nil: *messaging.Publisher is a concrete type, and
	// none of the module constructors or Set* calls in NewWorkerModules
	// publish anything at construction time.
	infra := &bootstrap.Infra{
		Cfg: &config.Config{},
	}

	mods := bootstrap.NewWorkerModules(infra, nil) // nil storageClient: same "Storage.URL unconfigured" case NewAPIModules already handles

	if mods.Organization == nil || mods.Account == nil || mods.Reference == nil ||
		mods.Billing == nil || mods.Notification == nil {
		t.Fatal("NewWorkerModules returned a WorkerModules with a nil module")
	}
}
