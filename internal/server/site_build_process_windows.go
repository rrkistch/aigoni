//go:build windows

package server

import (
	"fmt"
	"os/exec"
	"time"
)

// Cancel the owned compiler tree, not only its Node launcher.
func configureBuildProcess(cmd *exec.Cmd) {
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		err := exec.Command("taskkill.exe", "/PID", fmt.Sprint(cmd.Process.Pid), "/T", "/F").Run()
		if err != nil {
			return cmd.Process.Kill()
		}
		return nil
	}
	cmd.WaitDelay = 5 * time.Second
}
