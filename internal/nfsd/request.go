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
)

// Every NFS and MOUNT request is checked here before go-nfs reads it.
//
// go-nfs allocates each variable-length field (credentials, file handles,
// names, paths, write data) at the length the client declares, before it
// reads a single byte of it: one 76-byte call that declares a 2 GiB
// credential makes it allocate 2 GiB. So every declared length must fit in
// the request that carries it, and requests are kept small.
const (
	// maxRequestBytes bounds one request record. The largest request a
	// read-only client sends (a LOOKUP with a 64-byte handle, a 255-byte
	// name and 400-byte AUTH_UNIX credentials) is under 1 KiB; the rest
	// leaves room for small WRITEs, which are answered "read-only". go-nfs
	// holds up to twice ConcurrentHandlers requests per connection, so at
	// the connection limit this bound is what their memory is made of.
	maxRequestBytes = 8 << 10

	// maxReadBytes is the most one READ returns. go-nfs allocates the
	// count a READ asks for (up to 16 MiB) and keeps two more copies of
	// it while it encodes the reply. Linux never reads more than 1 MiB at
	// a time, and NFSv3 lets a server return fewer bytes than asked for.
	maxReadBytes = 1 << 20
)

// NFS version 3 procedures nfsd looks at more closely.
const (
	procRead   = 6
	procFSInfo = 19
)

var (
	errNotACall     = errors.New("not an RPC call")
	errBadAuthLen   = errors.New("credentials or verifier too long")
	errShortRequest = errors.New("request ends inside the RPC header")
)

// argument is one field of the arguments of a call, as go-nfs reads it.
type argument int

const (
	opaqueArgument     argument = iota // length-prefixed bytes: handle, name, path, data
	uint32Argument                     // 4 bytes
	uint64Argument                     // 8 bytes
	attributesArgument                 // sattr3: optional fields of fixed size
	readCountArgument                  // the count of a READ
)

// callArguments lists, for each procedure go-nfs serves, the arguments in
// the order go-nfs reads them, up to the last variable-length one: fields
// after that have a fixed size and go-nfs allocates nothing for them.
// The order is go-nfs's, not always the RFC's (LINK), because what matters
// is what go-nfs allocates. Procedures not listed are answered without
// reading their arguments.
var callArguments = map[uint32]map[uint32][]argument{
	progNFS: {
		1:  {opaqueArgument},                                                                 // GETATTR
		2:  {opaqueArgument},                                                                 // SETATTR
		3:  {opaqueArgument, opaqueArgument},                                                 // LOOKUP
		4:  {opaqueArgument},                                                                 // ACCESS
		5:  {opaqueArgument},                                                                 // READLINK
		6:  {opaqueArgument, uint64Argument, readCountArgument},                              // READ
		7:  {opaqueArgument, uint64Argument, uint32Argument, uint32Argument, opaqueArgument}, // WRITE
		8:  {opaqueArgument, opaqueArgument},                                                 // CREATE
		9:  {opaqueArgument, opaqueArgument},                                                 // MKDIR
		10: {opaqueArgument, opaqueArgument, attributesArgument, opaqueArgument},             // SYMLINK
		11: {opaqueArgument, opaqueArgument},                                                 // MKNOD
		12: {opaqueArgument, opaqueArgument},                                                 // REMOVE
		13: {opaqueArgument, opaqueArgument},                                                 // RMDIR
		14: {opaqueArgument, opaqueArgument, opaqueArgument, opaqueArgument},                 // RENAME
		15: {opaqueArgument, opaqueArgument, attributesArgument, opaqueArgument},             // LINK
		16: {opaqueArgument},                                                                 // READDIR
		17: {opaqueArgument},                                                                 // READDIRPLUS
		18: {opaqueArgument},                                                                 // FSSTAT
		19: {opaqueArgument},                                                                 // FSINFO
		20: {opaqueArgument},                                                                 // PATHCONF
		21: {opaqueArgument},                                                                 // COMMIT
	},
	progMount: {
		1: {opaqueArgument}, // MNT
		3: {opaqueArgument}, // UMNT
	},
}

// checkedCall is a request that checkCall let through.
type checkedCall struct {
	body      []byte // the call to hand to go-nfs, without its record mark
	xid       uint32
	program   uint32
	procedure uint32
	// readCountOffset is where the count of a READ is in body, or 0 if
	// the call is not a READ that has one.
	readCountOffset int
}

// checkCall checks the body of one request record (the call without its
// record mark).
//
// A body whose RPC header is malformed is an error: the connection is
// closed, as go-nfs itself does. If an argument declares more bytes than
// the request holds, the arguments are cut off, so go-nfs answers the
// call with its usual error for unreadable arguments without allocating
// anything. A READ that asks for more than maxReadBytes is lowered to
// that, in place.
func checkCall(body []byte) (checkedCall, error) {
	r := &xdrReader{data: body}
	call := checkedCall{body: body, xid: r.uint32()}
	if r.uint32() != rpcCall {
		return checkedCall{}, errNotACall
	}
	r.uint32() // RPC version: go-nfs does not check it
	call.program = r.uint32()
	r.uint32() // program version
	call.procedure = r.uint32()
	for range 2 { // credentials and verifier
		r.uint32() // flavor
		length := r.uint32()
		if length > maxAuthLength {
			return checkedCall{}, errBadAuthLen
		}
		r.skip(padded(length))
	}
	if r.short {
		return checkedCall{}, errShortRequest
	}

	header := r.offset
	fit, readCountOffset := r.argumentsFit(callArguments[call.program][call.procedure])
	if !fit {
		call.body = body[:header]
		return call, nil
	}
	call.readCountOffset = readCountOffset
	return call, nil
}

// argumentsFit walks the arguments and reports whether every declared
// length fits in what is left of the request, and where the count of a
// READ is (0 if there is none). Arguments that stop short are fine:
// go-nfs fails to read them without a large allocation.
func (r *xdrReader) argumentsFit(arguments []argument) (fit bool, readCountOffset int) {
	for _, arg := range arguments {
		switch arg {
		case opaqueArgument:
			length := r.uint32()
			if !r.short && uint64(length) > uint64(r.remaining()) {
				return false, 0
			}
			r.skip(padded(length))
		case uint32Argument:
			r.skip(4)
		case uint64Argument:
			r.skip(8)
		case attributesArgument:
			r.skipAttributes()
		case readCountArgument:
			if r.remaining() >= 4 {
				readCountOffset = r.offset
				if r.peekUint32() > maxReadBytes {
					binary.BigEndian.PutUint32(r.data[r.offset:], maxReadBytes)
				}
			}
			r.skip(4)
		}
		if r.short {
			break
		}
	}
	return true, readCountOffset
}

// skipAttributes skips a sattr3 the way go-nfs reads it: mode, uid, gid
// and size, each present if its flag is not zero, then atime and mtime,
// each with a time value if its flag is 2 (SET_TO_CLIENT_TIME).
func (r *xdrReader) skipAttributes() {
	for _, size := range []uint64{4, 4, 4, 8} {
		if r.uint32() != 0 {
			r.skip(size)
		}
	}
	for range 2 {
		if r.uint32() == 2 {
			r.skip(8)
		}
	}
}

// xdrReader reads big-endian words from a request. Reading past the end
// sets short and yields zeros.
type xdrReader struct {
	data   []byte
	offset int
	short  bool
}

func (r *xdrReader) remaining() int { return len(r.data) - r.offset }

func (r *xdrReader) peekUint32() uint32 { return binary.BigEndian.Uint32(r.data[r.offset:]) }

func (r *xdrReader) uint32() uint32 {
	if r.short || r.remaining() < 4 {
		r.short = true
		return 0
	}
	v := r.peekUint32()
	r.offset += 4
	return v
}

func (r *xdrReader) skip(n uint64) {
	if r.short || n > uint64(r.remaining()) {
		r.short = true
		return
	}
	r.offset += int(n)
}

// padded rounds an XDR length up to a multiple of four.
func padded(length uint32) uint64 {
	return (uint64(length) + 3) &^ 3
}
