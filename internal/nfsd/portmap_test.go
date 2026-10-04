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
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"
)

// getportCall builds a GETPORT call record as klibc sends it: AUTH_NULL
// credentials and verifier, then program, version, protocol and port 0.
func getportCall(xid, rpcVers, prog, vers, proc uint32, args ...uint32) []byte {
	words := append([]uint32{xid, rpcCall, rpcVers, prog, vers, proc, 0, 0, 0, 0}, args...)
	out := binary.BigEndian.AppendUint32(nil, lastFragment|uint32(4*len(words)))
	for _, w := range words {
		out = binary.BigEndian.AppendUint32(out, w)
	}
	return out
}

func newPortmap() *Portmap {
	return &Portmap{Port: 2049, Timeout: 5 * time.Second, Log: testLogger()}
}

// exchange sends one call over an in-memory connection and returns what a
// single Read yields. net.Pipe hands over each Write on its own, so a
// reply sent in two writes would come back short, just as klibc sees it.
func exchange(t *testing.T, p *Portmap, call []byte) []byte {
	t.Helper()
	client, server := net.Pipe()
	t.Cleanup(func() { _ = client.Close() })
	go p.serveConn(server)
	if err := client.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Write(call); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 512)
	n, err := client.Read(buf)
	if err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return buf[:n]
}

func TestPortmapGetPort(t *testing.T) {
	const xid = 0xdeadbeef
	reply := func(hi, lo byte) []byte {
		return []byte{
			0x80, 0, 0, 28, // record mark: last fragment, 28 bytes
			0xde, 0xad, 0xbe, 0xef, // xid
			0, 0, 0, 1, // REPLY
			0, 0, 0, 0, // MSG_ACCEPTED
			0, 0, 0, 0, 0, 0, 0, 0, // verifier: AUTH_NULL, length 0
			0, 0, 0, 0, // SUCCESS
			0, 0, hi, lo, // port
		}
	}
	tests := []struct {
		name              string
		prog, vers, proto uint32
		want              []byte
	}{
		{"nfs v3 tcp", 100003, 3, 6, reply(0x08, 0x01)},
		{"mount v3 tcp", 100005, 3, 6, reply(0x08, 0x01)},
		{"nfs v4 tcp", 100003, 4, 6, reply(0, 0)},
		{"nfs v2 tcp", 100003, 2, 6, reply(0, 0)},
		{"nfs v3 udp", 100003, 3, 17, reply(0, 0)},
		{"mount v3 udp", 100005, 3, 17, reply(0, 0)},
		{"mount v1 tcp", 100005, 1, 6, reply(0, 0)},
		{"nlockmgr", 100021, 4, 6, reply(0, 0)},
		{"status", 100024, 1, 6, reply(0, 0)},
		{"portmapper itself", 100000, 2, 6, reply(0, 0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call := getportCall(xid, 2, progPortmap, 2, procGetPort, tt.prog, tt.vers, tt.proto, 0)
			got := exchange(t, newPortmap(), call)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("reply in one read:\n got %x\nwant %x", got, tt.want)
			}
		})
	}
}

func TestPortmapOtherCalls(t *testing.T) {
	const xid = 7
	accept := func(stat uint32, extra ...uint32) []byte {
		return accepted(xid, stat, extra...)
	}
	tests := []struct {
		name string
		call []byte
		want []byte
	}{
		{"null", getportCall(xid, 2, progPortmap, 2, procNull), accept(acceptSuccess)},
		{"set", getportCall(xid, 2, progPortmap, 2, 1, 100003, 3, 6, 9), accept(acceptProcUnavail)},
		{"dump", getportCall(xid, 2, progPortmap, 2, 4), accept(acceptProcUnavail)},
		{"rpcbind v4", getportCall(xid, 2, progPortmap, 4, 3), accept(acceptProgMismatch, 2, 2)},
		{"other program", getportCall(xid, 2, progNFS, 3, 0), accept(acceptProgUnavail)},
		{"short args", getportCall(xid, 2, progPortmap, 2, procGetPort, 100003, 3), accept(acceptGarbageArgs)},
		{"rpc version 3", getportCall(xid, 3, progPortmap, 2, procGetPort, 100003, 3, 6, 0),
			record(xid, rpcDenied, rpcMismatch, 2, 2)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := exchange(t, newPortmap(), tt.call)
			if !bytes.Equal(got, tt.want) {
				t.Errorf("reply:\n got %x\nwant %x", got, tt.want)
			}
		})
	}
}

// A call with AUTH_UNIX credentials, as mount.nfs and the kernel send.
func TestPortmapSkipsCredentials(t *testing.T) {
	cred := []uint32{1, 20, 0, 0, 0, 0, 0} // AUTH_UNIX, 20 bytes: stamp, name "", uid, gid, no gids
	words := append([]uint32{1, rpcCall, 2, progPortmap, 2, procGetPort}, cred...)
	words = append(words, 0, 0, progMount, 3, protoTCP, 0)
	call := binary.BigEndian.AppendUint32(nil, lastFragment|uint32(4*len(words)))
	for _, w := range words {
		call = binary.BigEndian.AppendUint32(call, w)
	}
	got := exchange(t, newPortmap(), call)
	if want := accepted(1, acceptSuccess, 2049); !bytes.Equal(got, want) {
		t.Errorf("reply:\n got %x\nwant %x", got, want)
	}
}

// A peer that connects and then misbehaves must be disconnected: klibc
// has no timeout of its own, and neither may a stuck peer pin the server.
func TestPortmapClosesBadConnections(t *testing.T) {
	notACall := getportCall(1, 2, progPortmap, 2, procGetPort, 100003, 3, 6, 0)
	binary.BigEndian.PutUint32(notACall[8:], rpcReply)
	tests := []struct {
		name string
		send []byte
	}{
		{"silent peer", nil},
		{"half a record mark", []byte{0x80, 0}},
		{"oversized record", []byte{0x80, 0xff, 0xff, 0xff}},
		{"undersized record", []byte{0x80, 0, 0, 8, 1, 2, 3, 4, 5, 6, 7, 8}},
		{"fragmented record", []byte{0, 0, 0, 56}},
		{"truncated call", getportCall(1, 2, progPortmap, 2, procGetPort, 100003, 3, 6, 0)[:30]},
		{"not a call", notACall},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newPortmap()
			p.Timeout = 100 * time.Millisecond
			client, server := net.Pipe()
			defer func() { _ = client.Close() }()
			done := make(chan struct{})
			go func() {
				p.serveConn(server)
				close(done)
			}()
			go func() { _, _ = client.Write(tt.send) }()
			if err := client.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
				t.Fatal(err)
			}
			if n, err := client.Read(make([]byte, 64)); err != io.EOF {
				t.Errorf("read = %d, %v; want the connection closed without a reply", n, err)
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				t.Error("connection handler still running")
			}
		})
	}
}

// Over real TCP, the way klibc does it: one connection per lookup.
func TestPortmapServe(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	served := make(chan error, 1)
	go func() { served <- newPortmap().Serve(listener) }()

	for _, prog := range []uint32{progNFS, progMount} {
		conn, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(getportCall(prog, 2, progPortmap, 2, procGetPort, prog, 3, protoTCP, 0)); err != nil {
			t.Fatal(err)
		}
		got := make([]byte, 32)
		if _, err := io.ReadFull(conn, got); err != nil {
			t.Fatal(err)
		}
		if want := accepted(prog, acceptSuccess, 2049); !bytes.Equal(got, want) {
			t.Errorf("program %d:\n got %x\nwant %x", prog, got, want)
		}
		_ = conn.Close()
	}

	_ = listener.Close()
	if err := <-served; err != nil {
		t.Errorf("Serve after close = %v, want nil", err)
	}
}
