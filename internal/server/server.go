package server

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/apache261/Shinmon/internal/health"
)

type Options struct {
	Addr            string
	Handler         http.Handler
	Logger          *slog.Logger
	Readiness       *health.Readiness
	ShutdownTimeout time.Duration
	TLSCertFile     string
	TLSKeyFile      string
}

func Run(ctx context.Context, options Options) error {
	listener, err := net.Listen("tcp", options.Addr)
	if err != nil {
		return err
	}
	server := &http.Server{
		Addr:              options.Addr,
		Handler:           options.Handler,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if options.TLSCertFile != "" {
		certificate, loadErr := tls.LoadX509KeyPair(options.TLSCertFile, options.TLSKeyFile)
		if loadErr != nil {
			_ = listener.Close()
			return errors.New("load server TLS certificate")
		}
		server.TLSConfig = &tls.Config{Certificates: []tls.Certificate{certificate}, MinVersion: tls.VersionTLS12}
		listener = tls.NewListener(listener, server.TLSConfig)
	}
	return Serve(ctx, server, listener, options.Readiness, options.ShutdownTimeout, options.Logger)
}

// Serve owns readiness transitions and graceful shutdown for an HTTP server.
func Serve(ctx context.Context, httpServer *http.Server, listener net.Listener, readiness *health.Readiness, shutdownTimeout time.Duration, logger *slog.Logger) error {
	if logger == nil {
		logger = slog.Default()
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- httpServer.Serve(listener)
	}()

	readiness.Set(true)
	logger.Info("http server started", "address", listener.Addr().String())

	select {
	case err := <-serveResult:
		readiness.Set(false)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		readiness.Set(false)
	}

	logger.Info("http server shutting down")
	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	shutdownErr := httpServer.Shutdown(shutdownContext)
	if shutdownErr != nil {
		_ = httpServer.Close()
	}
	serveErr := <-serveResult
	if shutdownErr != nil {
		return shutdownErr
	}
	if serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
		return serveErr
	}
	return nil
}
