package cli

import (
	"context"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"mbl/ocbench/internal/config"
)

// serveWithListen returns a copy of cfg with server.listen replaced, so the
// resolution table can exercise config vs flag precedence without a real file.
func serveWithListen(cfg config.Config, listen string) config.Config {
	cfg.Server.Listen = listen
	return cfg
}

func TestServeCommandIsRegistered(t *testing.T) {
	root := NewRootWithDeps(Deps{})
	for _, c := range root.Commands() {
		if c.Name() != "serve" {
			continue
		}
		if c.Flags().Lookup("listen") == nil {
			t.Fatal("serve command is missing the --listen flag")
		}
		return
	}
	t.Fatal("serve command is not registered on the root command")
}

func TestServeListenResolution(t *testing.T) {
	cfg := config.DefaultsConfig()
	cases := []struct {
		name string
		flag string
		cfg  config.Config
		want string
	}{
		{name: "default from config", cfg: cfg, want: "127.0.0.1:8787"},
		{name: "config override", cfg: serveWithListen(cfg, "127.0.0.1:9999"), want: "127.0.0.1:9999"},
		{name: "flag overrides config", flag: "127.0.0.1:1234", cfg: serveWithListen(cfg, "127.0.0.1:9999"), want: "127.0.0.1:1234"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := serveListen(tc.flag, tc.cfg); got != tc.want {
				t.Fatalf("serveListen(%q, cfg) = %q, want %q", tc.flag, got, tc.want)
			}
		})
	}
}

func TestValidateLoopbackAcceptsLoopbackHosts(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:8787", "127.0.0.5:0", "[::1]:8787", "localhost:8787"} {
		if err := validateLoopback(addr); err != nil {
			t.Errorf("validateLoopback(%q) = %v, want nil", addr, err)
		}
	}
}

func TestValidateLoopbackRejectsNonLoopback(t *testing.T) {
	for _, addr := range []string{
		"0.0.0.0:8787", "[::]:8787", ":8787", "192.168.1.10:8787",
		"example.com:8787", "127.0.0.1", "8787",
	} {
		if err := validateLoopback(addr); err == nil {
			t.Errorf("validateLoopback(%q) = nil, want an error", addr)
		}
	}
}

// TestServeCommandRejectsNonLoopbackBeforeStore proves the guard runs before
// any listener or database side effect: a wildcard bind is a usage error and
// the configured store is never created.
func TestServeCommandRejectsNonLoopbackBeforeStore(t *testing.T) {
	d := cliTestDeps(t)
	cmd := newServeCmd(d)
	cmd.SilenceUsage = true
	cmd.SilenceErrors = true
	cmd.SetArgs([]string{"--listen", "0.0.0.0:8787"})

	err := cmd.ExecuteContext(context.Background())
	if err == nil {
		t.Fatal("serve --listen 0.0.0.0:8787: expected an error")
	}
	if !isUsageError(err) {
		t.Fatalf("serve error = %v, want a UsageError", err)
	}
	if _, statErr := os.Stat(d.Paths.DB); !os.IsNotExist(statErr) {
		t.Fatalf("store was opened before loopback validation (stat err = %v)", statErr)
	}
}

// TestServeHTTPServesAndShutsDownOnContextCancel exercises the lifecycle with an
// injected http.Server on an ephemeral loopback listener: the handler answers,
// command-context cancellation triggers a graceful shutdown, and the listener is
// released so no port remains bound.
func TestServeHTTPServesAndShutsDownOnContextCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()

	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, srv, ln) }()

	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		cancel()
		t.Fatalf("GET /: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serveHTTP = %v, want nil on graceful shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("serveHTTP did not return after context cancellation")
	}

	// The ephemeral port must be free again: no listener remains.
	again, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listener not released after shutdown: %v", err)
	}
	again.Close()
}

// TestServeHTTPGracefulShutdownWaitsForInflight proves Shutdown waits for an
// active request to finish rather than cutting it off.
func TestServeHTTPGracefulShutdownWaitsForInflight(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	started := make(chan struct{})
	release := make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		w.WriteHeader(http.StatusOK)
	})}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- serveHTTP(ctx, srv, ln) }()

	reqDone := make(chan error, 1)
	go func() {
		resp, err := http.Get("http://" + ln.Addr().String() + "/")
		if err == nil {
			resp.Body.Close()
		}
		reqDone <- err
	}()

	<-started
	cancel()
	close(release)

	if err := <-reqDone; err != nil {
		t.Fatalf("in-flight request failed during shutdown: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("serveHTTP = %v, want nil", err)
	}
}
