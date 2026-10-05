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
	"log/slog"
	"net"
	"net/netip"
	"time"
)

// ONC RPC and port mapper (RFC 1833, version 2) constants.
const (
	rpcVersion    = 2
	rpcCall       = 0
	rpcReply      = 1
	rpcAccepted   = 0
	rpcDenied     = 1
	rpcMismatch   = 0
	lastFragment  = 1 << 31
	maxAuthLength = 400

	acceptSuccess      = 0
	acceptProgUnavail  = 1
	acceptProgMismatch = 2
	acceptProcUnavail  = 3
	acceptGarbageArgs  = 4

	progPortmap = 100000
	progNFS     = 100003
	progMount   = 100005
	versPortmap = 2
	versNFS     = 3
	versMount   = 3
	protoTCP    = 6

	procNull    = 0
	procGetPort = 3

	// A GETPORT call is 56 bytes with empty credentials. Nothing the
	// responder understands comes close to this limit.
	maxPortmapCall = 1024
	minRPCCall     = 40
)

// Defaults for the connection limits of a Portmap. A connection costs a
// goroutine and at most maxPortmapCall bytes, and klibc makes one short
// connection per lookup. The cap per address is what keeps the table
// from filling up: one host, however many connections it opens, cannot
// take the slots the installing machines need.
const (
	DefaultPortmapMaxConnections        = 1024
	DefaultPortmapMaxConnectionsPerHost = 4
)

// Portmap answers port mapper GETPORT calls over TCP with the port of the
// NFS server, for NFS version 3 and MOUNT version 3 over TCP, and with
// port 0 ("not registered") for everything else.
//
// The NFS client in the Ubuntu installer's initramfs (klibc nfsmount)
// reads each reply with a single read() and has no timeout, so every
// reply is sent with one Write and no connection is left unanswered: a
// call that cannot be parsed closes the connection.
type Portmap struct {
	// Port is the TCP port that serves both NFS and MOUNT.
	Port uint32
	// Timeout bounds how long a connection may sit idle, and each write.
	Timeout time.Duration
	// MaxConnections and MaxConnectionsPerHost bound the connections
	// served at once, in total and from one IP address; zero means the
	// default. Connections beyond them are closed at once rather than
	// queued: klibc waits for ever on a connection nobody answers.
	MaxConnections        int
	MaxConnectionsPerHost int
	// Allow lists the networks clients may connect from. Connections from
	// anywhere else are closed at once. Empty allows every address.
	Allow []netip.Prefix
	// Log receives lookups, and refused connections and rejected calls at
	// most one warning per few seconds. Nil means slog.Default().
	Log *slog.Logger
}

// Serve accepts connections until the listener is closed.
func (p *Portmap) Serve(l net.Listener) error {
	warnings := &throttledLog{log: p.logger(), interval: warningInterval}
	gate := newConnectionGate("portmap", p.Allow,
		orDefault(p.MaxConnections, DefaultPortmapMaxConnections),
		orDefault(p.MaxConnectionsPerHost, DefaultPortmapMaxConnectionsPerHost),
		warnings)
	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(10 * time.Millisecond)
				continue
			}
			return err
		}
		release, ok := gate.admit(conn)
		if !ok {
			_ = conn.Close()
			continue
		}
		go func() {
			defer release()
			p.serveConn(conn, warnings)
		}()
	}
}

// serveConn answers calls on one connection until the peer closes it,
// goes quiet for longer than the timeout or sends something unparseable.
// Rejected calls are logged to warnings, which keeps a flood of them from
// flooding the log.
func (p *Portmap) serveConn(conn net.Conn, warnings *throttledLog) {
	defer func() { _ = conn.Close() }()
	client := conn.RemoteAddr().String()
	var mark [4]byte
	for {
		if err := conn.SetDeadline(time.Now().Add(p.Timeout)); err != nil {
			return
		}
		if _, err := io.ReadFull(conn, mark[:]); err != nil {
			return
		}
		fragment := binary.BigEndian.Uint32(mark[:])
		size := fragment &^ lastFragment
		if fragment&lastFragment == 0 || size < minRPCCall || size > maxPortmapCall {
			warnings.warn("portmap call rejected", "client", client, "reason", "bad record mark")
			return
		}
		call := make([]byte, size)
		if _, err := io.ReadFull(conn, call); err != nil {
			return
		}
		reply := p.reply(call, client)
		if reply == nil {
			warnings.warn("portmap call rejected", "client", client, "reason", "not an RPC call")
			return
		}
		// One Write per reply: record mark and body leave together.
		if _, err := conn.Write(reply); err != nil {
			return
		}
	}
}

// reply builds the complete reply record (record mark included) for one
// call, or returns nil if the bytes are not an RPC call at all.
func (p *Portmap) reply(call []byte, client string) []byte {
	if len(call) < minRPCCall {
		return nil
	}
	u32 := func(off int) uint32 { return binary.BigEndian.Uint32(call[off:]) }
	xid := u32(0)
	if u32(4) != rpcCall {
		return nil
	}
	if u32(8) != rpcVersion {
		return record(xid, rpcDenied, rpcMismatch, rpcVersion, rpcVersion)
	}
	prog, vers, proc := u32(12), u32(16), u32(20)

	// Skip the credential and the verifier: flavor, length, padded body.
	off := 24
	for range 2 {
		if len(call) < off+8 {
			return nil
		}
		length := u32(off + 4)
		if length > maxAuthLength {
			return nil
		}
		off += 8 + int(length+3)&^3
	}
	if len(call) < off {
		return nil
	}
	args := call[off:]

	switch {
	case prog != progPortmap:
		return accepted(xid, acceptProgUnavail)
	case vers != versPortmap:
		return accepted(xid, acceptProgMismatch, versPortmap, versPortmap)
	case proc == procNull:
		return accepted(xid, acceptSuccess)
	case proc != procGetPort:
		return accepted(xid, acceptProcUnavail)
	case len(args) < 16:
		return accepted(xid, acceptGarbageArgs)
	}

	wantProg := binary.BigEndian.Uint32(args[0:])
	wantVers := binary.BigEndian.Uint32(args[4:])
	wantProto := binary.BigEndian.Uint32(args[8:])
	port := p.lookup(wantProg, wantVers, wantProto)
	p.logger().Info("portmap getport", "client", client,
		"program", wantProg, "version", wantVers, "protocol", wantProto, "port", port)
	return accepted(xid, acceptSuccess, port)
}

func (p *Portmap) logger() *slog.Logger {
	return orDefault(p.Log, slog.Default())
}

// lookup returns the port for a program, or 0 if it is not served.
func (p *Portmap) lookup(prog, vers, proto uint32) uint32 {
	if proto != protoTCP {
		return 0
	}
	if (prog == progNFS && vers == versNFS) || (prog == progMount && vers == versMount) {
		return p.Port
	}
	return 0
}

// accepted builds an accepted reply: xid, REPLY, MSG_ACCEPTED, an empty
// verifier, the accept status and then the results.
func accepted(xid, stat uint32, results ...uint32) []byte {
	words := append([]uint32{rpcAccepted, 0, 0, stat}, results...)
	return record(xid, words...)
}

// record builds a single-fragment reply record: the record mark, the xid,
// the REPLY message type and the given words.
func record(xid uint32, words ...uint32) []byte {
	body := 4 * (2 + len(words))
	out := make([]byte, 0, 4+body)
	out = binary.BigEndian.AppendUint32(out, lastFragment|uint32(body))
	out = binary.BigEndian.AppendUint32(out, xid)
	out = binary.BigEndian.AppendUint32(out, rpcReply)
	for _, w := range words {
		out = binary.BigEndian.AppendUint32(out, w)
	}
	return out
}
