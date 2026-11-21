package audit

import "context"

// RunCleanup runs the Writer's retention purge synchronously, for tests —
// production only reaches cleanup() via the hourly ticker in run().
func RunCleanup(w *Writer) { w.cleanup(context.Background()) }
