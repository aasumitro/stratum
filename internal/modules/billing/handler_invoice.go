package billing

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/aasumitro/stratum/internal/platform/httpserver/middleware"
	"github.com/aasumitro/stratum/internal/platform/httpserver/response"
	"github.com/aasumitro/stratum/internal/platform/pdf"
)

// previewInvoice backs both the pending-changes bar (no query params —
// previews the subscription's current plan/cycle/addons/coupon) and the
// change-plan dialog's proration preview (?plan=&cycle= for a hypothetical
// target). Read-only — reuses composeInvoiceAmount/prorate, writes nothing.
//
// @Summary      Preview an invoice
// @Description  Read-only proration preview. With no query params, previews the subscription's current plan/cycle/addons/coupon; with plan/cycle, previews a hypothetical change.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true   "Organization ID"
// @Param        plan            query     string  false  "hypothetical target plan"
// @Param        cycle           query     string  false  "hypothetical target billing cycle"
// @Success      200             {object}  response.Payload{data=invoicePreview}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/preview [get]
func (h *handler) previewInvoice(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	preview, err := h.svc.previewInvoice(
		c.Request.Context(), subjectTypeOrganization,
		ws.ID, c.Query("plan"), c.Query("cycle"))
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(preview).JSON(c, http.StatusOK)
}

// listInvoices godoc
// @Summary      List invoices
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]invoiceRecord}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/invoices [get]
func (h *handler) listInvoices(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	invoices, err := h.svc.listInvoices(
		c.Request.Context(), subjectTypeOrganization, ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(invoices, int64(len(invoices))).JSON(c, http.StatusOK)
}

// listPaymentLinks godoc
// @Summary      List payment links
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Success      200             {object}  response.Payload{data=[]paymentLinkRecord}
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/payment-links [get]
func (h *handler) listPaymentLinks(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)

	links, err := h.svc.listPaymentLinks(
		c.Request.Context(), subjectTypeOrganization, ws.ID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.List(links, int64(len(links))).JSON(c, http.StatusOK)
}

// regeneratePaymentLink godoc
// @Summary      Regenerate a payment link
// @Description  Owner only. Issues a fresh payment link for an invoice, replacing an expired/failed one.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        invoiceID       path      string  true  "Invoice ID"
// @Success      201             {object}  response.Payload{data=paymentLinkRecord}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/invoices/{invoiceID}/pay/regenerate [post]
func (h *handler) regeneratePaymentLink(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	invoiceID := c.Param("invoiceID")

	link, err := h.svc.regeneratePaymentLink(
		c.Request.Context(), subjectTypeOrganization, ws.ID, invoiceID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(link).JSON(c, http.StatusCreated)
}

// createPaymentLink godoc
// @Summary      Create a payment link
// @Description  Owner only.
// @Tags         billing
// @Produce      json
// @Security     BearerAuth
// @Param        organizationID  path      string  true  "Organization ID"
// @Param        invoiceID       path      string  true  "Invoice ID"
// @Success      201             {object}  response.Payload{data=paymentLinkRecord}
// @Failure      403             {object}  response.Payload  "owner role required"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/invoices/{invoiceID}/pay [post]
func (h *handler) createPaymentLink(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	invoiceID := c.Param("invoiceID")

	link, err := h.svc.createPaymentLinkForOwner(
		c.Request.Context(), subjectTypeOrganization, ws.ID, invoiceID)
	if err != nil {
		response.FromError(c, err)
		return
	}
	response.Success(link).JSON(c, http.StatusCreated)
}

// invoicePDF godoc
// @Summary      Download an invoice as PDF
// @Tags         billing
// @Produce      application/pdf
// @Security     BearerAuth
// @Param        organizationID  path      string  true   "Organization ID"
// @Param        invoiceID       path      string  true   "Invoice ID"
// @Param        lang            query     string  false  "PDF language (default en)"
// @Success      200             {file}    file
// @Failure      500             {object}  response.Payload  "PDF generation failed"
// @Failure      401             {object}  response.Payload  "missing/invalid auth token"
// @Router       /organizations/{organizationID}/billing/invoices/{invoiceID}/pdf [get]
func (h *handler) invoicePDF(c *gin.Context) {
	ws, _ := middleware.OrganizationFromContext(c)
	invoiceID := c.Param("invoiceID")
	c.Set("audit.action", "invoice.pdf_download")

	inv, sub, planInfo, dbLineItems, err := h.svc.getInvoicePDFData(
		c.Request.Context(), subjectTypeOrganization, ws.ID, invoiceID)
	if err != nil {
		response.FromError(c, err)
		return
	}

	subtotal := int64(0)
	if inv.SubtotalCents != nil {
		subtotal = *inv.SubtotalCents
	}

	var lineItems []pdf.LineItem
	if len(dbLineItems) > 0 {
		for _, li := range dbLineItems {
			lineItems = append(lineItems, pdf.LineItem{
				Description: li.Description,
				Quantity:    li.Quantity,
				UnitPrice:   li.UnitPriceCents,
				Amount:      li.TotalCents,
			})
		}
	} else {
		lineItems = []pdf.LineItem{{
			Description: planInfo.Name + " plan (" + sub.Cycle + ")",
			Quantity:    1,
			UnitPrice:   subtotal,
			Amount:      subtotal,
		}}
	}

	invoiceLabel := inv.ID
	if inv.InvoiceNumber != nil {
		invoiceLabel = *inv.InvoiceNumber
	}

	data := pdf.InvoiceData{
		InvoiceID:   invoiceLabel,
		IssuedAt:    inv.CreatedAt,
		DueAt:       inv.DueAt,
		PaidAt:      inv.PaidAt,
		Status:      inv.Status,
		Currency:    inv.Currency,
		SubtotalAmt: subtotal,
		TaxRateBPS:  inv.TaxRateBPS,
		TaxAmt:      inv.TaxCents,
		TotalAmt:    inv.AmountCents,
		LineItems:   lineItems,
		From:        pdf.Party{Name: "Stratum"},
		To:          pdf.Party{Name: ws.Name},
	}
	_ = sub

	pdfBytes, err := pdf.RenderInvoice(data, c.DefaultQuery("lang", "en"))
	if err != nil {
		response.Error(
			"PDF_GENERATION_FAILED",
			"failed to generate invoice PDF",
		).JSON(c, http.StatusInternalServerError)
		return
	}

	c.Header("Content-Disposition", "attachment; filename=invoice-"+inv.ID+".pdf")
	c.Data(http.StatusOK, "application/pdf", pdfBytes)
}
