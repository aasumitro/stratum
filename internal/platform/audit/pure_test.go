package audit

import (
	"encoding/json"
	"testing"
)

func TestCSVSafe(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"formula equals", "=cmd|'/c calc'!A1", "'=cmd|'/c calc'!A1"},
		{"formula plus", "+1+1", "'+1+1"},
		{"formula minus", "-1+1", "'-1+1"},
		{"formula at", "@SUM(1+1)", "'@SUM(1+1)"},
		{"leading tab", "\tmalicious", "'\tmalicious"},
		{"leading carriage return", "\rmalicious", "'\rmalicious"},
		{"ordinary user agent", "Mozilla/5.0", "Mozilla/5.0"},
		{"empty string", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := csvSafe(tc.in); got != tc.want {
				t.Errorf("csvSafe(%q): want %q, got %q", tc.in, tc.want, got)
			}
		})
	}
}

func TestRedactSensitiveFields(t *testing.T) {
	t.Run("redacts a top-level sensitive field", func(t *testing.T) {
		got := redactSensitiveFields([]byte(`{"password":"hunter2","full_name":"John"}`))
		var m map[string]any
		if err := json.Unmarshal(got, &m); err != nil {
			t.Fatalf("output is not valid JSON: %v", err)
		}
		if m["password"] != redactedValue {
			t.Errorf("password: want %q, got %v", redactedValue, m["password"])
		}
		if m["full_name"] != "John" {
			t.Errorf("full_name: want untouched, got %v", m["full_name"])
		}
	})

	t.Run("matches case-insensitively", func(t *testing.T) {
		got := redactSensitiveFields([]byte(`{"Token":"abc123"}`))
		var m map[string]any
		json.Unmarshal(got, &m)
		if m["Token"] != redactedValue {
			t.Errorf("Token: want redacted regardless of case, got %v", m["Token"])
		}
	})

	t.Run("redacts nested objects", func(t *testing.T) {
		got := redactSensitiveFields([]byte(`{"user":{"email":"a@b.com","secret":"x"}}`))
		var m map[string]any
		json.Unmarshal(got, &m)
		user := m["user"].(map[string]any)
		if user["secret"] != redactedValue {
			t.Errorf("nested secret: want redacted, got %v", user["secret"])
		}
		if user["email"] != "a@b.com" {
			t.Errorf("nested email: want untouched, got %v", user["email"])
		}
	})

	t.Run("redacts objects inside arrays", func(t *testing.T) {
		got := redactSensitiveFields([]byte(`{"items":[{"api_key":"k1"},{"name":"n1"}]}`))
		var m map[string]any
		json.Unmarshal(got, &m)
		items := m["items"].([]any)
		if items[0].(map[string]any)["api_key"] != redactedValue {
			t.Errorf("items[0].api_key: want redacted, got %v", items[0])
		}
		if items[1].(map[string]any)["name"] != "n1" {
			t.Errorf("items[1].name: want untouched, got %v", items[1])
		}
	})

	t.Run("does not match substrings", func(t *testing.T) {
		// "password_hint" and "full_name" must not be caught by a loose match
		// on "password"/"name" — exact key match only, to avoid surprises.
		got := redactSensitiveFields([]byte(`{"password_hint":"my pet's name","full_name":"John"}`))
		var m map[string]any
		json.Unmarshal(got, &m)
		if m["password_hint"] != "my pet's name" {
			t.Errorf("password_hint: want untouched (not an exact match), got %v", m["password_hint"])
		}
	})

	t.Run("invalid JSON is returned unchanged", func(t *testing.T) {
		got := redactSensitiveFields([]byte(`not-json`))
		if string(got) != "not-json" {
			t.Errorf("want input returned unchanged, got %q", got)
		}
	})

	t.Run("non-object JSON is returned unchanged", func(t *testing.T) {
		got := redactSensitiveFields([]byte(`"just a string"`))
		if string(got) != `"just a string"` {
			t.Errorf("want scalar JSON returned unchanged, got %q", got)
		}
	})

	t.Run("redacts invite code field", func(t *testing.T) {
		got := redactSensitiveFields([]byte(`{"code":"abc123","organization_id":"org-1"}`))
		var m map[string]any
		json.Unmarshal(got, &m)
		if m["code"] != redactedValue {
			t.Errorf("code: want redacted, got %v", m["code"])
		}
		if m["organization_id"] != "org-1" {
			t.Errorf("organization_id: want untouched, got %v", m["organization_id"])
		}
	})
}
