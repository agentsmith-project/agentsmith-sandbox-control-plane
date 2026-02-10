package pty

import (
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"

	"github.com/creack/pty"
)

type Session struct {
	cmd     *exec.Cmd
	pty     *os.File
	shell   string
	workdir string
	closed  bool
	mu      sync.Mutex
}

func NewSession(shell, workdir string) *Session {
	return &Session{
		shell:   shell,
		workdir: workdir,
	}
}

func (s *Session) Start() error {
	s.cmd = exec.Command(s.shell)
	s.cmd.Dir = s.workdir
	s.cmd.SysProcAttr = &syscall.SysProcAttr{
		Setsid:  true,
		Setctty: true,
	}

	var err error
	s.pty, err = pty.Start(s.cmd)
	if err != nil {
		return err
	}
	return nil
}

func (s *Session) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pty == nil {
		return 0, io.ErrClosedPipe
	}
	return s.pty.Write(data)
}

func (s *Session) Read(buf []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pty == nil {
		return 0, io.ErrClosedPipe
	}
	return s.pty.Read(buf)
}

func (s *Session) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil // Already closed
	}
	s.closed = true
	if s.pty != nil {
		s.pty.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
		s.cmd.Wait()
	}
	return nil
}

func (s *Session) Resize(rows, cols uint16) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.pty == nil {
		return io.ErrClosedPipe
	}
	return pty.Setsize(s.pty, &pty.Winsize{
		Rows: rows,
		Cols: cols,
	})
}
