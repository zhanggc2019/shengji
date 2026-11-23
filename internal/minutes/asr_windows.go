//go:build windows

package minutes

import (
	"os/exec"
	"syscall"
)

// hideNativeConsole suppresses console windows for the optional offline worker.
func hideNativeConsole(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true, CreationFlags: 0x08000000}
}
