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

// newBashProcess starts a fresh bash subprocess and returns its pieces.
// Callers must close pw and stdinR after cmd.Start() succeeds.
func newBashProcess() (cmd *exec.Cmd, stdinW io.WriteCloser, scanner *bufio.Scanner, err error) {
	pr, pw, err := os.Pipe()
	if err != nil {
		return nil, nil, nil, fmt.Errorf("pipe: %w", err)
	}

	stdinR, stdinW, err := os.Pipe()
	if err != nil {
		pr.Close()
		pw.Close()
		return nil, nil, nil, fmt.Errorf("stdin pipe: %w", err)
	}

	cmd = exec.Command("bash", "--norc", "--noprofile")
	cmd.Stdin = stdinR
	cmd.Stdout = pw
	cmd.Stderr = pw // initial stderr goes to same pipe; redirected per-command

	if err = cmd.Start(); err != nil {
		pr.Close()
		pw.Close()
		stdinR.Close()
		stdinW.Close()
		return nil, nil, nil, fmt.Errorf("start bash: %w", err)
	}
	pw.Close()     // parent doesn't write to stdout pipe
	stdinR.Close() // child owns the read end

	sc := bufio.NewScanner(pr)
	sc.Buffer(make([]byte, 1<<20), 1<<20) // raise limit to 1 MiB per line
	return cmd, stdinW, sc, nil
}

// NewShell starts a bash subprocess and returns a ready Shell.
func NewShell() (*Shell, error) {
	cmd, stdinW, sc, err := newBashProcess()
	if err != nil {
		return nil, err
	}
	return &Shell{
		cmd:    cmd,
		stdin:  stdinW,
		stdout: sc,
	}, nil
}

// restartBash kills the current bash process, waits for it to exit, then
// starts a fresh one. It must be called with s.mu held.
func (s *Shell) restartBash() error {
	// Close stdin first so bash sees EOF, then kill it to be sure.
	s.stdin.Close()
	s.cmd.Process.Kill()
	s.cmd.Wait()

	cmd, stdinW, sc, err := newBashProcess()
	if err != nil {
		return fmt.Errorf("restart bash: %w", err)
	}
	s.cmd = cmd
	s.stdin = stdinW
	s.stdout = sc
	return nil
}

// Exec runs cmd in the persistent bash shell and returns its output.
// exit N commands are wrapped in a subshell so bash itself never exits.
// Returns error only on timeout (shell is restarted) or unrecoverable death.
// Non-zero exit codes are not errors.
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
	// TODO: use per-shell unique temp files to support concurrent Shell instances
	wrapped := fmt.Sprintf("( %s ) > /tmp/_stdout 2>/tmp/_stderr; echo \"EXIT:$?\"\n", cmd)
	if _, err := fmt.Fprint(s.stdin, wrapped); err != nil {
		return ExecResult{}, fmt.Errorf("write to shell: %w", err)
	}
	return s.readSentinel(timeout)
}

// execNormal handles regular (non-exit) commands.
func (s *Shell) execNormal(cmd string, timeout time.Duration) (ExecResult, error) {
	// TODO: use per-shell unique temp files to support concurrent Shell instances
	wrapped := fmt.Sprintf("{ %s ; } > /tmp/_stdout 2>/tmp/_stderr; echo \"EXIT:$?\"\n", cmd)
	if _, err := fmt.Fprint(s.stdin, wrapped); err != nil {
		return ExecResult{}, fmt.Errorf("write to shell: %w", err)
	}
	return s.readSentinel(timeout)
}

// readSentinel reads the bash stdout scanner until EXIT:N is seen or timeout elapses.
// On timeout, it kills and restarts bash so the shell is usable for future commands.
func (s *Shell) readSentinel(timeout time.Duration) (ExecResult, error) {
	// Capture the current scanner into a local so the goroutine doesn't race
	// against restartBash replacing s.stdout.
	sc := s.stdout
	done := make(chan string, 1)
	go func() {
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "EXIT:") {
				done <- line
				return
			}
		}
		// EOF (bash died) or scanner error — either way signal the caller.
		done <- "" // empty string means "no sentinel received"
	}()

	var sentinel string
	select {
	case sentinel = <-done:
	case <-time.After(timeout):
		// Kill bash — the scanner goroutine will get EOF and send "" to done.
		if err := s.restartBash(); err != nil {
			// Wait for the goroutine to drain on the old pipe before returning.
			<-done
			return ExecResult{}, fmt.Errorf("restart after timeout: %w", err)
		}
		<-done // wait for scanner goroutine to exit (old bash is dead)
		return ExecResult{}, fmt.Errorf("timeout after %s", timeout)
	}

	if sentinel == "" {
		return ExecResult{}, fmt.Errorf("shell died")
	}

	// Parse exit code from sentinel "EXIT:N".
	exitCode := 0
	if parts := strings.SplitN(sentinel, ":", 2); len(parts) == 2 {
		exitCode, _ = strconv.Atoi(parts[1])
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
