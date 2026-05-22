//go:build windows

package daemon

import (
	"os/exec"
	"syscall"
)

// detach configures cmd so the child does not stay attached to the parent
// console. CREATE_NEW_PROCESS_GROUP lets the child outlive the parent and
// receive its own Ctrl+Break signals if anything ever sends one.
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	const createNewProcessGroup = 0x00000200
	cmd.SysProcAttr.CreationFlags |= createNewProcessGroup
}
