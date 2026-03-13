package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"regexp"
	"strconv"
	"syscall"
	"time"
)

var (
	macRegexp = regexp.MustCompile(`^([0-9a-fA-F]{2}-){5}[0-9a-fA-F]{2}$`)
	// Permits IPv6 bracket notation (e.g. [::1]) and hostnames; requires at least one alphanumeric.
	hostRegexp = regexp.MustCompile(`^[a-zA-Z0-9.\[\]:-]*[a-zA-Z0-9][a-zA-Z0-9.\[\]:-]*$`)
)

func main() {
	listenAddr := flag.String("listen-addr", ":8080", "address to listen on")
	flag.Parse()

	handler, err := conditionalBootHandler(*listenAddr)
	if err != nil {
		slog.Error("invalid listen address", "error", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /conditional-boot", handler)
	mux.HandleFunc("GET /healthz", healthzHandler)

	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		slog.Info("starting server", "addr", *listenAddr)
		if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-errCh:
		slog.Error("server error", "error", err)
		os.Exit(1)
	case <-quit:
	}

	slog.Info("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}

func conditionalBootHandler(listenAddr string) (http.HandlerFunc, error) {
	_, listenerPort, err := net.SplitHostPort(listenAddr)
	if err != nil {
		return nil, fmt.Errorf("parsing listen address %q: %w", listenAddr, err)
	}

	return func(w http.ResponseWriter, r *http.Request) {
		mac := r.URL.Query().Get("mac")
		if mac == "" {
			http.Error(w, "missing required parameter: mac", http.StatusBadRequest)
			return
		}
		if !macRegexp.MatchString(mac) {
			http.Error(w, "invalid mac address format", http.StatusBadRequest)
			return
		}

		host, port, source := resolveHostPort(r, listenerPort)
		if host == "" {
			http.Error(w, "missing Host header", http.StatusBadRequest)
			return
		}
		if !hostRegexp.MatchString(host) {
			http.Error(w, "invalid host", http.StatusBadRequest)
			return
		}
		portNum, err := strconv.Atoi(port)
		if err != nil || portNum < 1 || portNum > 65535 {
			http.Error(w, "invalid port", http.StatusBadRequest)
			return
		}

		slog.Info("conditional-boot request",
			"mac", mac,
			"host", host,
			"port", port,
			"source", source,
		)

		body := fmt.Sprintf(`#!ipxe
chain http://%s:%s/boot?mac=%s
`, host, port, mac)

		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, body)
	}, nil
}

func resolveHostPort(r *http.Request, listenerPort string) (host, port, source string) {
	fwdHost := r.Header.Get("X-Forwarded-Host")
	fwdPort := r.Header.Get("X-Forwarded-Port")

	// Strip port from X-Forwarded-Host if present (e.g. "proxy:443")
	if h, _, err := net.SplitHostPort(fwdHost); err == nil {
		fwdHost = h
	}

	if fwdHost != "" && fwdPort != "" {
		return fwdHost, fwdPort, "forwarded"
	}

	hostHeader := r.Host
	if hostHeader == "" {
		return "", "", ""
	}

	headerHost, _, err := net.SplitHostPort(hostHeader)
	if err != nil {
		// Host header has no port (e.g. just "example.com")
		headerHost = hostHeader
	}

	if fwdHost != "" {
		return fwdHost, listenerPort, "forwarded-partial"
	}
	if fwdPort != "" {
		return headerHost, fwdPort, "forwarded-partial"
	}

	return headerHost, listenerPort, "host-header"
}

func healthzHandler(w http.ResponseWriter, _ *http.Request) {
	body := "OK"
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	_, _ = fmt.Fprint(w, body)
}
