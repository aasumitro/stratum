package httpclient

import (
	"net/http"
	"time"
)

// TrustedClient is a shared *http.Client for outbound calls to fixed,
// trusted hosts baked into config/code (Supabase admin API, Stripe, Xendit)
// — not tenant-supplied URLs, so SSRFSafeClient's dial-time host
// revalidation doesn't apply here. The Timeout bounds worst-case goroutine
// lifetime even when the caller's own context has no deadline.
var TrustedClient = &http.Client{Timeout: 15 * time.Second}
