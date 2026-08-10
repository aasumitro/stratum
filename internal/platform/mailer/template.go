package mailer

import (
	"bytes"
	"cmp"
	"embed"
	"fmt"
	"html/template"
	"strings"
	"sync"
)

//go:embed templates/*.html
var templateFS embed.FS

// parsedEmail holds the pre-parsed subject and body templates for one
// (name, lang) pair. subjectTmpl is nil if the extracted subject failed to
// parse as a template — RenderTemplate then falls back to subjectRaw as a
// literal string, matching the pre-caching behavior.
type parsedEmail struct {
	subjectRaw  string
	subjectTmpl *template.Template
	body        *template.Template
}

// templateFS is static (embed.FS, never changes at runtime), so parsed
// templates are cached forever once built — email send is not a hot path,
// but re-parsing on every send was pure waste.
var (
	emailCache  sync.Map // key: name+"|"+lang -> *parsedEmail
	layoutCache sync.Map // key: lang -> *template.Template
)

// TemplateData holds the variables available to all email templates.
type TemplateData struct {
	AppName          string
	OrganizationName string
	UserName         string
	PlanName         string
	Amount           string
	Currency         string
	DueDate          string
	InviteURL        string
	ActionURL        string
	Role             string
	IsTrial          bool
	FromTrial        bool
	// UsageDetail is a pre-formatted "current / limit" string for
	// usage_limit_warning (e.g. "92 / 100 members") — kept as one generic
	// field rather than separate numeric fields since no other template
	// needs the parts individually.
	UsageDetail string
}

// layoutData wraps TemplateData with the already-rendered body HTML for
// the shared "_layout" template (header/footer chrome).
type layoutData struct {
	TemplateData
	Body template.HTML
}

// readTemplateFile reads templates/<base>.<lang>.html, falling back to
// templates/<base>.en.html if the language-specific file doesn't exist.
func readTemplateFile(base, lang string) ([]byte, error) {
	filename := fmt.Sprintf("templates/%s.%s.html", base, lang)
	raw, err := templateFS.ReadFile(filename)
	if err != nil {
		filename = fmt.Sprintf("templates/%s.en.html", base)
		raw, err = templateFS.ReadFile(filename)
		if err != nil {
			return nil, fmt.Errorf("mailer.readTemplateFile: template %s not found", base)
		}
	}
	return raw, nil
}

// loadParsedEmail returns the cached (subject, body) template pair for
// name+lang, parsing and caching it on first use.
func loadParsedEmail(name, lang string) (*parsedEmail, error) {
	key := name + "|" + lang
	if v, ok := emailCache.Load(key); ok {
		return v.(*parsedEmail), nil
	}

	raw, err := readTemplateFile(name, lang)
	if err != nil {
		return nil, fmt.Errorf("mailer.loadParsedEmail: %w", err)
	}
	content := string(raw)

	// Extract subject from first line: <!-- subject: ... -->
	subject := name
	if before, after, found := strings.Cut(content, "<!-- subject:"); found {
		if raw, remaining, ok := strings.Cut(after, "-->"); ok {
			subject = strings.TrimSpace(raw)
			content = before + remaining
		}
	}

	// Subject may contain template vars like {{.OrganizationName}}; if it
	// fails to parse, subjectTmpl stays nil and the raw literal is used.
	subjectTmpl, err := template.New("subject").Parse(subject)
	if err != nil {
		subjectTmpl = nil
	}

	bodyTmpl, err := template.New(name).Parse(content)
	if err != nil {
		return nil, fmt.Errorf("mailer.loadParsedEmail: parsing template %s: %w", name, err)
	}

	parsed := &parsedEmail{subjectRaw: subject, subjectTmpl: subjectTmpl, body: bodyTmpl}
	emailCache.Store(key, parsed)
	return parsed, nil
}

// loadLayout returns the cached "_layout" template for lang, parsing and
// caching it on first use.
func loadLayout(lang string) (*template.Template, error) {
	if v, ok := layoutCache.Load(lang); ok {
		return v.(*template.Template), nil
	}

	layoutRaw, err := readTemplateFile("_layout", lang)
	if err != nil {
		return nil, fmt.Errorf("mailer.loadLayout: %w", err)
	}
	layoutTmpl, err := template.New("layout").Parse(string(layoutRaw))
	if err != nil {
		return nil, fmt.Errorf("mailer.loadLayout: parsing layout: %w", err)
	}

	layoutCache.Store(lang, layoutTmpl)
	return layoutTmpl, nil
}

// RenderTemplate renders an email template by name and language, wrapped in
// the shared "_layout" chrome (brand header/footer).
// Returns the subject line (from <!-- subject: ... --> comment) and rendered HTML.
// Falls back to "en" if the requested language template doesn't exist.
func RenderTemplate(name, lang string, data TemplateData) (string, string, error) {
	data.AppName = cmp.Or(data.AppName, "Stratum")

	parsed, err := loadParsedEmail(name, lang)
	if err != nil {
		return "", "", fmt.Errorf("mailer.RenderTemplate: %w", err)
	}

	subject := parsed.subjectRaw
	if parsed.subjectTmpl != nil {
		var sb bytes.Buffer
		if parsed.subjectTmpl.Execute(&sb, data) == nil {
			subject = sb.String()
		}
	}

	var bodyBuf bytes.Buffer
	if err := parsed.body.Execute(&bodyBuf, data); err != nil {
		return "", "", fmt.Errorf("mailer.RenderTemplate: rendering template %s: %w", name, err)
	}

	// Wrap the rendered (already-escaped) body in the shared layout. The
	// body is safe to embed as raw template.HTML here — every dynamic value
	// in it was already escaped by the html/template Execute call above.
	layoutTmpl, err := loadLayout(lang)
	if err != nil {
		return "", "", fmt.Errorf("mailer.RenderTemplate: %w", err)
	}

	var htmlBuf bytes.Buffer
	//#nosec G203 -- bodyBuf was already through html/template's own auto-escaping one line
	// up — this isn't raw/untrusted input, it's that same output being composed into the
	// outer layout, the standard html/template layout-wrapping pattern.
	if err := layoutTmpl.Execute(&htmlBuf, layoutData{TemplateData: data, Body: template.HTML(bodyBuf.String())}); err != nil {
		return "", "", fmt.Errorf("mailer.RenderTemplate: rendering layout: %w", err)
	}

	return subject, htmlBuf.String(), nil
}
