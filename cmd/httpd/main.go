package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	listenAddr := flag.String("listen-addr", ":8080", "address to listen on")
	flag.Parse()

	mux := http.NewServeMux()
	mux.HandleFunc("/conditional-boot", conditionalBootHandler(*listenAddr))
	mux.HandleFunc("/healthz", healthzHandler)

	srv := &http.Server{
		Addr:         *listenAddr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		slog.Info("starting server", "addr", *listenAddr)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM)
	<-quit

	slog.Info("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := srv.Shutdown(ctx); err != nil {
		slog.Error("shutdown error", "error", err)
	}
}

func conditionalBootHandler(listenAddr string) http.HandlerFunc {
	_, listenerPort, _ := net.SplitHostPort(listenAddr)

	return func(w http.ResponseWriter, r *http.Request) {
		mac := r.URL.Query().Get("mac")
		if mac == "" {
			http.Error(w, "missing required parameter: mac", http.StatusBadRequest)
			return
		}

		host, port, source := resolveHostPort(r, listenerPort)
		if host == "" {
			http.Error(w, "missing Host header", http.StatusBadRequest)
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
	}
}

func resolveHostPort(r *http.Request, listenerPort string) (host, port, source string) {
	fwdHost := r.Header.Get("X-Forwarded-Host")
	fwdPort := r.Header.Get("X-Forwarded-Port")

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
