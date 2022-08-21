package main

import (
	"fmt"
	"os/exec"

	gssh "github.com/gliderlabs/ssh"
)

// WITH_PTY signals that we don't support pty on windows systems yet
const WITH_PTY = false

func WrapPTY(s gssh.Session, cmd *exec.Cmd, ptyReq gssh.Pty, winCh <-chan gssh.Window) error {
	return fmt.Errorf("PTY not yet supported under windows")
}
