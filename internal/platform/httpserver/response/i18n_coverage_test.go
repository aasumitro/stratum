package response

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// codesWithDynamicMessages carry per-call-site interpolated content
// (a raw Go error's .Error(), or a formatted metric name) that a
// code-keyed translation would erase, so they're deliberately not
// required to have an errors.codes entry — the frontend falls back to
// the backend's own message for these instead.
var codesWithDynamicMessages = map[string]bool{
	"INVALID_REQUEST":                true,
	"UNPROCESSABLE":                  true,
	"EXTENSION_EXCEEDS_MAX_DURATION": true,
	"PLAN_LIMIT_REACHED":             true,
}

var (
	// codeCallPattern matches a literal code string passed straight to a
	// call site, e.g. response.Error("EMAIL_REQUIRED", ...).
	codeCallPattern = regexp.MustCompile(
		`(?:response\.Error|apperr\.(?:Internal|BadRequest|Unauthorized|Forbidden|NotFound|Conflict|PaymentRequired|Validation))\(\s*"([A-Z_]+)"`,
	)
	// codeIdentCallPattern matches a call site passing a named constant
	// instead, e.g. apperr.Validation(cancellationScheduledCode, ...) —
	// resolved against constStringPattern's declarations below.
	codeIdentCallPattern = regexp.MustCompile(
		`(?:response\.Error|apperr\.(?:Internal|BadRequest|Unauthorized|Forbidden|NotFound|Conflict|PaymentRequired|Validation))\(\s*([A-Za-z_]\w*)\s*,`,
	)
	// constStringPattern matches a `name = "VALUE"` const declaration
	// (inside or outside a const(...) block) so a call site referencing the
	// constant by name can be resolved to the code it actually sends.
	constStringPattern = regexp.MustCompile(`(?m)^\s*(\w+)\s*=\s*"([A-Z_]+)"\s*$`)
)

// repoRoot resolves the module root from this file's own path rather than
// the working directory `go test` happens to run from.
func repoRoot(t *testing.T) string {
	t.Helper()
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("could not resolve caller for repo root lookup")
	}
	// this file: <root>/internal/platform/httpserver/response/i18n_coverage_test.go
	return filepath.Clean(filepath.Join(filepath.Dir(thisFile), "..", "..", "..", ".."))
}

// codesThrownInGo greps every response.Error(.../apperr.<Kind>(... call site
// under internal/ for the code it sends — whether passed as a literal
// string or as a named constant (resolved via constStringPattern).
func codesThrownInGo(t *testing.T, root string) map[string]bool {
	t.Helper()
	codes := map[string]bool{}
	consts := map[string]string{}
	var identCalls []string

	internalDir := filepath.Join(root, "internal")
	err := filepath.Walk(internalDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, m := range codeCallPattern.FindAllSubmatch(src, -1) {
			codes[string(m[1])] = true
		}
		for _, m := range codeIdentCallPattern.FindAllSubmatch(src, -1) {
			identCalls = append(identCalls, string(m[1]))
		}
		for _, m := range constStringPattern.FindAllSubmatch(src, -1) {
			consts[string(m[1])] = string(m[2])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walking %s: %v", internalDir, err)
	}

	for _, ident := range identCalls {
		if code, ok := consts[ident]; ok {
			codes[code] = true
		}
		// An identifier that doesn't resolve to a known string constant
		// (a variable, a formatted value) is dynamic content by
		// definition — same reasoning as codesWithDynamicMessages, so
		// it's not required to have a translation.
	}
	return codes
}

// localeCodes reads the errors.codes keys out of one of ui/app's locale
// JSON files.
func localeCodes(t *testing.T, root, lang string) map[string]bool {
	t.Helper()
	path := filepath.Join(root, "ui", "app", "src", "lib", "i18n", "locales", lang+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var doc struct {
		Errors struct {
			Codes map[string]string `json:"codes"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	codes := make(map[string]bool, len(doc.Errors.Codes))
	for code := range doc.Errors.Codes {
		codes[code] = true
	}
	return codes
}

// TestErrorCodesHaveI18nCoverage guards against translation drift: a backend
// error code shipped with no matching translation degrades silently to English
// for a non-English user, with nothing else in the system surfacing that regression.
func TestErrorCodesHaveI18nCoverage(t *testing.T) {
	root := repoRoot(t)
	thrown := codesThrownInGo(t, root)

	for _, lang := range []string{"en", "id"} {
		t.Run(lang, func(t *testing.T) {
			catalog := localeCodes(t, root, lang)
			var missing []string
			for code := range thrown {
				if codesWithDynamicMessages[code] {
					continue
				}
				if !catalog[code] {
					missing = append(missing, code)
				}
			}
			if len(missing) > 0 {
				sort.Strings(missing)
				t.Errorf("%d code(s) thrown in Go have no errors.codes entry in %s.json: %v",
					len(missing), lang, missing)
			}
		})
	}
}
