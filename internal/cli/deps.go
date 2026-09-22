package cli

import (
	"fmt"
	"reflect"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/opencode"
)

// Deps carries the injectable dependencies shared by every command. Command
// implementations never reach for the environment directly; they take a Deps
// and call resolve before use, which makes each command testable in isolation.
type Deps struct {
	Adapter opencode.Adapter // nil → real adapter built from config
	Paths   config.Paths     // zero → config.ResolveOS()
	Config  config.Config    // zero → config.Load(paths)
}

// resolve fills zero fields lazily. Config is not comparable because it holds
// a slice, so reflect reports whether it is the zero value.
func (d Deps) resolve() (Deps, error) {
	out := d
	if out.Paths == (config.Paths{}) {
		out.Paths = config.ResolveOS()
	}
	if reflect.ValueOf(out.Config).IsZero() {
		cfg, err := config.Load(out.Paths)
		if err != nil {
			return Deps{}, fmt.Errorf("load config: %w", err)
		}
		out.Config = cfg
	}
	if out.Adapter == nil {
		out.Adapter = opencode.NewReal(opencode.Options{Bin: out.Config.OpenCodeBin})
	}
	return out, nil
}
