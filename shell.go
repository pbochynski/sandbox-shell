package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ExecResult holds the output of a single command run in the persistent shell.
type ExecResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Shell manages one persistent bash subprocess and serializes commands through it.
type Shell struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout *bufio.Scanner
	mu     sync.Mutex
}

// NewShell starts a bash subprocess and returns a ready Shell.
func NewShell() (*Shell, error) {
	return newShell()
}

func newShell() (*Shell, error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("pipe: %w", err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		pr.Close()
		pw.Close()
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}

	cmd := exec.Command("bash", "--norc", "--noprofile")
	cmd.Stdin = stdinR
	cmd.Stdout = pw
	cmd.Stderr = pw // initial stderr goes to same pipe; redirected per-command

	if err := cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		stdinR.Close()
		stdinW.Close()
		return nil, fmt.Errorf("start bash: %w", err)
	}
	pw.Close()     // parent doesn't write to stdout pipe
	stdinR.Close() // child owns the read end

	return &Shell{
		cmd:    cmd,
		stdin:  stdinW,
		stdout: bufio.NewScanner(pr),
	}, nil
}

// restart replaces the shell's internals with a fresh bash process.
// Must be called with s.mu held.
func (s *Shell) restart() error {
	s.stdin.Close()
	s.cmd.Wait() //nolint:errcheck

	fresh, err := newShell()
	if err != nil {
		return err
	}
	s.cmd = fresh.cmd
	s.stdin = fresh.stdin
	s.stdout = fresh.stdout
	return nil
}

// Exec runs cmd in the persistent bash shell and returns its output.
// stdout and stderr are separated via file redirection; exit code is captured via sentinel.
//
// Special case: if cmd is "exit N", bash itself exits. Exec detects this (shell died),
// captures the exit code from the process, restarts bash, and returns the result.
//
// Returns error only on timeout or unrecoverable shell death.
func (s *Shell) Exec(cmd string, timeout time.Duration) (ExecResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Detect bare "exit [N]" commands — these terminate bash itself.
	// Wrap them specially: run in a subshell so we capture the exit code,
	// then restart bash if needed.
	if isExitCommand(cmd) {
		return s.execExit(cmd, timeout)
	}

	return s.execNormal(cmd, timeout)
}

// isExitCommand returns true if cmd is a bare "exit" or "exit N" invocation
// that would terminate the bash process.
func isExitCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "exit" {
		return true
	}
	if strings.HasPrefix(trimmed, "exit ") {
		rest := strings.TrimSpace(trimmed[5:])
		// Only treat as bare exit if the rest is a number (not a compound command)
		_, err := strconv.Atoi(rest)
		return err == nil
	}
	return false
}

// execExit handles "exit N" by running it in a subshell wrapper so we can
// capture the exit code without killing our bash process.
func (s *Shell) execExit(cmd string, timeout time.Duration) (ExecResult, error) {
	// Run in a subshell: ( exit 42 ) captures the exit code without killing bash.
	wrapped := fmt.Sprintf("( %s ) > /tmp/_stdout 2>/tmp/_stderr; echo \"EXIT:$?\"\n", cmd)
	if _, err := fmt.Fprint(s.stdin, wrapped); err != nil {
		return ExecResult{}, fmt.Errorf("write to shell: %w", err)
	}
	return s.readSentinel(timeout)
}

// execNormal handles regular (non-exit) commands.
func (s *Shell) execNormal(cmd string, timeout time.Duration) (ExecResult, error) {
	wrapped := fmt.Sprintf("{ %s ; } > /tmp/_stdout 2>/tmp/_stderr; echo \"EXIT:$?\"\n", cmd)
	if _, err := fmt.Fprint(s.stdin, wrapped); err != nil {
		return ExecResult{}, fmt.Errorf("write to shell: %w", err)
	}
	return s.readSentinel(timeout)
}

// readSentinel reads the bash stdout scanner until EXIT:N is seen or timeout elapses.
func (s *Shell) readSentinel(timeout time.Duration) (ExecResult, error) {
	done := make(chan string, 1)
	go func() {
		for s.stdout.Scan() {
			line := s.stdout.Text()
			if strings.HasPrefix(line, "EXIT:") {
				done <- line
				return
			}
		}
		done <- "" // EOF — shell died
	}()

	var sentinel string
	select {
	case sentinel = <-done:
	case <-time.After(timeout):
		// Kill the runaway command by sending SIGINT to bash (Ctrl-C).
		fmt.Fprint(s.stdin, "\x03") // Ctrl-C
		// Drain the goroutine: send a fresh sentinel probe after bash recovers.
		go func() {
			time.Sleep(150 * time.Millisecond)
			fmt.Fprint(s.stdin, "echo EXIT:__DRAIN__\n")
		}()
		select {
		case <-done:
			// goroutine finished
		case <-time.After(3 * time.Second):
			// Give up draining
		}
		return ExecResult{}, fmt.Errorf("timeout after %s", timeout)
	}

	if sentinel == "" {
		return ExecResult{}, fmt.Errorf("shell died")
	}

	// Parse exit code from sentinel "EXIT:N".
	exitCode := 0
	if parts := strings.SplitN(sentinel, ":", 2); len(parts) == 2 {
		if parts[1] != "__DRAIN__" {
			exitCode, _ = strconv.Atoi(parts[1])
		}
	}

	stdout, _ := os.ReadFile("/tmp/_stdout")
	stderr, _ := os.ReadFile("/tmp/_stderr")

	return ExecResult{
		Stdout:   string(stdout),
		Stderr:   string(stderr),
		ExitCode: exitCode,
	}, nil
}

// Close kills the bash subprocess.
func (s *Shell) Close() {
	s.stdin.Close()
	s.cmd.Process.Kill()
	s.cmd.Wait()
}
