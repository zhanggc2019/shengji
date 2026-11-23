// Package configenv reads current and legacy Shengji configuration variables.
package configenv

import (
	"os"
	"strings"
)

// Get reads a SHENGJI_ variable, falling back to its old name only when unset.
func Get(name string) string {
	if value, set := os.LookupEnv(name); set {
		return value
	}
	suffix, canonical := strings.CutPrefix(name, "SHENGJI_")
	if !canonical {
		return ""
	}
	return os.Getenv("ODDMINUTES_" + suffix)
}
