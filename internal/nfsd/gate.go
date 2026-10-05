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
	"log/slog"
	"net"
	"net/netip"
	"slices"
	"sync"
	"time"
)

// warningInterval is the shortest time between two warnings about
// refused connections or rejected requests: a flood of them must not
// flood the log as well.
const warningInterval = 5 * time.Second

// connectionGate decides which connections a listener serves: only those
// from allowed addresses, and no more than a limit in total and from any
// one address, so that one host cannot take every connection slot.
type connectionGate struct {
	server     string // "nfs" or "portmap", for the log
	allow      []netip.Prefix
	maxTotal   int
	maxPerHost int
	warnings   *throttledLog

	mu      sync.Mutex
	total   int
	perHost map[netip.Addr]int
}

func newConnectionGate(
	server string, allow []netip.Prefix, maxTotal, maxPerHost int, warnings *throttledLog,
) *connectionGate {
	return &connectionGate{
		server:     server,
		allow:      allow,
		maxTotal:   maxTotal,
		maxPerHost: maxPerHost,
		warnings:   warnings,
		perHost:    map[netip.Addr]int{},
	}
}

// admit reports whether a new connection may be served. If it may, the
// caller must call release once the connection is closed; release may be
// called more than once.
func (g *connectionGate) admit(conn net.Conn) (release func(), ok bool) {
	host, known := hostAddr(conn.RemoteAddr())
	if len(g.allow) > 0 && (!known || !slices.ContainsFunc(g.allow, func(p netip.Prefix) bool {
		return p.Contains(host)
	})) {
		g.refuse(conn, "address not allowed")
		return nil, false
	}

	g.mu.Lock()
	switch {
	case g.total >= g.maxTotal:
		g.mu.Unlock()
		g.refuse(conn, "too many connections")
		return nil, false
	case g.perHost[host] >= g.maxPerHost:
		g.mu.Unlock()
		g.refuse(conn, "too many connections from this address")
		return nil, false
	}
	g.total++
	g.perHost[host]++
	g.mu.Unlock()

	return sync.OnceFunc(func() {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.total--
		if g.perHost[host]--; g.perHost[host] == 0 {
			delete(g.perHost, host)
		}
	}), true
}

func (g *connectionGate) refuse(conn net.Conn, reason string) {
	g.warnings.warn("connection refused", "server", g.server,
		"client", conn.RemoteAddr().String(), "reason", reason)
}

// hostAddr returns the IP address of a TCP peer, with IPv4 addresses that
// a dual-stack listener reports as IPv6 ("::ffff:10.0.0.5") unmapped, and
// without the zone of a link-local address ("fe80::1%eth0"): a network
// never contains an address with a zone.
func hostAddr(addr net.Addr) (netip.Addr, bool) {
	tcp, ok := addr.(*net.TCPAddr)
	if !ok {
		return netip.Addr{}, false
	}
	return tcp.AddrPort().Addr().Unmap().WithZone(""), true
}

// throttledLog logs warnings, at most one per interval. The next warning
// it lets through says how many it suppressed in between.
type throttledLog struct {
	log      *slog.Logger
	interval time.Duration

	mu         sync.Mutex
	last       time.Time
	suppressed int
}

func (t *throttledLog) warn(msg string, args ...any) {
	t.mu.Lock()
	now := time.Now()
	if !t.last.IsZero() && now.Sub(t.last) < t.interval {
		t.suppressed++
		t.mu.Unlock()
		return
	}
	suppressed := t.suppressed
	t.last, t.suppressed = now, 0
	t.mu.Unlock()

	if suppressed > 0 {
		args = append(args, "suppressed", suppressed)
	}
	t.log.Warn(msg, args...)
}

// orDefault returns value, or fallback if value is the zero value.
func orDefault[T comparable](value, fallback T) T {
	var zero T
	if value == zero {
		return fallback
	}
	return value
}
