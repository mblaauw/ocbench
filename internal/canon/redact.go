package canon

import (
	"encoding/json"
	"fmt"
	"regexp"
)

// RedactedValue is substituted for the value of any sensitive object key.
const RedactedValue = "<redacted>"

var sensitiveKey = regexp.MustCompile(`(?i)(api[_-]?key|token|secret|password|passwd|credential|authorization|cookie)`)

// IsSensitiveKey reports whether key matches the sensitive-key pattern. The
// match is a case-insensitive substring search, so e.g. "X-Api-Key" matches.
func IsSensitiveKey(key string) bool { return sensitiveKey.MatchString(key) }

// Redact returns a deep copy of v in which the value of every sensitive object
// key is replaced by RedactedValue. The input is not mutated.
func Redact(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			if IsSensitiveKey(k) {
				out[k] = RedactedValue
				continue
			}
			rv, err := Redact(val)
			if err != nil {
				return nil, err
			}
			out[k] = rv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			rv, err := Redact(item)
			if err != nil {
				return nil, err
			}
			out[i] = rv
		}
		return out, nil
	default:
		return v, nil
	}
}

// RedactRaw decodes raw JSON, redacts it and returns the redacted value.
func RedactRaw(raw []byte) (any, error) {
	var v any
	if err := jsonUnmarshalStrictish(raw, &v); err != nil {
		return nil, err
	}
	return Redact(v)
}

// jsonUnmarshalStrictish decodes JSON with a clearer error. Despite the name it
// does not reject unknown fields: resolved config gains keys between versions
// and must not fail to decode.
func jsonUnmarshalStrictish(raw []byte, v any) error {
	if err := json.Unmarshal(raw, v); err != nil {
		return fmt.Errorf("decode json: %w", err)
	}
	return nil
}
