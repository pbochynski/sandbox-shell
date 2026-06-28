package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	// Start ttyd on port 7681 for interactive browser terminal.
	ttyd := exec.CommandContext(ctx, "ttyd", "-p", "7681", "-W", "bash")
	ttyd.Stdout = os.Stdout
	ttyd.Stderr = os.Stderr
	if err := ttyd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "start ttyd: %v\n", err)
		os.Exit(1)
	}
	go func() {
		if err := ttyd.Wait(); err != nil && ctx.Err() == nil {
			fmt.Fprintf(os.Stderr, "ttyd exited unexpectedly: %v — shutting down\n", err)
			cancel()
		}
	}()

	// Start persistent shell for the HTTP API.
	shell, err := NewShell()
	if err != nil {
		fmt.Fprintf(os.Stderr, "start shell: %v\n", err)
		os.Exit(1)
	}
	defer shell.Close()

	// Serve HTTP API on port 7682.
	srv := &http.Server{
		Addr:    ":7682",
		Handler: NewAPIHandler(shell),
	}
	go func() {
		<-ctx.Done()
		shutCtx, shutCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutCancel()
		srv.Shutdown(shutCtx)
	}()

	fmt.Println("sandbox-shell listening on :7682 (API), ttyd on :7681 (terminal)")
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		fmt.Fprintf(os.Stderr, "serve: %v\n", err)
		os.Exit(1)
	}
}

