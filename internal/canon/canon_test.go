package canon

import (
	"encoding/json"
	"testing"
)

func TestJSONSortsKeysDeterministically(t *testing.T) {
	a := map[string]any{"b": 1, "a": map[string]any{"y": 2, "x": 3}}
	b := map[string]any{"a": map[string]any{"x": 3, "y": 2}, "b": 1}
	ba, err := JSON(a)
	if err != nil {
		t.Fatal(err)
	}
	bb, err := JSON(b)
	if err != nil {
		t.Fatal(err)
	}
	if string(ba) != string(bb) {
		t.Fatalf("nondeterministic:\n%s\n%s", ba, bb)
	}
	if string(ba) != `{"a":{"x":3,"y":2},"b":1}` {
		t.Fatalf("canonical form = %s", ba)
	}
}

func TestHashIsStableAcrossReencode(t *testing.T) {
	raw := []byte(`{"z":1,"a":{"nested":[1,2,{"k":"v"}]}}`)
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	h1, err := Hash(v)
	if err != nil {
		t.Fatal(err)
	}
	var v2 any
	if err := json.Unmarshal(raw, &v2); err != nil {
		t.Fatal(err)
	}
	h2, err := Hash(v2)
	if err != nil {
		t.Fatal(err)
	}
	if h1 != h2 {
		t.Fatalf("hashes differ: %s %s", h1, h2)
	}
	if len(h1) != 64 {
		t.Fatalf("hash length = %d", len(h1))
	}
}
