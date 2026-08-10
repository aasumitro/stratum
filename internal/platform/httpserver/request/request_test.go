package request

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/go-playground/validator/v10"
)

// --- toSnake ---

func TestToSnake(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Name", "name"},
		{"OrganizationName", "organization_name"},
		{"FirstName", "first_name"},
		{"alreadysnake", "alreadysnake"},
		{"A", "a"}, // single uppercase, no leading underscore
		{"", ""},   // empty
	} {
		if got := toSnake(tc.in); got != tc.want {
			t.Errorf("toSnake(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// --- ValidationError ---

func TestValidationError_NonValidatorError(t *testing.T) {
	p := ValidationError(errors.New("raw error"))
	if p.Status.Code != "INVALID_REQUEST" {
		t.Errorf("code = %q, want INVALID_REQUEST", p.Status.Code)
	}
}

func TestValidationError_BuildsFieldMap(t *testing.T) {
	type req struct {
		Name  string `validate:"required"`
		Email string `validate:"required,email"`
	}
	v := validator.New()
	p := ValidationError(v.Struct(req{}))

	if p.Status.Code != "VALIDATION_FAILED" {
		t.Fatalf("code = %q, want VALIDATION_FAILED", p.Status.Code)
	}
	fields, ok := p.Status.Details.(map[string][]FieldError)
	if !ok {
		t.Fatalf("details type = %T, want map[string][]FieldError", p.Status.Details)
	}
	if len(fields["name"]) == 0 || fields["name"][0].Code != "required" {
		t.Errorf("name errors = %+v, want a single required FieldError", fields["name"])
	}
	// Email fails both "required" (empty) and "email" (invalid format) —
	// go-playground/validator only reports the first failed tag per field.
	if len(fields["email"]) == 0 || fields["email"][0].Code != "required" {
		t.Errorf("email errors = %+v, want a single required FieldError", fields["email"])
	}
}

func TestValidationError_CarriesParam(t *testing.T) {
	type req struct {
		Name string `validate:"min=3"`
	}
	v := validator.New()
	p := ValidationError(v.Struct(req{Name: "ab"}))

	fields := p.Status.Details.(map[string][]FieldError)
	if got := fields["name"]; len(got) != 1 || got[0].Code != "min" || got[0].Param != "3" {
		t.Errorf("name errors = %+v, want [{Code:min Param:3}]", got)
	}
}

// --- SniffImageType ---

func TestSniffImageType(t *testing.T) {
	pngMagic := []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}
	jpegMagic := []byte{0xFF, 0xD8, 0xFF}

	for _, tc := range []struct {
		name    string
		content []byte
		wantOK  bool
	}{
		{"png magic bytes", append(pngMagic, []byte("rest of file")...), true},
		{"jpeg magic bytes", append(jpegMagic, []byte("rest of file")...), true},
		{"html masquerading as image (client header would lie)", []byte("<html><body>not an image</body></html>"), false},
		{"empty file", nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ct, ok := SniffImageType(bytes.NewReader(tc.content))
			if ok != tc.wantOK {
				t.Errorf("ok = %v, want %v (sniffed content-type: %q)", ok, tc.wantOK, ct)
			}
		})
	}
}

func TestSniffImageType_RewindsReaderToStart(t *testing.T) {
	content := append([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A}, []byte("the rest of the file")...)
	r := bytes.NewReader(content)

	if _, ok := SniffImageType(r); !ok {
		t.Fatal("want ok=true for a valid PNG")
	}

	rest, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("ReadAll after sniff: %v", err)
	}
	if !bytes.Equal(rest, content) {
		t.Errorf("reader wasn't rewound: got %d bytes, want the full %d-byte original content", len(rest), len(content))
	}
}
