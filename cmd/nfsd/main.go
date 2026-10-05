// Command nfsd is a small read-only NFS server for network installs.
//
// It serves the immediate subdirectories of --root as NFS version 3
// exports named "/<name>", with the MOUNT protocol on the same TCP port,
// and answers port mapper GETPORT calls so that clients which cannot be
// told the port (klibc nfsmount in the Ubuntu installer) find it.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/isoboot/isoboot/internal/nfsd"
)

// portmapTimeout bounds each port mapper call. klibc sends its 56-byte
// GETPORT as soon as it has connected, so a peer that has not sent a call
// within this time is not an installer and gives its slot back quickly.
const portmapTimeout = 2 * time.Second

func main() {
	listenAddr := flag.String("listen", ":2049", "TCP address for NFS and MOUNT")
	portmapAddr := flag.String("portmap-listen", ":111", "TCP address for the port mapper; empty disables it")
	root := flag.String("root", "", "directory whose immediate subdirectories are exported read-only")
	concurrency := flag.Int("concurrent-handlers", 8, "requests handled at the same time per connection")
	logLevel := flag.String("log-level", "info", "log level: info, debug, or trace (logs every request)")
	flag.Parse()

	level, err := parseLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	nfsd.UseLogger(logger)

	if err := run(logger, *root, *listenAddr, *portmapAddr, *concurrency); err != nil {
		slog.Error("nfsd failed", "error", err)
		os.Exit(1)
	}
}

func parseLevel(s string) (slog.Level, error) {
	switch s {
	case "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "trace":
		return nfsd.LevelTrace, nil
	}
	return 0, fmt.Errorf("invalid --log-level %q: want info, debug or trace", s)
}

func run(logger *slog.Logger, root, listenAddr, portmapAddr string, concurrency int) error {
	if root == "" {
		return errors.New("--root is required")
	}
	handler, err := nfsd.NewHandler(root, logger)
	if err != nil {
		return err
	}
	defer func() { _ = handler.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	errCh := make(chan error, 2)

	// The NFS listener is serving before the port mapper starts to hand
	// out its port: a client must never be sent to a port nobody answers.
	nfsListener, err := net.Listen("tcp", listenAddr)
	if err != nil {
		return fmt.Errorf("listen for NFS: %w", err)
	}
	defer func() { _ = nfsListener.Close() }()
	port := nfsListener.Addr().(*net.TCPAddr).Port
	server := &nfsd.Server{
		Handler:            handler,
		ConcurrentHandlers: concurrency,
		IdleTimeout:        nfsd.DefaultIdleTimeout,
		WriteTimeout:       nfsd.DefaultWriteTimeout,
	}
	go func() { errCh <- fmt.Errorf("NFS server: %w", server.Serve(ctx, nfsListener)) }()
	slog.Info("serving NFS and MOUNT", "addr", nfsListener.Addr().String(),
		"root", root, "exports", handler.Exports())

	if portmapAddr != "" {
		pmListener, err := net.Listen("tcp", portmapAddr)
		if err != nil {
			return fmt.Errorf("listen for port mapper: %w", err)
		}
		defer func() { _ = pmListener.Close() }()
		portmap := &nfsd.Portmap{Port: uint32(port), Timeout: portmapTimeout, Log: logger}
		go func() { errCh <- fmt.Errorf("port mapper: %w", portmap.Serve(pmListener)) }()
		slog.Info("serving port mapper", "addr", pmListener.Addr().String(), "nfsPort", port)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	select {
	case err := <-errCh:
		return err
	case <-quit:
		slog.Info("shutting down")
		return nil
	}
}
