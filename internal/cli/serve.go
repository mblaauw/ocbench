package cli

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"mbl/ocbench/internal/config"
	"mbl/ocbench/internal/web"
)

// serveShutdownTimeout bounds graceful shutdown once the command context ends.
// It is long enough for an in-flight read to finish but short enough that
// Ctrl-C feels immediate.
const serveShutdownTimeout = 5 * time.Second

// newServeCmd builds `ocbench serve`: the read-only dashboard bound to a
// loopback address. It opens the persisted store, migrates it, and serves the
// embedded handler until the command context is cancelled (SIGINT/SIGTERM),
// then shuts the HTTP server down gracefully. It never mutates the store and
// never binds a non-loopback interface.
func newServeCmd(d Deps) *cobra.Command {
	var listen string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Serve the read-only dashboard on a loopback address",
		Long: "Serve the read-only benchmark dashboard. The listen address defaults to " +
			"config.server.listen (127.0.0.1:8787) and may be overridden with --listen. " +
			"Only loopback hosts (127.0.0.0/8, ::1 or localhost) are accepted because the " +
			"dashboard has no authentication. The server shuts down gracefully on " +
			"SIGINT/SIGTERM and never writes to the store.",
		Args: usageArgs(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			resolved, err := d.resolve()
			if err != nil {
				return err
			}

			addr := serveListen(listen, resolved.Config)
			if err := validateLoopback(addr); err != nil {
				return &UsageError{Err: err}
			}

			st, err := openHistoryStore(cmd.Context(), resolved)
			if err != nil {
				return err
			}
			defer st.Close()

			ln, err := net.Listen("tcp", addr)
			if err != nil {
				return fmt.Errorf("listen %s: %w", addr, err)
			}
			srv := &http.Server{Addr: addr, Handler: web.NewHandler(st, web.WithPaths(resolved.Paths))}

			fmt.Fprintf(cmd.OutOrStdout(), "ocbench dashboard listening on http://%s\n", ln.Addr())
			return serveHTTP(cmd.Context(), srv, ln)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", "", "loopback address to listen on (default config.server.listen)")
	return cmd
}

// serveListen resolves the effective listen address: an explicit --listen wins,
// otherwise config.server.listen.
func serveListen(flag string, cfg config.Config) string {
	if flag != "" {
		return flag
	}
	return cfg.Server.Listen
}

// validateLoopback rejects any address that is not a loopback host. It requires
// a host:port pair and accepts only 127.0.0.0/8, ::1 and the name localhost,
// because the dashboard has no authentication.
func validateLoopback(addr string) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid listen address %q: %w", addr, err)
	}
	if host == "" {
		return fmt.Errorf("listen address %q has no host", addr)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("listen host %q is not a loopback address", host)
	}
	return nil
}

// serveHTTP runs srv on ln until ctx is cancelled, then gracefully shuts the
// server down with a bounded context. A clean shutdown returns nil; a serve
// failure returns the underlying error.
func serveHTTP(ctx context.Context, srv *http.Server, ln net.Listener) error {
	errCh := make(chan error, 1)
	go func() { errCh <- srv.Serve(ln) }()

	select {
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), serveShutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		// A timeout means requests are still active: force the listener closed
		// so no port remains bound, then report the failure.
		_ = srv.Close()
		return fmt.Errorf("shutdown dashboard: %w", err)
	}
	// Serve returns http.ErrServerClosed once Shutdown has closed the listener.
	<-errCh
	return nil
}
