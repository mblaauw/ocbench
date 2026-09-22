package canon

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
)

// JSON returns a deterministic canonical JSON encoding of v: object keys are
// sorted lexicographically, HTML escaping is disabled and the trailing newline
// emitted by encoding/json is stripped. Numbers use encoding/json's default
// (float64) representation; call sites must not mix in json.Number.
func JSON(v any) ([]byte, error) {
	normalized, err := toCanonical(v)
	if err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(normalized); err != nil {
		return nil, fmt.Errorf("encode canonical json: %w", err)
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

func toCanonical(v any) (any, error) {
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(t))
		for _, k := range keys {
			cv, err := toCanonical(t[k])
			if err != nil {
				return nil, err
			}
			out[k] = cv
		}
		return out, nil
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			cv, err := toCanonical(item)
			if err != nil {
				return nil, err
			}
			out[i] = cv
		}
		return out, nil
	default:
		return v, nil
	}
}

// Hash returns the hex-encoded SHA-256 of JSON(v).
func Hash(v any) (string, error) {
	b, err := JSON(v)
	if err != nil {
		return "", err
	}
	return SHA256Hex(b), nil
}

// SHA256Hex returns the hex-encoded SHA-256 digest of b.
func SHA256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// HashBytes returns the hex-encoded SHA-256 digest of b.
func HashBytes(b []byte) string { return SHA256Hex(b) }
