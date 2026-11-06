//go:build windows

package collector

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// configureGitProcess gives Git a dedicated Windows process group. Cancellation
// terminates the owned Git process and bounds Wait when inherited pipes remain
// open. Git is invoked without shell-based credential or component helpers.
func configureGitProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := command.Process.Kill()
		if errors.Is(err, os.ErrProcessDone) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
}
