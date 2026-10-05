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
package nfsd

import (
	"bytes"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"testing"
	"time"
)

// peer is a connection that only has a remote address.
type peer struct {
	net.Conn
	addr net.Addr
}

func (p peer) RemoteAddr() net.Addr { return p.addr }

func peerAt(addrPort string) peer {
	return peer{addr: net.TCPAddrFromAddrPort(netip.MustParseAddrPort(addrPort))}
}

func quietWarnings() *throttledLog {
	return &throttledLog{log: testLogger(), interval: time.Hour}
}

func TestGateLimitsConnectionsPerHost(t *testing.T) {
	gate := newConnectionGate("test", nil, 100, 2, quietWarnings())
	first, ok := gate.admit(peerAt("10.0.0.5:1000"))
	if !ok {
		t.Fatal("first connection refused")
	}
	if _, ok := gate.admit(peerAt("10.0.0.5:1001")); !ok {
		t.Fatal("second connection refused")
	}
	if _, ok := gate.admit(peerAt("10.0.0.5:1002")); ok {
		t.Error("third connection from one host admitted")
	}
	// The same host over a dual-stack listener is still the same host.
	if _, ok := gate.admit(peerAt("[::ffff:10.0.0.5]:1003")); ok {
		t.Error("third connection from one host, IPv4-mapped, admitted")
	}
	if _, ok := gate.admit(peerAt("10.0.0.6:1000")); !ok {
		t.Error("another host refused while one host is at its limit")
	}

	first()
	first() // closing twice frees one slot, not two
	if _, ok := gate.admit(peerAt("10.0.0.5:1004")); !ok {
		t.Error("connection refused after one was released")
	}
	if _, ok := gate.admit(peerAt("10.0.0.5:1005")); ok {
		t.Error("released slot counted twice")
	}
}

func TestGateLimitsConnectionsInTotal(t *testing.T) {
	gate := newConnectionGate("test", nil, 3, 100, quietWarnings())
	addrs := []string{"10.0.0.1:1", "10.0.0.2:1", "10.0.0.3:1"}
	releases := make([]func(), 0, len(addrs))
	for _, addr := range addrs {
		release, ok := gate.admit(peerAt(addr))
		if !ok {
			t.Fatalf("%s refused", addr)
		}
		releases = append(releases, release)
	}
	if _, ok := gate.admit(peerAt("10.0.0.4:1")); ok {
		t.Error("connection over the total limit admitted")
	}
	releases[0]()
	if _, ok := gate.admit(peerAt("10.0.0.4:1")); !ok {
		t.Error("connection refused after one was released")
	}
}

func TestGateAllowList(t *testing.T) {
	allow := []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/24"), netip.MustParsePrefix("fd00::/64"), netip.MustParsePrefix("fe80::/64"),
	}
	gate := newConnectionGate("test", allow, 100, 100, quietWarnings())
	tests := []struct {
		conn net.Conn
		want bool
	}{
		{peerAt("10.0.0.5:700"), true},
		{peerAt("[::ffff:10.0.0.7]:700"), true},
		{peerAt("[fd00::1]:700"), true},
		// A link-local peer comes with the zone of its interface.
		{peerAt("[fe80::1%eth0]:700"), true},
		{peerAt("10.0.1.5:700"), false},
		{peerAt("127.0.0.1:700"), false},
		{peerAt("[fd00:0:0:1::1]:700"), false},
		{peer{addr: &net.UnixAddr{Name: "@x", Net: "unix"}}, false},
	}
	for _, tt := range tests {
		if _, ok := gate.admit(tt.conn); ok != tt.want {
			t.Errorf("%v: admitted %v, want %v", tt.conn.RemoteAddr(), ok, tt.want)
		}
	}

	open := newConnectionGate("test", nil, 100, 100, quietWarnings())
	if _, ok := open.admit(peerAt("192.0.2.1:700")); !ok {
		t.Error("an empty allow-list refused a connection")
	}
}

// A flood of refused connections must not flood the log.
func TestGateRefusalsAreRateLimited(t *testing.T) {
	var out bytes.Buffer
	warnings := &throttledLog{log: slog.New(slog.NewTextHandler(&out, nil)), interval: 200 * time.Millisecond}
	gate := newConnectionGate("test", []netip.Prefix{netip.MustParsePrefix("10.0.0.0/24")}, 100, 100, warnings)
	for range 50 {
		gate.admit(peerAt("192.0.2.1:700"))
	}
	if lines := strings.Count(out.String(), "\n"); lines != 1 {
		t.Fatalf("%d log lines for a burst of refusals, want 1:\n%s", lines, out.String())
	}
	if !strings.Contains(out.String(), "connection refused") || !strings.Contains(out.String(), "address not allowed") {
		t.Errorf("log does not say why: %s", out.String())
	}

	time.Sleep(250 * time.Millisecond)
	gate.admit(peerAt("192.0.2.1:700"))
	if !strings.Contains(out.String(), "suppressed=49") {
		t.Errorf("next warning does not count the suppressed ones:\n%s", out.String())
	}
}
