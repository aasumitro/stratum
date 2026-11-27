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

// A subject is a plain-text header: data in it reads as typed, not HTML-escaped.
func TestRenderTemplate_SubjectIsPlainText(t *testing.T) {
	subject, _, err := mailer.RenderTemplate("invoice_paid", "en", mailer.TemplateData{Amount: "R&D <$10>"})
	if err != nil {
		t.Fatalf("RenderTemplate: %v", err)
	}
	if !strings.Contains(subject, "R&D <$10>") || strings.Contains(subject, "&amp;") {
		t.Errorf("subject = %q, want the data unescaped", subject)
	}
}

// A template's summary line renders as plain text; one without it gives "".
func TestRenderSummary(t *testing.T) {
	got := mailer.RenderSummary("usage_limit_warning", "en", mailer.TemplateData{UsageDetail: "92 / 100 members"})
	if got != "92 / 100 members of the plan's allowance used." {
		t.Errorf("usage_limit_warning summary = %q", got)
	}
	if got := mailer.RenderSummary("usage_limit_warning", "id", mailer.TemplateData{UsageDetail: "92 / 100 anggota"}); !strings.Contains(got, "92 / 100 anggota") {
		t.Errorf("id summary = %q", got)
	}
	if got := mailer.RenderSummary("invoice_failed", "en", mailer.TemplateData{}); got != "" {
		t.Errorf("template without a summary: got %q", got)
	}
	subject, html, err := mailer.RenderTemplate("usage_limit_warning", "en", mailer.TemplateData{OrganizationName: "Acme"})
	if err != nil || strings.Contains(html, "summary:") || subject != "Acme is approaching a usage limit" {
		t.Errorf("the summary comment must not reach the email: subject %q, err %v", subject, err)
	}
}
