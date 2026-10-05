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
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

// nullCall is an NFS NULL call record; its reply is 24 bytes.
func nullCall(xid uint32) []byte {
	return withRecordMark(callBody(xid, progNFS, 0))
}

// ping sends a NULL call and waits for its reply.
func ping(t *testing.T, conn net.Conn) {
	t.Helper()
	if _, err := conn.Write(nullCall(1)); err != nil {
		t.Fatal(err)
	}
	if reply := readReply(t, conn); len(reply) != 24 {
		t.Fatalf("NULL reply of %d bytes", len(reply))
	}
}

// pipelinedReads is READ calls of the big test file, one after the other,
// with xids from 0.
func pipelinedReads(reads int, count uint32) []byte {
	call := withRecordMark(callBody(0, progNFS, procRead,
		xdrOpaque(isoHandle("casper", "big.squashfs")), xdrWords(0, 0, count)))
	calls := make([]byte, 0, reads*len(call))
	for xid := range uint32(reads) {
		binary.BigEndian.PutUint32(call[4:], xid)
		calls = append(calls, call...)
	}
	return calls
}

// mountEventually mounts an export, retrying while the server is still
// letting go of connections the test closed.
func mountEventually(t *testing.T, addr, export string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		_, err := mount(t, addr, export)
		if err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("mount %s: %v", export, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A host that opens connections and sits on them must not take every
// slot: past its limit its connections are closed at once.
func TestServeLimitsConnectionsPerHost(t *testing.T) {
	addr, _ := startServerWith(t, newTree(t), &Server{MaxConnectionsPerHost: 2})
	first := dialRaw(t, addr)
	second := dialRaw(t, addr)
	ping(t, first)
	ping(t, second)

	expectClosed(t, dialRaw(t, addr), time.Second)

	_ = first.Close()
	mountEventually(t, addr, "/iso")
	ping(t, second)
}

func TestServeLimitsConnectionsInTotal(t *testing.T) {
	addr, _ := startServerWith(t, newTree(t), &Server{MaxConnections: 1})
	first := dialRaw(t, addr)
	ping(t, first)
	expectClosed(t, dialRaw(t, addr), time.Second)
	_ = first.Close()
	mountEventually(t, addr, "/iso")
}

func TestServeAllowList(t *testing.T) {
	root := newTree(t)
	elsewhere := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	addr, _ := startServerWith(t, root, &Server{Allow: elsewhere})
	expectClosed(t, dialRaw(t, addr), time.Second)

	loopback := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("127.0.0.0/8")}
	addr, _ = startServerWith(t, root, &Server{Allow: loopback})
	mustMount(t, addr, "/iso")
}

// A peer that connects and sends nothing, or stops halfway through a
// request, is dropped after RequestTimeout, not after the idle timeout.
func TestServeRequestTimeout(t *testing.T) {
	addr, _ := startServerWith(t, newTree(t), &Server{RequestTimeout: 200 * time.Millisecond})

	expectClosed(t, dialRaw(t, addr), 2*time.Second)

	halfway := dialRaw(t, addr)
	ping(t, halfway)
	if _, err := halfway.Write(nullCall(2)[:20]); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, halfway, 2*time.Second)

	// Between requests the idle timeout applies.
	idle := dialRaw(t, addr)
	ping(t, idle)
	time.Sleep(500 * time.Millisecond)
	ping(t, idle)
}

func TestServeIdleTimeout(t *testing.T) {
	addr, _ := startServerWith(t, newTree(t), &Server{IdleTimeout: 200 * time.Millisecond})
	conn := dialRaw(t, addr)
	ping(t, conn)
	expectClosed(t, conn, 2*time.Second)

	addr, _ = startServerWith(t, newTree(t), &Server{})
	conn = dialRaw(t, addr)
	ping(t, conn)
	expectOpen(t, conn, 500*time.Millisecond)
}

// A peer that asks for data and does not read it is cut off after
// WriteTimeout, instead of holding the replies in memory.
func TestServeWriteTimeout(t *testing.T) {
	addr, _ := startServerWith(t, newTree(t), &Server{WriteTimeout: 200 * time.Millisecond})
	mustMount(t, addr, "/iso")

	conn := dialRaw(t, addr)
	if err := conn.(*net.TCPConn).SetReadBuffer(4096); err != nil {
		t.Fatal(err)
	}
	const reads = 64
	if _, err := conn.Write(pipelinedReads(reads, maxReadBytes)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(time.Second) // not reading: the server's writes block

	if err := conn.SetReadDeadline(time.Now().Add(10 * time.Second)); err != nil {
		t.Fatal(err)
	}
	received, err := io.Copy(io.Discard, conn)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Fatalf("connection still open after %d bytes", received)
	}
	t.Logf("received %d MiB of %d", received>>20, reads)
	if received >= reads*maxReadBytes {
		t.Errorf("received all %d bytes: the server never gave up on the stalled peer", received)
	}
}
