package pdf

import (
	"bytes"
	_ "embed"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/signintech/gopdf"
)

// embeddedFont is the last-resort fallback when none of defaultFont's OS
// paths exist (e.g. a minimal Alpine/distroless container with no system
// fonts installed) — without it, invoice PDF generation 500s outright on
// those hosts instead of rendering with a bundled font. Same file already
// shipped for ui/studio's frontend (SIL Open Font License, see "Inter Font
// License.txt" there); copied here rather than imported since ui/studio
// carries its own standalone go.mod (never shared with this module).
//
//go:embed assets/Inter-Medium.ttf
var embeddedFont []byte

const (
	pageW     = 595.28 // A4 width in points
	pageH     = 841.89 // A4 height in points
	marginL   = 50.0
	marginR   = 50.0
	marginT   = 50.0
	lineH     = 18.0
	colRight  = 400.0
	fontSizeS = 9
	fontSizeM = 11
	fontSizeL = 16

	// Line item table columns (marginL=50 .. pageW-marginR=545.28).
	colDescX = marginL
	colDescW = 250.0
	colQtyX  = colDescX + colDescW
	colQtyW  = 60.0
	colUnitX = colQtyX + colQtyW
	colUnitW = 90.0
	colAmtX  = colUnitX + colUnitW
	colAmtW  = pageW - marginR - colAmtX

	badgeW = 80.0
	badgeH = 20.0
)

// brand palette — same hex values used across the email templates
// (internal/platform/mailer/templates/_layout.*.html), for visual
// consistency between the PDF and the emails a customer already sees.
type rgbColor struct{ r, g, b uint8 }

var (
	colorPrimary = rgbColor{22, 27, 29}    // #161b1d
	colorMuted   = rgbColor{103, 120, 124} // #67787c
	colorDanger  = rgbColor{231, 0, 11}    // #e7000b
	colorBorder  = rgbColor{227, 231, 232} // #e3e7e8
	colorMutedBg = rgbColor{241, 243, 243} // #f1f3f3
	colorWhite   = rgbColor{255, 255, 255}
)

// InvoiceData holds everything needed to render an invoice PDF.
// Designed to be module-agnostic — any module can build this struct.
type InvoiceData struct {
	InvoiceID   string
	IssuedAt    time.Time
	DueAt       *time.Time
	PaidAt      *time.Time
	Status      string
	Currency    string
	SubtotalAmt int64
	TaxRateBPS  int
	TaxAmt      int64
	TotalAmt    int64
	LineItems   []LineItem
	From        Party
	To          Party
}

// LineItem is a single row in the invoice.
type LineItem struct {
	Description string
	Quantity    int
	UnitPrice   int64
	Amount      int64
}

// Party is either the seller or the buyer.
type Party struct {
	Name    string
	Email   string
	Country string
}

// invoiceLabels holds every fixed piece of copy on the invoice PDF for one language.
type invoiceLabels struct {
	title, billFrom, billTo, description, qty, unitPrice, amount, subtotal, tax, total string
	invoiceNo, issued, due, paid, thankYou                                             string
	statusPaid, statusPending, statusFailed, statusExpired                             string
}

var invoiceLabelsByLang = map[string]invoiceLabels{
	"en": {
		title: "INVOICE", billFrom: "Bill From", billTo: "Bill To",
		description: "Description", qty: "Qty", unitPrice: "Unit Price", amount: "Amount",
		subtotal: "Subtotal", tax: "Tax (%s%%)", total: "Total",
		invoiceNo: "Invoice #%s", issued: "Issued %s", due: "Due %s", paid: "Paid %s",
		thankYou:   "Thank you for your business.",
		statusPaid: "PAID", statusPending: "PENDING", statusFailed: "FAILED", statusExpired: "EXPIRED",
	},
	"id": {
		title: "FAKTUR", billFrom: "Ditagihkan Dari", billTo: "Ditagihkan Kepada",
		description: "Deskripsi", qty: "Jml", unitPrice: "Harga Satuan", amount: "Jumlah",
		subtotal: "Subtotal", tax: "Pajak (%s%%)", total: "Total",
		invoiceNo: "No. Faktur %s", issued: "Diterbitkan %s", due: "Jatuh Tempo %s", paid: "Dibayar %s",
		thankYou:   "Terima kasih atas kepercayaan Anda.",
		statusPaid: "LUNAS", statusPending: "TERTUNDA", statusFailed: "GAGAL", statusExpired: "KEDALUWARSA",
	},
}

// labelsFor returns the label set for lang, falling back to English if unrecognized.
func labelsFor(lang string) invoiceLabels {
	if l, ok := invoiceLabelsByLang[lang]; ok {
		return l
	}
	return invoiceLabelsByLang["en"]
}

// RenderInvoice generates a PDF from InvoiceData and returns the bytes.
// lang selects the label language ("en"/"id"); unrecognized values fall back to English.
func RenderInvoice(d InvoiceData, lang string) ([]byte, error) {
	lbl := labelsFor(lang)

	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: gopdf.Rect{W: pageW, H: pageH}})
	p.AddPage()

	if path := defaultFont(); path != "" {
		if err := p.AddTTFFont("sans", path); err != nil {
			return nil, fmt.Errorf("loading font: %w", err)
		}
	} else if err := p.AddTTFFontData("sans", embeddedFont); err != nil {
		return nil, fmt.Errorf("loading embedded fallback font: %w", err)
	}
	setTextColor(p, colorPrimary)

	y := marginT

	// Header: wordmark (left) + invoice title/number (right).
	y = drawText(p, "STRATUM", fontSizeL, marginL, y)
	setTextColor(p, colorMuted)
	cellAt(p, colUnitX, marginT, colAmtX+colAmtW-colUnitX, lineH, lbl.title, gopdf.Right)
	cellAt(p, colUnitX, marginT+lineH, colAmtX+colAmtW-colUnitX, lineH, fmt.Sprintf(lbl.invoiceNo, d.InvoiceID), gopdf.Right)
	setTextColor(p, colorPrimary)

	// Status badge, top-right.
	drawStatusBadge(p, d.Status, lbl, pageW-marginR-badgeW, marginT+2*lineH+4)

	y += lineH
	y = drawLine(p, y, colorBorder)
	y += lineH / 2

	// Meta: issued / due / paid, muted.
	setTextColor(p, colorMuted)
	_ = p.SetFont("sans", "", fontSizeS)
	metaParts := []string{fmt.Sprintf(lbl.issued, d.IssuedAt.Format("Jan 2, 2006"))}
	if d.DueAt != nil {
		metaParts = append(metaParts, fmt.Sprintf(lbl.due, d.DueAt.Format("Jan 2, 2006")))
	}
	if d.PaidAt != nil {
		metaParts = append(metaParts, fmt.Sprintf(lbl.paid, d.PaidAt.Format("Jan 2, 2006")))
	}
	y = drawText(p, strings.Join(metaParts, "   ·   "), fontSizeS, marginL, y)
	y += lineH / 2
	setTextColor(p, colorPrimary)

	// Bill From / Bill To, side by side.
	fromY, toY := y, y
	fromY = drawPartyBlock(p, lbl.billFrom, d.From, marginL, fromY)
	toY = drawPartyBlock(p, lbl.billTo, d.To, marginL+260, toY)
	y = max(fromY, toY) + lineH/2

	// Line items table header.
	y = drawFilledRect(p, marginL, y, pageW-marginR, y+lineH, colorMutedBg)
	setTextColor(p, colorMuted)
	_ = p.SetFont("sans", "", fontSizeS)
	cellAt(p, colDescX+6, y-lineH, colDescW-6, lineH, lbl.description, gopdf.Left)
	cellAt(p, colQtyX, y-lineH, colQtyW, lineH, lbl.qty, gopdf.Center)
	cellAt(p, colUnitX, y-lineH, colUnitW-6, lineH, lbl.unitPrice, gopdf.Right)
	cellAt(p, colAmtX, y-lineH, colAmtW-6, lineH, lbl.amount, gopdf.Right)
	setTextColor(p, colorPrimary)

	// Line item rows.
	_ = p.SetFont("sans", "", fontSizeS)
	for _, item := range d.LineItems {
		cellAt(p, colDescX+6, y, colDescW-6, lineH, item.Description, gopdf.Left)
		cellAt(p, colQtyX, y, colQtyW, lineH, fmt.Sprintf("%d", item.Quantity), gopdf.Center)
		cellAt(p, colUnitX, y, colUnitW-6, lineH, FormatMoney(item.UnitPrice, d.Currency), gopdf.Right)
		cellAt(p, colAmtX, y, colAmtW-6, lineH, FormatMoney(item.Amount, d.Currency), gopdf.Right)
		y += lineH
		y = drawLine(p, y, colorBorder)
	}
	y += lineH / 2

	// Totals — right-aligned label/value pairs.
	y = drawTotalRow(p, lbl.subtotal, FormatMoney(d.SubtotalAmt, d.Currency), y, fontSizeS, false)
	if d.TaxRateBPS > 0 {
		taxLabel := fmt.Sprintf(lbl.tax, formatPercent(d.TaxRateBPS))
		y = drawTotalRow(p, taxLabel, FormatMoney(d.TaxAmt, d.Currency), y, fontSizeS, false)
	}
	y += 4
	y = drawFilledRect(p, colUnitX, y, pageW-marginR, y+lineH+6, colorMutedBg) - lineH - 6
	y = drawTotalRow(p, lbl.total, FormatMoney(d.TotalAmt, d.Currency), y+3, fontSizeM, true)

	// Footer.
	y += lineH
	y = drawLine(p, y, colorBorder)
	setTextColor(p, colorMuted)
	_ = drawText(p, lbl.thankYou, fontSizeS, marginL, y+lineH/2)
	setTextColor(p, colorPrimary)

	var buf bytes.Buffer
	if _, err := p.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func drawText(p *gopdf.GoPdf, text string, size int, x, y float64) float64 {
	_ = p.SetFont("sans", "", size)
	p.SetX(x)
	p.SetY(y)
	_ = p.Cell(nil, text)
	return y + lineH
}

// cellAt draws text in a fixed-size box at (x,y), aligned within it.
func cellAt(p *gopdf.GoPdf, x, y, w, h float64, text string, align int) {
	p.SetX(x)
	p.SetY(y)
	_ = p.CellWithOption(&gopdf.Rect{W: w, H: h}, text, gopdf.CellOption{Align: align | gopdf.Middle})
}

// drawLine draws a colored horizontal rule and returns the y just below it.
func drawLine(p *gopdf.GoPdf, y float64, c rgbColor) float64 {
	setStrokeColor(p, c)
	p.Line(marginL, y, pageW-marginR, y)
	setStrokeColor(p, colorPrimary)
	return y + 4
}

// drawFilledRect fills a rectangle and returns the y just below it.
func drawFilledRect(p *gopdf.GoPdf, x0, y0, x1, y1 float64, c rgbColor) float64 {
	fillColor(p, c)
	_ = p.Rectangle(x0, y0, x1, y1, "F", 0, 0)
	return y1
}

// drawPartyBlock renders a labeled name/email/country block and returns the y after it.
func drawPartyBlock(p *gopdf.GoPdf, label string, party Party, x, y float64) float64 {
	setTextColor(p, colorMuted)
	_ = p.SetFont("sans", "", fontSizeS)
	p.SetX(x)
	p.SetY(y)
	_ = p.Cell(nil, label)
	y += lineH

	setTextColor(p, colorPrimary)
	_ = p.SetFont("sans", "", fontSizeM)
	p.SetX(x)
	p.SetY(y)
	_ = p.Cell(nil, party.Name)
	y += lineH

	_ = p.SetFont("sans", "", fontSizeS)
	if party.Email != "" {
		p.SetX(x)
		p.SetY(y)
		_ = p.Cell(nil, party.Email)
		y += lineH
	}
	if party.Country != "" {
		p.SetX(x)
		p.SetY(y)
		_ = p.Cell(nil, party.Country)
		y += lineH
	}
	return y
}

// drawStatusBadge draws a small filled pill showing the invoice status.
func drawStatusBadge(p *gopdf.GoPdf, status string, lbl invoiceLabels, x, y float64) {
	bg, text := colorMutedBg, lbl.statusPending
	switch status {
	case "paid":
		bg, text = colorPrimary, lbl.statusPaid
	case "failed":
		bg, text = colorDanger, lbl.statusFailed
	case "expired":
		bg, text = colorDanger, lbl.statusExpired
	}
	fg := colorPrimary
	if bg != colorMutedBg {
		fg = colorWhite
	}

	fillColor(p, bg)
	_ = p.Rectangle(x, y, x+badgeW, y+badgeH, "F", 4, 3)
	setTextColor(p, fg)
	_ = p.SetFont("sans", "", fontSizeS)
	cellAt(p, x, y, badgeW, badgeH, text, gopdf.Center)
	setTextColor(p, colorPrimary)
}

// drawTotalRow renders a right-aligned label/value pair and returns the next y.
func drawTotalRow(p *gopdf.GoPdf, label, value string, y float64, size int, emphasis bool) float64 {
	_ = p.SetFont("sans", "", size)
	if emphasis {
		setTextColor(p, colorPrimary)
	} else {
		setTextColor(p, colorMuted)
	}
	cellAt(p, colUnitX, y, colUnitW-6, lineH, label, gopdf.Right)
	if emphasis {
		setTextColor(p, colorPrimary)
	}
	cellAt(p, colAmtX, y, colAmtW-6, lineH, value, gopdf.Right)
	setTextColor(p, colorPrimary)
	return y + lineH
}

func setTextColor(p *gopdf.GoPdf, c rgbColor)   { p.SetTextColor(c.r, c.g, c.b) }
func setStrokeColor(p *gopdf.GoPdf, c rgbColor) { p.SetStrokeColor(c.r, c.g, c.b) }
func fillColor(p *gopdf.GoPdf, c rgbColor)      { p.SetFillColor(c.r, c.g, c.b) }

// FormatMoney renders a minor-unit amount (cents) as a display string for
// the given currency — amountMinor is negative for discount line items.
func FormatMoney(amountMinor int64, currency string) string {
	switch currency {
	case "IDR":
		return "Rp" + groupThousands(amountMinor)
	default:
		// Sign extracted before formatting: Go's / truncates toward zero
		// and % keeps the dividend's sign, so a negative amountMinor
		// (e.g. -550) would otherwise print as "$-5.-50" instead of
		// "-$5.50" — groupThousands (the IDR path above) already handles
		// this correctly, this default branch didn't.
		sign := ""
		if amountMinor < 0 {
			sign = "-"
			amountMinor = -amountMinor
		}
		return fmt.Sprintf("%s$%d.%02d", sign, amountMinor/100, amountMinor%100)
	}
}

// groupThousands inserts "." as a thousands separator (e.g. 299000 ->
// "299.000") — IDR has no minor unit here, so the raw integer needs the
// same grouping the frontend already applies via
// Number.prototype.toLocaleString("id-ID") for the same amount_cents value.
func groupThousands(n int64) string {
	s := strconv.FormatInt(n, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, '.')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

func formatPercent(bps int) string {
	whole := bps / 100
	frac := bps % 100
	if frac == 0 {
		return fmt.Sprintf("%d", whole)
	}
	return fmt.Sprintf("%d.%02d", whole, frac)
}

func defaultFont() string {
	for _, path := range []string{
		"/usr/share/fonts/truetype/dejavu/DejaVuSans.ttf",
		"/usr/share/fonts/TTF/DejaVuSans.ttf",
		"/System/Library/Fonts/Supplemental/Arial.ttf",
		"/System/Library/Fonts/Geneva.ttf",
		"/Library/Fonts/Arial.ttf",
	} {
		if _, err := os.Stat(path); err == nil {
			return path
		}
	}
	return ""
}
