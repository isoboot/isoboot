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
	"errors"
	"runtime"
	"slices"
	"testing"
	"time"

	nfsc "github.com/willscott/go-nfs-client/nfs"
)

// NFS version 3 and MOUNT version 3 procedures used by the tests.
const (
	procGetAttr  = 1
	procLookup   = 3
	procWrite    = 7
	procSymlink  = 10
	procRename   = 14
	procLink     = 15
	procMount    = 1
	procExport   = 5
	hugeLength   = 0x7ffffff0
	headerLength = 40 // an RPC call header with AUTH_NULL credentials
)

func TestCheckCallPassesWellFormedCalls(t *testing.T) {
	handle := bytes.Repeat([]byte{1}, HandleLen)
	// sattr3 as clients send it: mode set, uid and gid not, size set,
	// atime set to the client's time, mtime to the server's.
	attributes := xdrWords(1, 0o644, 0, 0, 1, 0, 4096, 2, 1700000000, 0, 1)
	tests := []struct {
		name string
		body []byte
	}{
		{"null", callBody(1, progNFS, 0)},
		{"lookup", callBody(1, progNFS, procLookup, xdrOpaque(handle), xdrOpaque([]byte("vmlinuz")))},
		{"read of 1 MiB", callBody(1, progNFS, procRead, xdrOpaque(handle), xdrWords(0, 0, maxReadBytes))},
		{"write", callBody(1, progNFS, procWrite, xdrOpaque(handle), xdrWords(0, 0, 5, 0), xdrOpaque([]byte("hello")))},
		{"symlink", callBody(1, progNFS, procSymlink,
			xdrOpaque(handle), xdrOpaque([]byte("link")), attributes, xdrOpaque([]byte("target")))},
		{"rename", callBody(1, progNFS, procRename,
			xdrOpaque(handle), xdrOpaque([]byte("a")), xdrOpaque(handle), xdrOpaque([]byte("b")))},
		{"mount", callBody(1, progMount, procMount, xdrOpaque([]byte("/ubuntu-26.04")))},
		// Arguments that stop short cost go-nfs nothing to fail on.
		{"lookup without a name", callBody(1, progNFS, procLookup, xdrOpaque(handle))},
		{"read without a count", callBody(1, progNFS, procRead, xdrOpaque(handle), xdrWords(0))},
		// go-nfs answers procedures it does not serve without reading them.
		{"mount export", callBody(1, progMount, procExport, xdrWords(hugeLength))},
		{"unknown program", callBody(1, 100099, 1, xdrWords(hugeLength))},
		{"auth unix", append(xdrWords(1, rpcCall, rpcVersion, progNFS, 3, procGetAttr,
			1, 20, 0, 0, 0, 0, 0, 0, 0), xdrOpaque(handle)...)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call, err := checkCall(slices.Clone(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if got := call.body; !bytes.Equal(got, tt.body) {
				t.Errorf("call changed:\n got %x\nwant %x", got, tt.body)
			}
		})
	}
}

// A length that does not fit in the request would make go-nfs allocate
// it: the arguments are dropped, and go-nfs answers "invalid".
func TestCheckCallDropsArgumentsThatDoNotFit(t *testing.T) {
	handle := bytes.Repeat([]byte{1}, HandleLen)
	attributes := xdrWords(1, 0o644, 0, 0, 1, 0, 4096, 2, 1700000000, 0, 1)
	tests := []struct {
		name string
		body []byte
	}{
		{"getattr handle", callBody(1, progNFS, procGetAttr, xdrWords(hugeLength))},
		{"read handle", callBody(1, progNFS, procRead, xdrWords(hugeLength), make([]byte, 64))},
		{"lookup name", callBody(1, progNFS, procLookup, xdrOpaque(handle), xdrWords(hugeLength))},
		{"lookup name one byte too long", callBody(1, progNFS, procLookup, xdrOpaque(handle), xdrWords(5), []byte("abcd"))},
		{"write data", callBody(1, progNFS, procWrite, xdrOpaque(handle), xdrWords(0, 0, 5, 0, hugeLength))},
		// The target comes after a sattr3 of variable size: it is found
		// only if the attributes are skipped exactly as go-nfs reads them.
		{"symlink target", callBody(1, progNFS, procSymlink,
			xdrOpaque(handle), xdrOpaque([]byte("link")), attributes, xdrWords(hugeLength), make([]byte, 8))},
		{"link target", callBody(1, progNFS, procLink,
			xdrOpaque(handle), xdrOpaque([]byte("link")), xdrWords(0, 0, 0, 0, 0, 0), xdrWords(hugeLength))},
		{"rename second name", callBody(1, progNFS, procRename,
			xdrOpaque(handle), xdrOpaque([]byte("a")), xdrOpaque(handle), xdrWords(0xffffffff))},
		{"mount path", callBody(1, progMount, procMount, xdrWords(0xfffffff0))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			call, err := checkCall(slices.Clone(tt.body))
			if err != nil {
				t.Fatal(err)
			}
			if got, want := call.body, tt.body[:headerLength]; !bytes.Equal(got, want) {
				t.Errorf("call:\n got %x\nwant the header alone %x", got, want)
			}
		})
	}
}

func TestCheckCallRejectsBadHeaders(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want error
	}{
		{"reply", xdrWords(1, rpcReply, rpcVersion, progNFS, 3, 0, 0, 0, 0, 0), errNotACall},
		{"huge credentials", append(xdrWords(1, rpcCall, rpcVersion, progNFS, 3, 0, 1, hugeLength), make([]byte, 40)...),
			errBadAuthLen},
		{"credentials over 400 bytes", append(xdrWords(1, rpcCall, rpcVersion, progNFS, 3, 0, 1, 404), make([]byte, 420)...),
			errBadAuthLen},
		{"huge verifier", xdrWords(1, rpcCall, rpcVersion, progNFS, 3, 0, 0, 0, 0, hugeLength), errBadAuthLen},
		{"credentials longer than the call", append(xdrWords(1, rpcCall, rpcVersion, progNFS, 3, 0, 1, 400), make([]byte, 40)...),
			errShortRequest},
		{"no verifier", xdrWords(1, rpcCall, rpcVersion, progNFS, 3, 0, 0, 0), errShortRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := checkCall(tt.body); !errors.Is(err, tt.want) {
				t.Errorf("err = %v, want %v", err, tt.want)
			}
		})
	}
}

func TestCheckCallLowersReadCount(t *testing.T) {
	handle := bytes.Repeat([]byte{1}, HandleLen)
	body := callBody(1, progNFS, procRead, xdrOpaque(handle), xdrWords(0, 4096, 16<<20))
	call, err := checkCall(body)
	if err != nil {
		t.Fatal(err)
	}
	want := callBody(1, progNFS, procRead, xdrOpaque(handle), xdrWords(0, 4096, maxReadBytes))
	if !bytes.Equal(call.body, want) {
		t.Errorf("call:\n got %x\nwant %x", call.body, want)
	}
	// The connection lowers the count further when the read budget is
	// spent: it must find the count where it is.
	if call.xid != 1 || call.program != progNFS || call.procedure != procRead {
		t.Errorf("call %d to %d.%d, want 1 to %d.%d", call.xid, call.program, call.procedure, progNFS, procRead)
	}
	if want := len(want) - 4; call.readCountOffset != want {
		t.Errorf("readCountOffset = %d, want %d", call.readCountOffset, want)
	}

	call, err = checkCall(callBody(1, progNFS, procRead, xdrOpaque(handle), xdrWords(0)))
	if err != nil || call.readCountOffset != 0 {
		t.Errorf("READ without a count: readCountOffset = %d, %v; want 0", call.readCountOffset, err)
	}
}

// A record mark may declare up to 16 MiB, which go-nfs buffers in full
// before it looks at the call. nfsd closes the connection at the mark.
func TestServeRejectsOversizedRecord(t *testing.T) {
	addr, _ := startServer(t, newTree(t))
	conn := dialRaw(t, addr)
	if _, err := conn.Write(xdrWords(lastFragment | 1<<20)); err != nil {
		t.Fatal(err)
	}
	expectClosed(t, conn, 2*time.Second)
}

// One small call that declares a huge credential, handle or path must not
// make the server allocate it.
func TestServeHugeLengthsDoNotAllocate(t *testing.T) {
	addr, _ := startServer(t, newTree(t))
	tests := []struct {
		name       string
		body       []byte
		wantAnswer bool // false: the connection is closed instead
	}{
		{"credentials", append(xdrWords(1, rpcCall, rpcVersion, progNFS, 3, 0, 1, hugeLength), make([]byte, 40)...), false},
		{"read handle", callBody(2, progNFS, procRead, xdrWords(hugeLength), make([]byte, 64)), true},
		{"mount path", callBody(3, progMount, procMount, xdrWords(0xfffffff0), make([]byte, 64)), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			conn := dialRaw(t, addr)
			before := totalAlloc()
			if _, err := conn.Write(withRecordMark(tt.body)); err != nil {
				t.Fatal(err)
			}
			if tt.wantAnswer {
				reply := readReply(t, conn)
				if xid := binary.BigEndian.Uint32(reply); xid != binary.BigEndian.Uint32(tt.body) {
					t.Errorf("reply xid %d", xid)
				}
				// xid, REPLY, MSG_ACCEPTED, verifier (2 words), accept
				// status, then the NFS or MOUNT status if accepted.
				accept := binary.BigEndian.Uint32(reply[20:])
				if accept == acceptSuccess && binary.BigEndian.Uint32(reply[24:]) == 0 {
					t.Error("call with an impossible length succeeded")
				}
			} else {
				expectClosed(t, conn, 2*time.Second)
			}
			if grown := totalAlloc() - before; grown > 64<<20 {
				t.Errorf("server allocated %d MiB for one small call", grown>>20)
			}
		})
	}
}

func totalAlloc() uint64 {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats.TotalAlloc
}

// rawRead sends one READ and returns the count of the reply.
func rawRead(t *testing.T, addr string, handle []byte, count uint32) uint32 {
	t.Helper()
	conn := dialRaw(t, addr)
	if _, err := conn.Write(withRecordMark(callBody(9, progNFS, procRead, xdrOpaque(handle), xdrWords(0, 0, count)))); err != nil {
		t.Fatal(err)
	}
	reply := readReply(t, conn)
	// xid, REPLY, MSG_ACCEPTED, verifier (2 words), accept status, NFS
	// status, attributes follow (1), fattr3 (84 bytes), count.
	const statusOffset, countOffset = 24, 116
	if status := binary.BigEndian.Uint32(reply[statusOffset:]); status != nfsc.NFS3Ok {
		t.Fatalf("READ status %d", status)
	}
	got := binary.BigEndian.Uint32(reply[countOffset:])
	if dataLength := binary.BigEndian.Uint32(reply[countOffset+8:]); dataLength != got {
		t.Errorf("READ count %d but %d bytes of data", got, dataLength)
	}
	return got
}

// go-nfs would read up to 16 MiB per READ, and FSINFO invites clients to
// ask for 1 GiB; nfsd returns at most maxReadBytes.
func TestServeReadIsClamped(t *testing.T) {
	addr, _ := startServer(t, newTree(t))
	mustMount(t, addr, "/iso")
	handle := isoHandle("casper", "big.squashfs")
	if got := rawRead(t, addr, handle, 16<<20); got != maxReadBytes {
		t.Errorf("READ of 16 MiB returned %d bytes, want %d", got, maxReadBytes)
	}
	if got := rawRead(t, addr, handle, 4096); got != 4096 {
		t.Errorf("READ of 4096 bytes returned %d", got)
	}
}

// checkCall runs on every request from the network before anything else
// looks at it: whatever the bytes, it must not panic, and what it passes
// on is the call or the call cut short at its header.
func FuzzCheckCall(f *testing.F) {
	handle := bytes.Repeat([]byte{1}, HandleLen)
	f.Add(callBody(1, progNFS, procLookup, xdrOpaque(handle), xdrOpaque([]byte("vmlinuz"))))
	f.Add(callBody(1, progNFS, procRead, xdrOpaque(handle), xdrWords(0, 0, 16<<20)))
	f.Add(callBody(1, progNFS, procSymlink, xdrOpaque(handle), xdrOpaque([]byte("l")),
		xdrWords(1, 0o644, 0, 0, 1, 0, 1, 2, 1, 0, 2, 1, 0), xdrWords(hugeLength)))
	f.Add(callBody(1, progMount, procMount, xdrWords(0xffffffff)))
	f.Fuzz(func(t *testing.T, body []byte) {
		call, err := checkCall(slices.Clone(body))
		if err != nil {
			return
		}
		if len(call.body) != len(body) && len(call.body) < headerLength {
			t.Errorf("passed on %d of %d bytes", len(call.body), len(body))
		}
		if call.readCountOffset != 0 && call.readCountOffset+4 > len(call.body) {
			t.Errorf("READ count at %d of a %d-byte call", call.readCountOffset, len(call.body))
		}
	})
}
