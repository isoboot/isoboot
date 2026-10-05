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
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"
)

// replyRecord builds a reply record of the given body size, starting with
// its xid.
func replyRecord(xid uint32, size int) []byte {
	record := binary.BigEndian.AppendUint32(nil, lastFragment|uint32(size))
	body := make([]byte, size)
	if size >= 4 {
		binary.BigEndian.PutUint32(body, xid)
	}
	return append(record, body...)
}

func TestReplyStream(t *testing.T) {
	large := replyRecord(3, 1<<20)
	tests := []struct {
		name   string
		writes [][]byte
		want   []uint32
	}{
		{"one reply", [][]byte{replyRecord(1, 24)}, []uint32{1}},
		// go-nfs writes a large reply as its 4 KiB buffer and then the rest.
		{"large reply", [][]byte{large[:4096], large[4096 : 1<<19], large[1<<19:]}, []uint32{3}},
		{"two replies in one write", [][]byte{append(replyRecord(1, 24), replyRecord(2, 120)...)}, []uint32{1, 2}},
		{"split record mark", [][]byte{replyRecord(4, 24)[:2], replyRecord(4, 24)[2:6], replyRecord(4, 24)[6:]}, []uint32{4}},
		{"reply without an xid", [][]byte{replyRecord(0, 0), replyRecord(5, 24)}, []uint32{5}},
		{"unfinished reply", [][]byte{replyRecord(6, 24), large[:4096]}, []uint32{6}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stream replyStream
			var answered []uint32
			for _, write := range tt.writes {
				stream.written(write, func(xid uint32) { answered = append(answered, xid) })
			}
			if !slices.Equal(answered, tt.want) {
				t.Errorf("answered %v, want %v", answered, tt.want)
			}
		})
	}

	var stream replyStream
	if xid, ok := stream.atReplyStart(replyRecord(7, 24)); !ok || xid != 7 {
		t.Errorf("atReplyStart = %d, %v; want 7, true", xid, ok)
	}
	stream.written(large[:4096], func(uint32) {})
	if _, ok := stream.atReplyStart(large[4096:]); ok {
		t.Error("the middle of a reply taken for the start of one")
	}
}

func TestReadBudget(t *testing.T) {
	budget := &readBudget{available: 2<<20 + 512<<10}
	for _, step := range []struct{ want, granted uint32 }{
		{1 << 20, 1 << 20},
		{1 << 20, 1 << 20},
		{1 << 20, 512 << 10},    // what is left
		{1 << 20, minReadBytes}, // spent: a little, to keep going
		{100, 100},
	} {
		if got := budget.take(step.want); got != step.granted {
			t.Fatalf("take(%d) = %d, want %d", step.want, got, step.granted)
		}
	}
	budget.give(1 << 20)
	budget.give(1 << 20)
	budget.give(512 << 10)
	budget.give(minReadBytes)
	budget.give(100)
	if budget.available != 2<<20+512<<10 {
		t.Errorf("available %d after everything was given back", budget.available)
	}
}

// go-nfs offers 1 GiB transfers; clients must be told what nfsd serves.
func TestServeFSInfoAdvertisesLimits(t *testing.T) {
	addr, _ := startServer(t, newTree(t))
	info, err := mustMount(t, addr, "/iso").FSInfo()
	if err != nil {
		t.Fatal(err)
	}
	if info.RTMax != maxReadBytes || info.RTPref != maxReadBytes {
		t.Errorf("rtmax %d, rtpref %d; want %d", info.RTMax, info.RTPref, maxReadBytes)
	}
	if info.WTMax != maxWriteBytes || info.WTPref != maxWriteBytes {
		t.Errorf("wtmax %d, wtpref %d; want %d", info.WTMax, info.WTPref, maxWriteBytes)
	}
	if info.RTMult != 4096 || info.DTPref != 8192 {
		t.Errorf("rtmult %d, dtpref %d: other fields changed", info.RTMult, info.DTPref)
	}
}

// readCall is a READ of the big test file.
func readCall(xid, count uint32) []byte {
	handle := isoHandle("casper", "big.squashfs")
	return withRecordMark(callBody(xid, progNFS, procRead, xdrOpaque(handle), xdrWords(0, 0, count)))
}

// readOn sends a READ over conn and returns the count of its reply.
func readOn(t *testing.T, conn net.Conn, xid, count uint32) uint32 {
	t.Helper()
	if _, err := conn.Write(readCall(xid, count)); err != nil {
		t.Fatal(err)
	}
	reply := readReply(t, conn)
	if got := binary.BigEndian.Uint32(reply); got != xid {
		t.Fatalf("reply to xid %d, want %d", got, xid)
	}
	const countOffset = 116 // see rawRead
	return binary.BigEndian.Uint32(reply[countOffset:])
}

// pipeListener hands the server in-memory connections. Unlike TCP, they
// buffer nothing: the server's Write of a reply waits until the client
// reads it, so a client that does not read holds its replies for certain.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case conn := <-l.conns:
		return conn, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "pipe"} }

// dial connects to the server.
func (l *pipeListener) dial(t *testing.T) net.Conn {
	t.Helper()
	client, server := net.Pipe()
	l.conns <- server
	t.Cleanup(func() { _ = client.Close() })
	return client
}

// stallReads sends READs over conn, and never reads the replies.
func stallReads(conn net.Conn, reads int) {
	calls := pipelinedReads(reads, maxReadBytes)
	go func() { _, _ = conn.Write(calls) }()
}

// A client that asks for data and does not read it holds that data in
// the server's memory. The READ data in flight is budgeted: once it is
// spent, READs still work but return a little each, and the budget comes
// back as replies go out or connections close.
func TestServeReadBudget(t *testing.T) {
	listener := newPipeListener()
	serveOn(t, newTree(t), &Server{MaxReadBytesInFlight: 2 * maxReadBytes}, listener)

	// Replies that are read give their share back: one connection reads
	// far more than the budget, one READ after the other.
	reader := listener.dial(t)
	for xid := range uint32(5) {
		if got := readOn(t, reader, xid, maxReadBytes); got != maxReadBytes {
			t.Fatalf("READ %d returned %d bytes, want %d", xid, got, maxReadBytes)
		}
	}

	// A client that reads none of its replies holds the budget it got.
	stalled := listener.dial(t)
	stallReads(stalled, 8)
	time.Sleep(100 * time.Millisecond)
	xid := uint32(100)
	if got := readOn(t, reader, xid, maxReadBytes); got != minReadBytes {
		t.Errorf("READ while the budget is held returned %d bytes, want %d", got, minReadBytes)
	}

	_ = stalled.Close()
	deadline := time.Now().Add(5 * time.Second)
	for xid++; readOn(t, reader, xid, maxReadBytes) != maxReadBytes; xid++ {
		if time.Now().After(deadline) {
			t.Fatal("budget not given back after the stalled client left")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// Replies are matched to calls by xid, so a connection may not reuse the
// xid of a call that is still awaiting its reply.
func TestServeRejectsReusedXid(t *testing.T) {
	listener := newPipeListener()
	serveOn(t, newTree(t), &Server{}, listener)
	conn := listener.dial(t)
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	// The reply to the first call cannot go out before the client reads.
	if _, err := conn.Write(append(readCall(7, maxReadBytes), readCall(7, maxReadBytes)...)); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	_, err := io.Copy(io.Discard, conn)
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		t.Error("connection still open after a call reused an xid")
	}
}

// One host with every connection it may open, each asking for far more
// than it reads, must stay well within the pod's 256 MiB. Without the
// budget this kept 120 MiB live (and up to 230 MiB in use); with it,
// about 33 MiB.
func TestServeReadMemoryIsBounded(t *testing.T) {
	root := newTree(t)
	if err := os.Truncate(filepath.Join(root, "iso", "casper", "big.squashfs"), 64<<20); err != nil {
		t.Fatal(err)
	}
	addr, _ := startServerWith(t, root, &Server{ConcurrentHandlers: 8})
	mustMount(t, addr, "/iso")

	calls := pipelinedReads(32, 16<<20)
	runtime.GC()
	for range DefaultMaxConnectionsPerHost {
		conn := dialRaw(t, addr)
		if err := conn.(*net.TCPConn).SetReadBuffer(4096); err != nil {
			t.Fatal(err)
		}
		if _, err := conn.Write(calls); err != nil {
			t.Fatal(err)
		}
	}
	// Once every connection has as many replies in hand as go-nfs lets it,
	// measure what stays in use: the heap after a collection.
	time.Sleep(time.Second)
	var peak uint64
	for range 5 {
		runtime.GC()
		var stats runtime.MemStats
		runtime.ReadMemStats(&stats)
		peak = max(peak, stats.HeapAlloc)
		time.Sleep(100 * time.Millisecond)
	}
	t.Logf("live heap: %d MiB", peak>>20)
	if peak > 2*DefaultMaxReadBytesInFlight {
		t.Errorf("live heap %d MiB, want under twice the read budget", peak>>20)
	}
}
