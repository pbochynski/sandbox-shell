package main

import (
	"strings"
	"testing"
	"time"
)

func TestShell_basicCommand(t *testing.T) {
	s, err := NewShell()
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	defer s.Close()

	res, err := s.Exec("echo hello", 5*time.Second)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello")
	}
	if res.ExitCode != 0 {
		t.Errorf("exitCode = %d, want 0", res.ExitCode)
	}
}

func TestShell_exitCode(t *testing.T) {
	s, err := NewShell()
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	defer s.Close()

	res, err := s.Exec("exit 42", 5*time.Second)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if res.ExitCode != 42 {
		t.Errorf("exitCode = %d, want 42", res.ExitCode)
	}
}

func TestShell_stderrSeparated(t *testing.T) {
	s, err := NewShell()
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	defer s.Close()

	res, err := s.Exec("echo out; echo err >&2", 5*time.Second)
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "out" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "out")
	}
	if strings.TrimSpace(res.Stderr) != "err" {
		t.Errorf("stderr = %q, want %q", res.Stderr, "err")
	}
}

func TestShell_persistentState(t *testing.T) {
	s, err := NewShell()
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	defer s.Close()

	if _, err := s.Exec("export MYVAR=hello123", 5*time.Second); err != nil {
		t.Fatalf("Exec set: %v", err)
	}
	res, err := s.Exec("echo $MYVAR", 5*time.Second)
	if err != nil {
		t.Fatalf("Exec get: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "hello123" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello123")
	}
}

func TestShell_timeout(t *testing.T) {
	s, err := NewShell()
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	defer s.Close()

	_, err = s.Exec("sleep 60", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
	if !strings.Contains(err.Error(), "timeout") {
		t.Errorf("error = %q, want it to contain 'timeout'", err.Error())
	}

	// Shell must be usable after a timeout (bash was restarted).
	res, err := s.Exec("echo after-timeout", 5*time.Second)
	if err != nil {
		t.Fatalf("Exec after timeout: %v", err)
	}
	if strings.TrimSpace(res.Stdout) != "after-timeout" {
		t.Errorf("stdout after restart = %q, want %q", res.Stdout, "after-timeout")
	}
}
