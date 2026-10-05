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
	"net/netip"
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
	var allow cidrList
	flag.Var(&allow, "allow-cidr", "network (CIDR) clients may connect from, for NFS, MOUNT and the port mapper; "+
		"repeat for more; none allows every address")
	flag.Parse()

	level, err := parseLevel(*logLevel)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level}))
	slog.SetDefault(logger)
	nfsd.UseLogger(logger)

	if err := run(logger, *root, *listenAddr, *portmapAddr, *concurrency, allow); err != nil {
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

// cidrList is a repeatable flag of networks.
type cidrList []netip.Prefix

func (l *cidrList) String() string {
	return fmt.Sprint([]netip.Prefix(*l))
}

func (l *cidrList) Set(s string) error {
	prefix, err := netip.ParsePrefix(s)
	if err != nil {
		return fmt.Errorf("want a network such as 10.0.0.0/24 (a single address is 10.0.0.5/32): %w", err)
	}
	*l = append(*l, prefix.Masked())
	return nil
}

func run(logger *slog.Logger, root, listenAddr, portmapAddr string, concurrency int, allow []netip.Prefix) error {
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
		Allow:              allow,
		Log:                logger,
	}
	go func() { errCh <- fmt.Errorf("NFS server: %w", server.Serve(ctx, nfsListener)) }()
	slog.Info("serving NFS and MOUNT", "addr", nfsListener.Addr().String(),
		"root", root, "exports", handler.Exports(), "allow", allowedNetworks(allow))

	if portmapAddr != "" {
		pmListener, err := net.Listen("tcp", portmapAddr)
		if err != nil {
			return fmt.Errorf("listen for port mapper: %w", err)
		}
		defer func() { _ = pmListener.Close() }()
		portmap := &nfsd.Portmap{Port: uint32(port), Timeout: portmapTimeout, Allow: allow, Log: logger}
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

// allowedNetworks describes the allow-list for the log.
func allowedNetworks(allow []netip.Prefix) string {
	if len(allow) == 0 {
		return "any address"
	}
	return fmt.Sprint(allow)
}
