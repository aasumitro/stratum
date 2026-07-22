package request

import (
	"bytes"
	"errors"
	"io"
	"strings"
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
	fields, ok := p.Status.Details.(map[string][]string)
	if !ok {
		t.Fatalf("details type = %T, want map[string][]string", p.Status.Details)
	}
	if len(fields["name"]) == 0 {
		t.Error("expected errors for field 'name'")
	}
	if len(fields["email"]) == 0 {
		t.Error("expected errors for field 'email'")
	}
}

// --- message (all switch branches) ---

func TestMessage_Tags(t *testing.T) {
	v := validator.New()

	// fe returns the first FieldError for the given struct value.
	fe := func(obj any) validator.FieldError {
		t.Helper()
		err := v.Struct(obj)
		if err == nil {
			t.Fatal("expected validation error, got nil")
		}
		return err.(validator.ValidationErrors)[0]
	}

	type strField struct{ F string }
	type intField struct{ F int }

	for _, tc := range []struct {
		tag  string
		fe   validator.FieldError
		want string // substring expected in message
	}{
		{"required", fe(&struct {
			F string `validate:"required"`
		}{}), "required"},
		{"email", fe(&struct {
			F string `validate:"email"`
		}{F: "notanemail"}), "valid email"},
		{"min", fe(&struct {
			F string `validate:"min=3"`
		}{F: "ab"}), "at least 3"},
		{"max", fe(&struct {
			F string `validate:"max=2"`
		}{F: "abc"}), "greater than 2"},
		{"oneof", fe(&struct {
			F string `validate:"oneof=foo bar"`
		}{F: "baz"}), "Must be one of"},
		{"url", fe(&struct {
			F string `validate:"url"`
		}{F: "not-url"}), "valid URL"},
		{"uuid", fe(&struct {
			F string `validate:"uuid"`
		}{F: "not-uuid"}), "valid UUID"},
		{"len", fe(&struct {
			F string `validate:"len=5"`
		}{F: "ab"}), "exactly 5"},
		{"gt", fe(&struct {
			F int `validate:"gt=5"`
		}{F: 5}), "greater than 5"},
		{"gte", fe(&struct {
			F int `validate:"gte=5"`
		}{F: 4}), "at least 5"},
		{"lt", fe(&struct {
			F int `validate:"lt=5"`
		}{F: 5}), "less than 5"},
		{"lte", fe(&struct {
			F int `validate:"lte=5"`
		}{F: 6}), "greater than 5"},
		{"default", fe(&struct {
			F string `validate:"alpha"`
		}{F: "123"}), "invalid"},
	} {
		_ = strField{}
		_ = intField{}
		got := message(tc.fe)
		if !strings.Contains(got, tc.want) {
			t.Errorf("tag=%s: message=%q, want contains %q", tc.tag, got, tc.want)
		}
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
