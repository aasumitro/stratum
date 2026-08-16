package audit

import (
	"encoding/json"
	"strings"
)

const redactedValue = "[REDACTED]"

// sensitiveFieldNames are JSON object keys whose values are never persisted
// into audit.events.metadata, matched case-insensitively and exactly (not
// substring) to avoid surprising false positives. Grounded in fields that
// actually appear in request bodies today (e.g. acceptInvitationRequest's
// "token") plus the obvious categories any future handler could introduce.
var sensitiveFieldNames = map[string]bool{
	"password":         true,
	"new_password":     true,
	"old_password":     true,
	"confirm_password": true,
	"token":            true,
	"access_token":     true,
	"refresh_token":    true,
	"secret":           true,
	"client_secret":    true,
	"api_key":          true,
	"service_role_key": true,
	"credential":       true,
	"credentials":      true,
	"code":             true,
}

// redactSensitiveFields walks a JSON value at any nesting depth (objects and
// arrays) and replaces the value of any key in sensitiveFieldNames with a
// fixed marker. Returns the input unchanged if it isn't valid JSON — callers
// already fall back to a safe default for invalid bodies elsewhere.
//
// This only ever runs on the copy of the request body destined for the audit
// log — the handler still receives and binds the original, unredacted body,
// since routes like acceptInvitation need the real token value to function.
func redactSensitiveFields(raw []byte) []byte {
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return raw
	}
	redactValue(v)
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return out
}

func redactValue(v any) {
	switch val := v.(type) {
	case map[string]any:
		for k, sub := range val {
			if sensitiveFieldNames[strings.ToLower(k)] {
				val[k] = redactedValue
				continue
			}
			redactValue(sub)
		}
	case []any:
		for _, sub := range val {
			redactValue(sub)
		}
	}
}
