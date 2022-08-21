//go:build !windows
// +build !windows

package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"github.com/creack/pty"
	gssh "github.com/gliderlabs/ssh"
)

// Tells a pty program input f to resize the window to the given size
func setWinsize(f *os.File, w, h int) {
	_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), uintptr(syscall.TIOCSWINSZ),
		uintptr(unsafe.Pointer(&struct{ h, w, x, y uint16 }{uint16(h), uint16(w), 0, 0})))
}

// WITH_PTY signals that we support pty on unix systems
const WITH_PTY = true

// WrapPTY start the given command with pty support and copy the in/output through the ssh session.
// This function also forward windows resizing
func WrapPTY(s gssh.Session, cmd *exec.Cmd, ptyReq gssh.Pty, winCh <-chan gssh.Window) error {
	cmd.Env = append(cmd.Env, fmt.Sprintf("TERM=%s", ptyReq.Term))
	f, err := pty.Start(cmd)
	if err != nil {
		return err
	}
	go func() {
		for win := range winCh {
			setWinsize(f, win.Width, win.Height)
		}
	}()
	// We create a copy goroutine for copying the app output to ssh and handle the error by a chan
	var errChan chan error
	go func() {
		defer close(errChan)
		_, err := io.Copy(f, s)
		if err != nil {
			errChan <- err
		}
	}()
	_, err = io.Copy(s, f) // stdout
	if err != nil {
		return err
	}
	// does the goroutine had an error?
	err, ok := <-errChan
	if ok && err != nil {
		return err
	}
	return nil
}
