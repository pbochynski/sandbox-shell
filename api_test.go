package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	s, err := NewShell()
	if err != nil {
		t.Fatalf("NewShell: %v", err)
	}
	t.Cleanup(s.Close)
	return NewAPIHandler(s)
}

func TestAPI_health(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("body = %q, want %q", w.Body.String(), "ok")
	}
}

func TestAPI_exec_happy(t *testing.T) {
	h := newTestHandler(t)
	body, _ := json.Marshal(map[string]any{"command": "echo hello"})
	req := httptest.NewRequest("POST", "/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	var res ExecResult
	if err := json.NewDecoder(w.Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.ExitCode != 0 {
		t.Errorf("exitCode = %d, want 0", res.ExitCode)
	}
	if res.Stdout != "hello\n" {
		t.Errorf("stdout = %q, want %q", res.Stdout, "hello\n")
	}
}

func TestAPI_exec_nonzero_exit_still_200(t *testing.T) {
	h := newTestHandler(t)
	body, _ := json.Marshal(map[string]any{"command": "false"})
	req := httptest.NewRequest("POST", "/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 even for non-zero exit", w.Code)
	}
	var res ExecResult
	json.NewDecoder(w.Body).Decode(&res)
	if res.ExitCode != 1 {
		t.Errorf("exitCode = %d, want 1", res.ExitCode)
	}
}

func TestAPI_exec_bad_json(t *testing.T) {
	h := newTestHandler(t)
	req := httptest.NewRequest("POST", "/exec", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", w.Code)
	}
}

func TestAPI_exec_timeout(t *testing.T) {
	h := newTestHandler(t)
	body, _ := json.Marshal(map[string]any{"command": "sleep 60", "timeout": 0.1})
	req := httptest.NewRequest("POST", "/exec", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusGatewayTimeout {
		t.Errorf("status = %d, want 504", w.Code)
	}
}
