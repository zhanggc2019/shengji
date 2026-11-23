//go:build !windows

package minutes

import "os/exec"

// hideNativeConsole uses the default process options when Windows flags are unavailable.
func hideNativeConsole(_ *exec.Cmd) {}
