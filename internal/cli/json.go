package cli

import (
	"encoding/json"
	"fmt"
	"io"
)

// writeJSON renders v as indented JSON with a trailing newline, the shape every
// --json command emits.
func writeJSON(w io.Writer, v any) error {
	encoded, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(encoded))
	return err
}
