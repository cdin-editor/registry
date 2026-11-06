//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package collector

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// configureGitProcess isolates Git and any helper processes in a process group.
// CommandContext's default cancellation kills only the direct child; Git may
// leave a helper holding stdout/stderr pipes open. Killing the process group
// ensures cancellation tears down the complete Git command tree.
func configureGitProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return os.ErrProcessDone
		}
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
	command.WaitDelay = 2 * time.Second
}
