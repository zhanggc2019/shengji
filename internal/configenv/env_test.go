package configenv

import (
	"os"
	"testing"
)

// TestGet covers legacy compatibility, the current name, and an explicit empty override.
func TestGet(t *testing.T) {
	const current = "SHENGJI_ENV_TEST"
	const legacy = "ODDMINUTES_ENV_TEST"
	t.Setenv(current, "")
	t.Setenv(legacy, "legacy")
	if value := Get(current); value != "" {
		t.Fatalf("explicit empty variable read as %q", value)
	}
	if err := os.Unsetenv(current); err != nil {
		t.Fatal(err)
	}
	if value := Get(current); value != "legacy" {
		t.Fatalf("legacy variable read as %q", value)
	}
	t.Setenv(current, "current")
	if value := Get(current); value != "current" {
		t.Fatalf("current variable read as %q", value)
	}
}
