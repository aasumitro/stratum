package pdf

import (
	"bytes"
	"testing"
	"time"
)

// currencyIDR keeps the Indonesian-rupiah currency code to a single literal
// in this test file so it doesn't push the package-wide goconst count over
// the threshold.
const currencyIDR = "IDR"

func TestFormatMoney(t *testing.T) {
	cases := []struct {
		amount   int64
		currency string
		want     string
	}{
		{1234, "USD", "$12.34"},
		{100, "USD", "$1.00"},
		{5, "USD", "$0.05"},
		{0, "USD", "$0.00"},
		{50000, currencyIDR, "Rp50000"},
		{999, "EUR", "$9.99"}, // unknown currency falls back to the dollar format
	}
	for _, c := range cases {
		if got := FormatMoney(c.amount, c.currency); got != c.want {
			t.Errorf("FormatMoney(%d, %q) = %q, want %q", c.amount, c.currency, got, c.want)
		}
	}
}

func TestFormatPercent(t *testing.T) {
	cases := map[int]string{
		1000: "10",    // 10.00% → whole
		1150: "11.50", // fractional
		0:    "0",
		825:  "8.25",
	}
	for bps, want := range cases {
		if got := formatPercent(bps); got != want {
			t.Errorf("formatPercent(%d) = %q, want %q", bps, got, want)
		}
	}
}

func TestLabelsFor(t *testing.T) {
	if labelsFor("en").title == "" {
		t.Error("English labels should be populated")
	}
	if labelsFor("id").title == "" {
		t.Error("Indonesian labels should be populated")
	}
	// unknown language falls back to English
	if labelsFor("zz") != labelsFor("en") {
		t.Error("unknown language should fall back to English labels")
	}
}

func TestRenderInvoice_ProducesPDF(t *testing.T) {
	due := time.Now().Add(7 * 24 * time.Hour)
	data := InvoiceData{
		InvoiceID:   "INV-2026-001",
		IssuedAt:    time.Now(),
		DueAt:       &due,
		Status:      "pending",
		Currency:    "USD",
		SubtotalAmt: 10000,
		TaxRateBPS:  1100,
		TaxAmt:      1100,
		TotalAmt:    11100,
		LineItems: []LineItem{
			{Description: "Growth plan (monthly)", Quantity: 1, UnitPrice: 10000, Amount: 10000},
		},
		From: Party{Name: "Stratum Inc", Email: "billing@stratum.test", Country: "US"},
		To:   Party{Name: "Acme Co", Email: "ap@acme.test", Country: "US"},
	}

	out, err := RenderInvoice(data, "en")
	if err != nil {
		t.Fatalf("RenderInvoice: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("RenderInvoice produced empty output")
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Errorf("output does not look like a PDF (missing %%PDF header): % x", out[:min(8, len(out))])
	}
}

func TestRenderInvoice_PaidStatus_IndonesianLabels(t *testing.T) {
	paid := time.Now()
	data := InvoiceData{
		InvoiceID: "INV-ID-01", IssuedAt: time.Now(), PaidAt: &paid,
		Status: "paid", Currency: currencyIDR, SubtotalAmt: 150000, TotalAmt: 150000,
		LineItems: []LineItem{{Description: "Langganan", Quantity: 1, UnitPrice: 150000, Amount: 150000}},
		From:      Party{Name: "Penjual"}, To: Party{Name: "Pembeli"},
	}
	out, err := RenderInvoice(data, "id")
	if err != nil {
		t.Fatalf("RenderInvoice(id, paid): %v", err)
	}
	if !bytes.HasPrefix(out, []byte("%PDF")) {
		t.Error("expected a PDF for the paid/Indonesian invoice")
	}
}
