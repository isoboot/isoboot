/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

	http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/
package main

import (
	"context"
	"encoding/binary"
	"errors"
	"flag"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"
)

func parseAllowCIDR(args ...string) (cidrList, error) {
	flags := flag.NewFlagSet("nfsd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var allow cidrList
	flags.Var(&allow, "allow-cidr", "")
	return allow, flags.Parse(args)
}

func TestAllowCIDRFlag(t *testing.T) {
	allow, err := parseAllowCIDR("--allow-cidr=192.168.101.0/24", "--allow-cidr=10.0.0.7/16", "--allow-cidr=fd00::/64")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("192.168.101.0/24"),
		netip.MustParsePrefix("10.0.0.0/16"), // host bits dropped
		netip.MustParsePrefix("fd00::/64"),
	}
	if !slices.Equal(allow, want) {
		t.Errorf("allow = %v, want %v", allow, want)
	}

	if allow, err := parseAllowCIDR(); err != nil || len(allow) != 0 {
		t.Errorf("no flag: allow = %v, %v; want empty", allow, err)
	}

	// A bare address or a typo must stop nfsd, not open it to everyone.
	for _, bad := range []string{"192.168.101.5", "192.168.101.0/33", "subnet", ""} {
		if _, err := parseAllowCIDR("--allow-cidr=" + bad); err == nil {
			t.Errorf("--allow-cidr=%q accepted", bad)
		}
	}
}

// freeAddr returns a loopback address with a port nothing listens on.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

// startRun runs nfsd on free loopback ports until the test ends, and
// returns the NFS and port mapper addresses.
func startRun(t *testing.T, allow []netip.Prefix) (nfsAddr, portmapAddr string) {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "iso"), 0o755); err != nil {
		t.Fatal(err)
	}
	nfsAddr, portmapAddr = freeAddr(t), freeAddr(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	go func() { done <- run(ctx, logger, root, nfsAddr, portmapAddr, 4, allow) }()
	t.Cleanup(func() {
		cancel()
		if err := <-done; err != nil {
			t.Errorf("run: %v", err)
		}
	})
	return nfsAddr, portmapAddr
}

// dial connects to addr, waiting for nfsd to start listening.
func dial(t *testing.T, addr string) net.Conn {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		conn, err := net.Dial("tcp", addr)
		if err == nil {
			t.Cleanup(func() { _ = conn.Close() })
			return conn
		}
		if time.Now().After(deadline) {
			t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// closedWithin reports whether the server closes conn within the given
// time without sending anything.
func closedWithin(t *testing.T, conn net.Conn, within time.Duration) bool {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(within)); err != nil {
		t.Fatal(err)
	}
	_, err := conn.Read(make([]byte, 64))
	var netErr net.Error
	return err != nil && (!errors.As(err, &netErr) || !netErr.Timeout())
}

// The networks given with --allow-cidr apply to NFS and to the port
// mapper: connections from elsewhere are closed at once.
func TestRunServesOnlyAllowedNetworks(t *testing.T) {
	nfsAddr, portmapAddr := startRun(t, []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")})
	for _, addr := range []string{nfsAddr, portmapAddr} {
		if !closedWithin(t, dial(t, addr), time.Second) {
			t.Errorf("%s: connection from 127.0.0.1 not closed", addr)
		}
	}

	nfsAddr, _ = startRun(t, []netip.Prefix{netip.MustParsePrefix("127.0.0.0/8")})
	conn := dial(t, nfsAddr)
	// An NFS NULL call: xid, CALL, RPC version 2, NFS, version 3, NULL,
	// AUTH_NULL credentials and verifier.
	call := []byte{0x80, 0, 0, 40}
	for _, word := range []uint32{1, 0, 2, 100003, 3, 0, 0, 0, 0, 0} {
		call = binary.BigEndian.AppendUint32(call, word)
	}
	if _, err := conn.Write(call); err != nil {
		t.Fatal(err)
	}
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, make([]byte, 28)); err != nil {
		t.Errorf("NULL call from an allowed network: %v", err)
	}
}

// klibc sends its GETPORT as soon as it has connected, so a peer that has
// sent nothing for two seconds is not an installer: it must give its port
// mapper slot back rather than hold it.
func TestRunDropsSilentPortmapPeers(t *testing.T) {
	_, portmapAddr := startRun(t, nil)
	if !closedWithin(t, dial(t, portmapAddr), 3*time.Second) {
		t.Error("silent port mapper peer still connected after 3s")
	}
}
