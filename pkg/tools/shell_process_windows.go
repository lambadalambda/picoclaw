//go:build windows

package tools

import (
	"errors"
	"os"
	"os/exec"
)

func configureExecCommand(_ *exec.Cmd) {}

func killExecCommand(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}

	err := cmd.Process.Kill()
	if err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}

	return nil
}
