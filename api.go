package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"
)

type execRequest struct {
	Command string  `json:"command"`
	Timeout float64 `json:"timeout"` // seconds; 0 → default 30s
}

// NewAPIHandler returns an http.Handler serving POST /exec and GET /health.
func NewAPIHandler(s *Shell) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("POST /exec", func(w http.ResponseWriter, r *http.Request) {
		var req execRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request: "+err.Error(), http.StatusBadRequest)
			return
		}
		timeout := time.Duration(req.Timeout * float64(time.Second))
		if timeout <= 0 {
			timeout = 30 * time.Second
		}
		res, err := s.Exec(req.Command, timeout)
		if err != nil {
			if strings.Contains(err.Error(), "timeout") {
				http.Error(w, err.Error(), http.StatusGatewayTimeout)
				return
			}
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(res)
	})
	return mux
}
