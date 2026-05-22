//go:build !windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach configures cmd so the child survives the parent shell exiting.
// On Unix we put it in its own session (and thus its own process group)
// so SIGHUP from the controlling terminal isn't forwarded.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
}
