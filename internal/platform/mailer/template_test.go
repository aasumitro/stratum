package mailer_test

import (
	"strings"
	"testing"

	"github.com/aasumitro/stratum/internal/platform/mailer"
)

func TestRenderTemplate_SubjectInterpolated(t *testing.T) {
	subject, _, err := mailer.RenderTemplate("invite", "en", mailer.TemplateData{OrganizationName: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(subject, "Acme") {
		t.Errorf("subject %q: missing organization name", subject)
	}
}

func TestRenderTemplate_SubjectWithAmount(t *testing.T) {
	// invoice_paid subject: "Payment received — {{.Amount}}"
	subject, _, err := mailer.RenderTemplate("invoice_paid", "en", mailer.TemplateData{
		Amount: "$99.00",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(subject, "$99.00") {
		t.Errorf("subject %q: missing amount", subject)
	}
}

func TestRenderTemplate_BodyContainsData(t *testing.T) {
	_, html, err := mailer.RenderTemplate("invite", "en", mailer.TemplateData{
		OrganizationName: "Acme",
		InviteURL:        "https://example.com/inv",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "Acme") {
		t.Errorf("body missing organization name")
	}
	if !strings.Contains(html, "https://example.com/inv") {
		t.Errorf("body missing invite URL")
	}
}

func TestRenderTemplate_DefaultAppName(t *testing.T) {
	// AppName="" → defaults to "Stratum"
	_, html, err := mailer.RenderTemplate("invite", "en", mailer.TemplateData{OrganizationName: "X"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "Stratum") {
		t.Errorf("body missing default app name 'Stratum'")
	}
}

func TestRenderTemplate_LangFallback(t *testing.T) {
	// "xx" doesn't exist → falls back to "en" without error
	_, html, err := mailer.RenderTemplate("invite", "xx", mailer.TemplateData{OrganizationName: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if html == "" {
		t.Error("fallback render returned empty body")
	}
}

func TestRenderTemplate_IDLocale(t *testing.T) {
	_, html, err := mailer.RenderTemplate("invite", "id", mailer.TemplateData{OrganizationName: "Acme"})
	if err != nil {
		t.Fatal(err)
	}
	if html == "" {
		t.Error("id locale render returned empty body")
	}
}

func TestRenderTemplate_MissingTemplate(t *testing.T) {
	_, _, err := mailer.RenderTemplate("nonexistent", "en", mailer.TemplateData{})
	if err == nil {
		t.Error("expected error for missing template, got nil")
	}
}
