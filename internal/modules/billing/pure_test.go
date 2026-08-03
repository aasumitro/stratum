package billing

// Tests for all pure functions: no DB, no HTTP, no external deps.
// Package-internal so unexported symbols are directly accessible.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/contracts/events"
)

// --- calculateTax ---

func TestCalculateTax(t *testing.T) {
	cases := []struct {
		subtotal int64
		bps      int
		want     int64
	}{
		{100_00, 1000, 10_00},   // 10% of $100 = $10
		{100_00, 1100, 11_00},   // 11% of $100 = $11
		{500_00, 1000, 50_00},   // 10% of $500 = $50
		{100_00, 0, 0},          // no tax
		{0, 1000, 0},            // zero subtotal
		{100_00, 10000, 100_00}, // 100% tax (edge)
		{99, 1, 0},              // rounds down (1 BPS of 99 cents = 0)
	}
	for _, c := range cases {
		if got := calculateTax(c.subtotal, c.bps); got != c.want {
			t.Errorf("calculateTax(%d, %d) = %d, want %d", c.subtotal, c.bps, got, c.want)
		}
	}
}

// --- countryFromCurrency ---

func TestCountryFromCurrency(t *testing.T) {
	cases := []struct{ in, want string }{
		{"IDR", "ID"},
		{"USD", "US"},
		{"EUR", "US"}, // default
		{"", "US"},
	}
	for _, c := range cases {
		if got := countryFromCurrency(c.in); got != c.want {
			t.Errorf("countryFromCurrency(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- selectProvider ---

func TestSelectProvider(t *testing.T) {
	cases := []struct{ in, want string }{
		{"IDR", "xendit"},
		{"USD", "stripe"},
		{"", "stripe"}, // default
		{"EUR", "stripe"},
	}
	for _, c := range cases {
		if got := selectProvider(c.in); got != c.want {
			t.Errorf("selectProvider(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- prorate ---

func TestProrate(t *testing.T) {
	// Use Unix epoch seconds for clean integer math.
	// period: t=0 to t=100s; now=t=50s → 50s remaining.
	s := time.Unix(0, 0).UTC()
	e := time.Unix(100, 0).UTC()
	n := time.Unix(50, 0).UTC()

	t.Run("upgrade doubles price, halves remaining period (same cycle)", func(t *testing.T) {
		// old=$1, new=$2, both monthly → same cycleDays cancel out → ratio=0.5 → 25s remaining → end at t=75
		got := prorate(n, s, e, 1, 2, "monthly", "monthly")
		want := time.Unix(75, 0).UTC()
		if !got.Equal(want) {
			t.Errorf("upgrade: got %v, want %v", got, want)
		}
	})

	t.Run("downgrade halves price, doubles remaining period (same cycle)", func(t *testing.T) {
		// old=$2, new=$1, both monthly → ratio=2 → 100s remaining → end at t=150
		got := prorate(n, s, e, 2, 1, "monthly", "monthly")
		want := time.Unix(150, 0).UTC()
		if !got.Equal(want) {
			t.Errorf("downgrade: got %v, want %v", got, want)
		}
	})

	t.Run("same price, period unchanged (same cycle)", func(t *testing.T) {
		// ratio=1 → remaining unchanged → end = original periodEnd
		got := prorate(n, s, e, 5, 5, "monthly", "monthly")
		want := time.Unix(100, 0).UTC()
		if !got.Equal(want) {
			t.Errorf("same price: got %v, want %v", got, want)
		}
	})

	t.Run("new price zero returns now", func(t *testing.T) {
		got := prorate(n, s, e, 10, 0, "monthly", "monthly")
		if !got.Equal(n) {
			t.Errorf("newPrice=0: want now, got %v", got)
		}
	})

	t.Run("now after period end returns now", func(t *testing.T) {
		past := time.Unix(200, 0).UTC() // beyond periodEnd
		got := prorate(past, s, e, 10, 20, "monthly", "monthly")
		if !got.Equal(past) {
			t.Errorf("now past end: want now, got %v", got)
		}
	})

	t.Run("zero-length period returns now", func(t *testing.T) {
		got := prorate(n, n, n, 10, 20, "monthly", "monthly") // start == end == now
		if !got.Equal(n) {
			t.Errorf("zero period: want now, got %v", got)
		}
	})

	// Switching cycle length mid-plan-change must not treat the yearly
	// price as if it covered the same span as the monthly price.
	t.Run("monthly to yearly extends remaining period, not shrinks it", func(t *testing.T) {
		// 15 days left in a 30-day monthly cycle, old=$9/mo, new=$90/yr.
		// oldPricePerDay = 900/30 = 30c/day; newPricePerDay = 9000/365 ≈ 24.66c/day.
		// ratio ≈ 1.2167 → ~18.25 days remaining, not the ~1.5 days the old
		// (buggy) raw-price-ratio formula would have produced.
		start := time.Unix(0, 0).UTC()
		fullMonth := start.Add(30 * 24 * time.Hour)
		fifteenDaysIn := start.Add(15 * 24 * time.Hour)

		got := prorate(fifteenDaysIn, start, fullMonth, 900, 9000, "monthly", "yearly")

		oldPricePerDay := 900.0 / 30.0
		newPricePerDay := 9000.0 / 365.0
		wantRemaining := time.Duration(float64(15*24*time.Hour) * (oldPricePerDay / newPricePerDay))
		want := fifteenDaysIn.Add(wantRemaining)

		if !got.Equal(want) {
			t.Errorf("monthly->yearly: got %v, want %v", got, want)
		}
		if remaining := got.Sub(fifteenDaysIn); remaining <= 15*24*time.Hour {
			t.Errorf("monthly->yearly: want extended remaining period (>15d), got %v", remaining)
		}
	})

	t.Run("yearly to monthly shrinks remaining period, not extends it", func(t *testing.T) {
		// Inverse of the above: switching from a cheaper-per-day yearly plan
		// down to a pricier-per-day monthly plan should shrink, not extend.
		start := time.Unix(0, 0).UTC()
		fullYear := start.Add(365 * 24 * time.Hour)
		hundredDaysIn := start.Add(100 * 24 * time.Hour)

		got := prorate(hundredDaysIn, start, fullYear, 9000, 900, "yearly", "monthly")

		if remaining := got.Sub(hundredDaysIn); remaining >= 265*24*time.Hour {
			t.Errorf("yearly->monthly: want shrunk remaining period, got %v", remaining)
		}
	})
}

// --- computeAddonIncreaseProration ---

func TestComputeAddonIncreaseProration(t *testing.T) {
	t.Run("zero days remaining charges nothing", func(t *testing.T) {
		periodStart := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
		now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		got := computeAddonIncreaseProration(periodStart, now, now, 5, 100_00)
		if got != 0 {
			t.Errorf("computeAddonIncreaseProration() = %d, want 0", got)
		}
	})

	t.Run("negative remaining (past periodEnd) charges nothing", func(t *testing.T) {
		periodStart := time.Date(2026, 7, 3, 0, 0, 0, 0, time.UTC)
		now := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		periodEnd := now.AddDate(0, 0, -1)
		got := computeAddonIncreaseProration(periodStart, now, periodEnd, 5, 100_00)
		if got != 0 {
			t.Errorf("computeAddonIncreaseProration() = %d, want 0", got)
		}
	})

	t.Run("full period remaining charges exactly the full per-unit price", func(t *testing.T) {
		periodStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		periodEnd := periodStart.AddDate(0, 1, 0)
		got := computeAddonIncreaseProration(periodStart, periodStart, periodEnd, 1, 900)
		if got != 900 {
			t.Errorf("computeAddonIncreaseProration() = %d, want 900", got)
		}
	})

	t.Run("full period remaining charges the full per-unit price regardless of month length", func(t *testing.T) {
		// A same-day request should always cost exactly unitPrice*delta —
		// the reference length is the period's own real span, not a fixed
		// 30-day assumption, so a 31-day August period must not overcharge
		// (the bug this test guards against: was 51_666 instead of 50_000
		// for delta=5 at unitPrice=10_000 in a real 31-day period).
		periodStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC) // August: 31 days
		periodEnd := periodStart.AddDate(0, 1, 0)
		got := computeAddonIncreaseProration(periodStart, periodStart, periodEnd, 5, 10_000)
		if got != 50_000 {
			t.Errorf("computeAddonIncreaseProration() = %d, want 50000", got)
		}
	})

	t.Run("half the period remaining charges roughly half", func(t *testing.T) {
		periodStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		periodEnd := periodStart.AddDate(0, 0, 30)
		now := periodStart.AddDate(0, 0, 15)
		got := computeAddonIncreaseProration(periodStart, now, periodEnd, 1, 900)
		want := int64(450) // 900 * (15/30) * 1
		if got != want {
			t.Errorf("computeAddonIncreaseProration() = %d, want %d", got, want)
		}
	})

	t.Run("delta multiplies the per-unit charge", func(t *testing.T) {
		periodStart := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
		periodEnd := periodStart.AddDate(0, 0, 30)
		got := computeAddonIncreaseProration(periodStart, periodStart, periodEnd, 5, 900)
		want := int64(900 * 5)
		if got != want {
			t.Errorf("computeAddonIncreaseProration() = %d, want %d", got, want)
		}
	})
}

// --- normalizeStripeStatus ---

func TestNormalizeStripeStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"paid", "paid"},                // checkout session payment_status: paid
		{"no_payment_required", "paid"}, // free checkout (e.g. 100% coupon)
		{"canceled", "failed"},          // US spelling from Stripe PaymentIntent
		{"cancelled", "failed"},         // UK spelling, also mapped
		{"unpaid", "pending"},           // checkout session payment_status: unpaid
		{"pending", "pending"},
		{"active", "pending"}, // unknown → pending
		{"", "pending"},
	}
	for _, c := range cases {
		if got := normalizeStripeStatus(c.in); got != c.want {
			t.Errorf("normalizeStripeStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- normalizeXenditStatus ---

func TestNormalizeXenditStatus(t *testing.T) {
	cases := []struct{ in, want string }{
		{"PAID", "paid"},
		{"EXPIRED", "expired"},
		{"FAILED", "failed"},
		{"PENDING", "pending"}, // default
		{"paid", "pending"},    // case-sensitive: lowercase does not match
		{"", "pending"},
	}
	for _, c := range cases {
		if got := normalizeXenditStatus(c.in); got != c.want {
			t.Errorf("normalizeXenditStatus(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// --- verifyStripeSignature ---

// computeStripeSig builds a valid Stripe-Signature header for the given body,
// timestamp, and secret — mirrors the algorithm in verifyStripeSignature.
func computeStripeSig(body []byte, ts, secret string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "." + string(body)))
	return "t=" + ts + ",v1=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifyStripeSignature(t *testing.T) {
	body := []byte(`{"id":"evt_test"}`)
	secret := "whsec_test"
	now := strconv.FormatInt(time.Now().Unix(), 10)
	validHeader := computeStripeSig(body, now, secret)

	t.Run("empty secret bypasses verification (dev mode)", func(t *testing.T) {
		if !verifyStripeSignature(body, "", "") {
			t.Error("empty secret should return true")
		}
	})

	t.Run("valid signature within tolerance", func(t *testing.T) {
		if !verifyStripeSignature(body, validHeader, secret) {
			t.Error("valid sig should return true")
		}
	})

	t.Run("wrong secret rejects", func(t *testing.T) {
		if verifyStripeSignature(body, validHeader, "wrong-secret") {
			t.Error("wrong secret should return false")
		}
	})

	t.Run("tampered body rejects", func(t *testing.T) {
		if verifyStripeSignature([]byte(`{"id":"tampered"}`), validHeader, secret) {
			t.Error("tampered body should return false")
		}
	})

	t.Run("expired timestamp (>5 min) rejects", func(t *testing.T) {
		expiredTs := strconv.FormatInt(time.Now().Add(-6*time.Minute).Unix(), 10)
		expiredHeader := computeStripeSig(body, expiredTs, secret)
		if verifyStripeSignature(body, expiredHeader, secret) {
			t.Error("expired timestamp should return false")
		}
	})

	t.Run("timestamp exactly at tolerance boundary rejects (strictly >300s)", func(t *testing.T) {
		oldTs := strconv.FormatInt(time.Now().Add(-301*time.Second).Unix(), 10)
		oldHeader := computeStripeSig(body, oldTs, secret)
		if verifyStripeSignature(body, oldHeader, secret) {
			t.Error(">300s old timestamp should return false")
		}
	})

	t.Run("missing t= field rejects", func(t *testing.T) {
		mac := hmac.New(sha256.New, []byte(secret))
		mac.Write([]byte(now + "." + string(body)))
		noTs := "v1=" + hex.EncodeToString(mac.Sum(nil))
		if verifyStripeSignature(body, noTs, secret) {
			t.Error("missing t= should return false")
		}
	})

	t.Run("missing v1= field rejects", func(t *testing.T) {
		_ = now // suppress unused warning
		if verifyStripeSignature(body, "t="+now, secret) {
			t.Error("missing v1= should return false")
		}
	})

	t.Run("non-numeric timestamp rejects", func(t *testing.T) {
		if verifyStripeSignature(body, "t=notanumber,v1=abc", secret) {
			t.Error("non-numeric timestamp should return false")
		}
	})

	t.Run("multiple v1 signatures, one correct, accepts", func(t *testing.T) {
		multiHeader := validHeader + ",v1=deadbeef"
		if !verifyStripeSignature(body, multiHeader, secret) {
			t.Error("one valid sig among multiple should return true")
		}
	})
}

// --- verifyXenditToken ---

func TestVerifyXenditToken(t *testing.T) {
	t.Run("empty config token bypasses check (dev mode)", func(t *testing.T) {
		if !verifyXenditToken("anything", "") {
			t.Error("empty config should return true")
		}
	})

	t.Run("matching tokens accepted", func(t *testing.T) {
		if !verifyXenditToken("secret", "secret") {
			t.Error("matching tokens should return true")
		}
	})

	t.Run("wrong header token rejected", func(t *testing.T) {
		if verifyXenditToken("wrong", "secret") {
			t.Error("wrong token should return false")
		}
	})

	t.Run("empty header with non-empty config rejected", func(t *testing.T) {
		if verifyXenditToken("", "secret") {
			t.Error("empty header against set config should return false")
		}
	})
}

// --- couponStillApplicable ---

func TestCouponStillApplicable(t *testing.T) {
	two := 2

	cases := []struct {
		name          string
		cadence       string
		appliedCount  int
		durationCount *int
		want          bool
	}{
		{"forever always applicable", "forever", 0, nil, true},
		{"forever applicable after many invoices", "forever", 50, nil, true},
		{"once applicable before first invoice", "once", 0, nil, true},
		{"once exhausted after first invoice", "once", 1, nil, false},
		{"repeated applicable within duration", "repeated", 1, &two, true},
		{"repeated exhausted at duration", "repeated", 2, &two, false},
		{"repeated with nil duration is exhausted", "repeated", 0, nil, false},
		{"unknown cadence is not applicable", "bogus", 0, nil, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := couponStillApplicable(c.cadence, c.appliedCount, c.durationCount); got != c.want {
				t.Errorf("couponStillApplicable(%q, %d, %v) = %v, want %v", c.cadence, c.appliedCount, c.durationCount, got, c.want)
			}
		})
	}
}

// --- computeCouponDiscount ---

func TestComputeCouponDiscount(t *testing.T) {
	amount := func(v int64) *int64 { return &v }
	percent := func(v int16) *int16 { return &v }

	cases := []struct {
		name         string
		discountType string
		amountCents  *int64
		percentOff   *int16
		subtotal     int64
		want         int64
	}{
		{"fixed discount under subtotal", "fixed", amount(500), nil, 900, 500},
		{"fixed discount clamped to subtotal", "fixed", amount(5000), nil, 900, 900},
		{"percent discount rounds down", "percent", nil, percent(10), 999, 99}, // 999*10/100 = 99.9 -> 99
		{"percent discount full", "percent", nil, percent(100), 900, 900},
		{"nil amount on fixed type is zero", "fixed", nil, nil, 900, 0},
		{"unknown discount type is zero", "bogus", amount(500), nil, 900, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computeCouponDiscount(c.discountType, c.amountCents, c.percentOff, c.subtotal); got != c.want {
				t.Errorf("computeCouponDiscount(%q, ..., %d) = %d, want %d", c.discountType, c.subtotal, got, c.want)
			}
		})
	}
}

// --- crossedUsageThreshold ---

func TestCrossedUsageThreshold(t *testing.T) {
	cases := []struct {
		name              string
		previous, current int64
		limit             int
		want              bool
	}{
		{"first write crosses 90%", 0, 90, 100, true},
		{"already over threshold, no re-fire", 91, 95, 100, false},
		{"still under threshold", 50, 89, 100, false},
		{"exactly at threshold counts as crossed", 89, 90, 100, true},
		{"decreasing usage never crosses", 95, 50, 100, false},
		{"crosses then stays at same value next call", 90, 90, 100, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := crossedUsageThreshold(c.previous, c.current, c.limit); got != c.want {
				t.Errorf("crossedUsageThreshold(%d, %d, %d) = %v, want %v", c.previous, c.current, c.limit, got, c.want)
			}
		})
	}
}

// --- abs ---

func TestAbs(t *testing.T) {
	cases := []struct {
		in, want int64
	}{
		{5, 5},
		{-5, 5},
		{0, 0},
		{-1, 1},
	}
	for _, c := range cases {
		if got := abs(c.in); got != c.want {
			t.Errorf("abs(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

// --- resolveChangedBy ---

type stubUserReaderForHistory struct{}

func (stubUserReaderForHistory) GetUserByAuthSub(_ context.Context, sub string) (*contracts.UserInfo, error) {
	if sub == "sub_known" {
		return &contracts.UserInfo{AuthSub: sub, Name: "Jane Cooper", Email: "jane@example.com"}, nil
	}
	if sub == "sub_no_name" {
		return &contracts.UserInfo{AuthSub: sub, Name: "", Email: "noname@example.com"}, nil
	}
	return nil, pgx.ErrNoRows
}

func (stubUserReaderForHistory) GetUserByEmail(_ context.Context, _ string) (*contracts.UserInfo, error) {
	return nil, pgx.ErrNoRows
}

func (stubUserReaderForHistory) GetUsersByAuthSubs(_ context.Context, _ []string) (map[string]contracts.UserInfo, error) {
	return nil, nil
}

func (stubUserReaderForHistory) IsMFAEnabled(_ context.Context, _ string) (bool, error) {
	return false, nil
}

func TestResolveChangedBy(t *testing.T) {
	svcNoReader := &service{}
	svcWithReader := &service{userReader: stubUserReaderForHistory{}}
	ctx := t.Context()

	cases := []struct {
		name      string
		svc       *service
		changedBy string
		wantName  string
		wantKind  string
	}{
		{"system sentinel", svcNoReader, "system", "System", "system"},
		{"empty falls back to system", svcNoReader, "", "System", "system"},
		{"webhook sentinel", svcNoReader, "webhook", "Webhook", "webhook"},
		{"unwired reader falls back to raw value", svcNoReader, "sub_known", "sub_known", "user"},
		{"resolved user with name", svcWithReader, "sub_known", "Jane Cooper", "user"},
		{"resolved user without name falls back to email", svcWithReader, "sub_no_name", "noname@example.com", "user"},
		{"unknown auth_sub falls back to raw value", svcWithReader, "sub_missing", "sub_missing", "user"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gotName, gotKind := c.svc.resolveChangedBy(ctx, c.changedBy)
			if gotName != c.wantName || gotKind != c.wantKind {
				t.Errorf("resolveChangedBy(%q) = (%q, %q), want (%q, %q)", c.changedBy, gotName, gotKind, c.wantName, c.wantKind)
			}
		})
	}
}

// --- staleSubscriptionCheck ---

func TestStaleSubscriptionCheck(t *testing.T) {
	periodEnd := time.Date(2027, 8, 23, 0, 0, 0, 0, time.UTC)
	trialEnd := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	otherEnd := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name  string
		sub   *subscriptionRecord
		check events.SubscriptionCheck
		want  bool
	}{
		{
			"active sub, ExpectedEnd matches current period_end: not stale",
			&subscriptionRecord{PeriodEnd: &periodEnd},
			events.SubscriptionCheck{ExpectedEnd: periodEnd},
			false,
		},
		{
			"active sub, ExpectedEnd doesn't match (period_end moved since scheduling): stale",
			&subscriptionRecord{PeriodEnd: &periodEnd},
			events.SubscriptionCheck{ExpectedEnd: otherEnd},
			true,
		},
		{
			"trialing sub, ExpectedEnd matches current trial_end: not stale",
			&subscriptionRecord{TrialEnd: &trialEnd},
			events.SubscriptionCheck{ExpectedEnd: trialEnd, IsTrial: true},
			false,
		},
		{
			"trialing sub, ExpectedEnd doesn't match trial_end: stale",
			&subscriptionRecord{TrialEnd: &trialEnd},
			events.SubscriptionCheck{ExpectedEnd: otherEnd, IsTrial: true},
			true,
		},
		{
			"IsTrial but sub has no trial_end (e.g. converted to active since scheduling): stale",
			&subscriptionRecord{PeriodEnd: &periodEnd, TrialEnd: nil},
			events.SubscriptionCheck{ExpectedEnd: trialEnd, IsTrial: true},
			true,
		},
		{
			"not IsTrial but sub has no period_end (e.g. still trialing since scheduling): stale",
			&subscriptionRecord{PeriodEnd: nil, TrialEnd: &trialEnd},
			events.SubscriptionCheck{ExpectedEnd: periodEnd, IsTrial: false},
			true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := staleSubscriptionCheck(c.sub, c.check); got != c.want {
				t.Errorf("staleSubscriptionCheck() = %v, want %v", got, c.want)
			}
		})
	}
}

// --- maxExtendableMonths ---

func TestMaxExtendableMonths(t *testing.T) {
	// Runway-cap model: period_end may never sit more than 24 calendar
	// months ahead of now, full stop — not "24 months since created_at".
	// 2025-01-01 has no leap day inside its 24-month window, so it lines up
	// cleanly with round-number worked examples below; the leap-year case
	// gets its own dedicated test further down.
	now := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name            string
		monthsRemaining int // periodEnd = now + monthsRemaining
		wantExtendable  int
	}{
		{"1 month remaining leaves 23 extendable", 1, 23},
		{"13 months remaining leaves 11 extendable", 13, 11},
		// Boundaries.
		{"0 months remaining (period ends today) leaves the full 24", 0, 24},
		{"24 months remaining (already at cap) leaves 0", 24, 0},
		{"23 months remaining leaves exactly 1", 23, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			periodEnd := now.AddDate(0, c.monthsRemaining, 0)
			if got := maxExtendableMonths(periodEnd, now); got != c.wantExtendable {
				t.Errorf("maxExtendableMonths() = %d, want %d", got, c.wantExtendable)
			}
		})
	}

	t.Run("never returns more than the 24-month bound even for an already-expired periodEnd", func(t *testing.T) {
		periodEnd := now.AddDate(0, -100, 0) // pathological: periodEnd long in the past
		if got := maxExtendableMonths(periodEnd, now); got > 24 {
			t.Errorf("maxExtendableMonths() = %d, want <= 24", got)
		}
	})

	t.Run("a plan-change-shifted time-of-day doesn't cost a whole month", func(t *testing.T) {
		// Reproduces a real case: now at 09:04:50, but period_end sits a few
		// hours later in the day (10:40:05) because a plan change reprorated
		// the period anchored to time.Now() of that change. 13 months
		// remaining should still leave 11 extendable exactly like the
		// clean-timestamp case above — the few hours of same-day drift must
		// not shave off a 12th.
		drifted := time.Date(2025, 1, 1, 9, 4, 50, 0, time.UTC)
		periodEnd := drifted.AddDate(0, 13, 0).Add(95 * time.Minute) // ~10:40 the same day
		if got := maxExtendableMonths(periodEnd, drifted); got != 11 {
			t.Errorf("maxExtendableMonths() = %d, want 11", got)
		}
	})

	t.Run("a leap day inside the 24-month window no longer costs a whole month", func(t *testing.T) {
		// Real case that motivated the runway-cap rewrite: an org created
		// 2026-07-30, still on its first yearly period (period_end
		// 2027-07-30, 12 months remaining). 2028 is a leap year, so the old
		// fixed-duration-from-created_at cap (2*365*24h) landed one day
		// short of a true calendar 2-year horizon and undercounted this as
		// 11 extendable instead of 12. Calendar arithmetic (AddDate) on both
		// sides of the comparison must get this right regardless of leap
		// years.
		now := time.Date(2026, 7, 30, 14, 48, 45, 0, time.UTC)
		periodEnd := now.AddDate(0, 12, 0) // 2027-07-30, 12 months remaining
		if got := maxExtendableMonths(periodEnd, now); got != 12 {
			t.Errorf("maxExtendableMonths() = %d, want 12", got)
		}
	})
}

// --- computeExtensionSubtotal ---

func TestComputeExtensionSubtotal(t *testing.T) {
	planInfo := &contracts.PlanInfo{
		Prices: map[string]contracts.PlanPrices{
			"USD": {Monthly: 9_00, Yearly: 90_00}, // $9/mo, $90/yr (2 months free)
		},
	}

	cases := []struct {
		name   string
		months int
		want   int64
	}{
		{"months < 12 stays flat monthly x months (today's existing behavior)", 1, 9_00},
		{"months < 12, multiple months", 6, 54_00},
		{"exactly 12 months bills one yearly block, not 12x monthly", 12, 90_00},
		{"13 months = 1 yearly block + 1 month remainder", 13, 90_00 + 9_00},
		{"24 months = 2 yearly blocks, no remainder", 24, 2 * 90_00},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := computeExtensionSubtotal(planInfo, "USD", c.months); got != c.want {
				t.Errorf("computeExtensionSubtotal(%d) = %d, want %d", c.months, got, c.want)
			}
		})
	}
}
