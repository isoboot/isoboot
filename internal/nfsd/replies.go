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
	"slices"
	"sync"
)

// The data of a READ stays in memory from the moment go-nfs reads the
// file until the reply has been sent: go-nfs holds about two copies of
// it, and a client that does not read its replies keeps them there for up
// to WriteTimeout. Each connection has up to twice ConcurrentHandlers
// replies in hand, so a few connections asking for 1 MiB each would
// exhaust the pod's memory. The READ data in flight is therefore budgeted
// across all connections.
const (
	// DefaultMaxReadBytesInFlight is the default budget: about 100 MiB of
	// memory at worst, and far more than the installing machines of one
	// subnet keep in flight.
	DefaultMaxReadBytesInFlight = 32 << 20

	// minReadBytes is what a READ is lowered to once the budget is spent,
	// so that every client still makes progress. It must not be 0: the
	// Linux client takes a READ that returns nothing before the end of
	// the file as an I/O error.
	minReadBytes = 4096

	// maxWriteBytes is the largest WRITE nfsd advertises, so that one
	// fits in a request of maxRequestBytes. Every WRITE is answered
	// "read-only file system" anyway.
	maxWriteBytes = 4 << 10
)

// readBudget is the READ data that may be in flight on all connections.
type readBudget struct {
	mu        sync.Mutex
	available int64
}

// take reserves the bytes for a READ that asks for want bytes and returns
// how many it may return: all of them while the budget lasts, and then
// minReadBytes. The budget may go below zero by those.
func (b *readBudget) take(want uint32) uint32 {
	b.mu.Lock()
	defer b.mu.Unlock()
	granted := want
	if int64(want) > b.available {
		granted = min(want, uint32(max(b.available, minReadBytes)))
	}
	b.available -= int64(granted)
	return granted
}

// give returns bytes that take reserved, once their reply has been sent.
func (b *readBudget) give(bytes uint32) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.available += int64(bytes)
}

// replyStream follows the reply records go-nfs writes to a connection, to
// tell when each reply has been sent in full. A reply larger than the
// write buffer of go-nfs (4 KiB) takes more than one Write.
type replyStream struct {
	mark      [4]byte
	markSize  int    // bytes of mark written so far
	remaining uint32 // bytes of the current reply still to be written
	xid       [4]byte
	xidSize   int // bytes of xid written so far
}

// atReplyStart reports whether p begins a new reply, and if so its xid.
func (s *replyStream) atReplyStart(p []byte) (xid uint32, ok bool) {
	if s.markSize != 0 || len(p) < 8 {
		return 0, false
	}
	return binary.BigEndian.Uint32(p[4:]), true
}

// written follows the bytes p that went out on the connection, and calls
// answered with the xid of each reply they complete.
func (s *replyStream) written(p []byte, answered func(xid uint32)) {
	for len(p) > 0 {
		if s.markSize < len(s.mark) {
			n := copy(s.mark[s.markSize:], p)
			s.markSize += n
			p = p[n:]
			if s.markSize < len(s.mark) {
				return
			}
			s.remaining = binary.BigEndian.Uint32(s.mark[:]) &^ lastFragment
			s.xidSize = 0
		}
		n := min(uint32(len(p)), s.remaining)
		s.xidSize += copy(s.xid[s.xidSize:], p[:n])
		s.remaining -= n
		p = p[n:]
		if s.remaining == 0 {
			if s.xidSize == len(s.xid) {
				answered(binary.BigEndian.Uint32(s.xid[:]))
			}
			s.markSize = 0
		}
	}
}

// advertiseLimits returns a copy of an FSINFO reply record (RFC 1813,
// section 3.3.19) that offers the transfer sizes nfsd serves: go-nfs
// offers 1 GiB for both reads and writes. Anything but a whole successful
// reply is returned unchanged.
func advertiseLimits(record []byte) []byte {
	word := func(offset int) uint32 { return binary.BigEndian.Uint32(record[offset:]) }
	// Record mark, xid, REPLY, MSG_ACCEPTED, verifier flavor and length,
	// accept status, NFS status, then whether attributes follow.
	const attributesFollow, fileAttributesSize = 32, 84
	if len(record) < attributesFollow+4 || int(word(0)&^lastFragment) != len(record)-4 ||
		word(12) != rpcAccepted || word(20) != 0 || word(24) != acceptSuccess || word(28) != 0 {
		return record
	}
	rtmax := attributesFollow + 4
	if word(attributesFollow) != 0 {
		rtmax += fileAttributesSize
	}
	// rtmax, rtpref, rtmult, wtmax, wtpref
	if len(record) < rtmax+20 {
		return record
	}
	out := slices.Clone(record)
	binary.BigEndian.PutUint32(out[rtmax:], maxReadBytes)
	binary.BigEndian.PutUint32(out[rtmax+4:], maxReadBytes)
	binary.BigEndian.PutUint32(out[rtmax+12:], maxWriteBytes)
	binary.BigEndian.PutUint32(out[rtmax+16:], maxWriteBytes)
	return out
}
