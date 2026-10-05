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
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/netip"
	"strings"
	"time"

	nfs "github.com/willscott/go-nfs"
)

// Defaults for the limits of a Server. The Linux client closes a
// connection it has not used for five minutes and reconnects by itself,
// so an idle timeout above that only ever hits dead peers. A client sends
// its first request as soon as it has connected, and a request is under
// 1 KiB, so DefaultRequestTimeout only ever hits peers that stall.
//
// One installing machine holds one connection for the kernel's mount and
// briefly a second one for klibc's MOUNT call; DefaultMaxConnectionsPerHost
// leaves room for a few left behind by a machine that rebooted.
const (
	DefaultIdleTimeout           = 10 * time.Minute
	DefaultRequestTimeout        = 10 * time.Second
	DefaultWriteTimeout          = time.Minute
	DefaultMaxConnections        = 512
	DefaultMaxConnectionsPerHost = 8
)

// Server serves NFS version 3 and MOUNT version 3 on one listener.
//
// Zero values of the limits mean their defaults.
type Server struct {
	Handler *Handler
	// ConcurrentHandlers is how many requests of one connection are
	// handled at the same time.
	ConcurrentHandlers int
	// IdleTimeout is how long a connection may go without a request.
	IdleTimeout time.Duration
	// RequestTimeout is how long a client may take to send one request,
	// and to start its first one after connecting.
	RequestTimeout time.Duration
	// WriteTimeout bounds each write of a reply.
	WriteTimeout time.Duration
	// MaxConnections and MaxConnectionsPerHost bound the connections
	// served at once, in total and from one IP address. Connections
	// beyond them are closed at once.
	MaxConnections        int
	MaxConnectionsPerHost int
	// Allow lists the networks clients may connect from. Connections from
	// anywhere else are closed at once. Empty allows every address.
	Allow []netip.Prefix
	// Log receives refused connections and rejected requests, at most
	// one warning per few seconds. Nil means slog.Default().
	Log *slog.Logger
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	warnings := &throttledLog{log: orDefault(s.Log, slog.Default()), interval: warningInterval}
	srv := &nfs.Server{
		Handler:            s.Handler,
		ConcurrentHandlers: s.ConcurrentHandlers,
		Context:            ctx,
	}
	return srv.Serve(&guardedListener{
		Listener: l,
		gate: newConnectionGate("nfs", s.Allow,
			orDefault(s.MaxConnections, DefaultMaxConnections),
			orDefault(s.MaxConnectionsPerHost, DefaultMaxConnectionsPerHost), warnings),
		idleTimeout:    orDefault(s.IdleTimeout, DefaultIdleTimeout),
		requestTimeout: orDefault(s.RequestTimeout, DefaultRequestTimeout),
		writeTimeout:   orDefault(s.WriteTimeout, DefaultWriteTimeout),
		warnings:       warnings,
	})
}

// guardedListener hands go-nfs only the connections the gate admits, and
// wraps each in a guardedConn.
type guardedListener struct {
	net.Listener
	gate                                      *connectionGate
	idleTimeout, requestTimeout, writeTimeout time.Duration
	warnings                                  *throttledLog
}

func (l *guardedListener) Accept() (net.Conn, error) {
	for {
		conn, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		release, ok := l.gate.admit(conn)
		if !ok {
			_ = conn.Close()
			continue
		}
		return &guardedConn{Conn: conn, listener: l, release: release}, nil
	}
}

// guardedConn stands between a client and go-nfs. It reads one whole
// request record at a time, checks it (see checkCall) and only then lets
// go-nfs read it, and it gives every read and write a deadline, so that
// a stuck peer cannot hold a connection for ever. Replies pass through
// unchanged, one Write each as go-nfs makes them.
type guardedConn struct {
	net.Conn
	listener *guardedListener
	release  func()
	pending  []byte // the checked record go-nfs is reading
	started  bool   // a request has arrived
}

func (c *guardedConn) Read(p []byte) (int, error) {
	if len(c.pending) == 0 {
		record, err := c.nextRecord()
		if err != nil {
			_ = c.Close()
			return 0, err
		}
		c.pending = record
	}
	n := copy(p, c.pending)
	c.pending = c.pending[n:]
	return n, nil
}

var errRejectedRequest = errors.New("request rejected")

// nextRecord reads and checks the next request record: its record mark
// and its body.
func (c *guardedConn) nextRecord() ([]byte, error) {
	wait := c.listener.idleTimeout
	if !c.started {
		wait = c.listener.requestTimeout
	}
	if err := c.SetReadDeadline(time.Now().Add(wait)); err != nil {
		return nil, err
	}
	var mark [4]byte
	if _, err := io.ReadFull(c.Conn, mark[:]); err != nil {
		return nil, err
	}
	c.started = true
	fragment := binary.BigEndian.Uint32(mark[:])
	size := fragment &^ lastFragment
	if fragment&lastFragment == 0 || size < minRPCCall || size > maxRequestBytes {
		return nil, c.reject("bad record mark")
	}

	if err := c.SetReadDeadline(time.Now().Add(c.listener.requestTimeout)); err != nil {
		return nil, err
	}
	record := make([]byte, 4+size)
	if _, err := io.ReadFull(c.Conn, record[4:]); err != nil {
		return nil, err
	}
	body, err := checkCall(record[4:])
	if err != nil {
		return nil, c.reject(err.Error())
	}
	binary.BigEndian.PutUint32(record, lastFragment|uint32(len(body)))
	return record[:4+len(body)], nil
}

func (c *guardedConn) reject(reason string) error {
	c.listener.warnings.warn("request rejected", "server", "nfs",
		"client", c.RemoteAddr().String(), "reason", reason)
	return errRejectedRequest
}

func (c *guardedConn) Write(p []byte) (int, error) {
	if err := c.SetWriteDeadline(time.Now().Add(c.listener.writeTimeout)); err != nil {
		return 0, err
	}
	n, err := c.Conn.Write(p)
	if err != nil {
		_ = c.Close()
	}
	return n, err
}

func (c *guardedConn) Close() error {
	c.release()
	return c.Conn.Close()
}

// UseLogger sends the log output of the go-nfs library to log.
func UseLogger(log *slog.Logger) {
	nfs.SetLogger(&libraryLogger{log: log})
}

// LevelTrace is the slog level at which go-nfs logs every request.
const LevelTrace = slog.LevelDebug - 4

// libraryLogger adapts slog to the logger interface of go-nfs.
type libraryLogger struct {
	log *slog.Logger
}

var _ nfs.Logger = (*libraryLogger)(nil)

func (l *libraryLogger) emit(level slog.Level, args []any) {
	if !l.log.Enabled(context.Background(), level) {
		return
	}
	l.log.Log(context.Background(), level, strings.TrimSpace(fmt.Sprint(args...)), "source", "go-nfs")
}

func (l *libraryLogger) emitf(level slog.Level, format string, args []any) {
	if !l.log.Enabled(context.Background(), level) {
		return
	}
	l.log.Log(context.Background(), level, strings.TrimSpace(fmt.Sprintf(format, args...)), "source", "go-nfs")
}

func (l *libraryLogger) SetLevel(nfs.LogLevel)     {}
func (l *libraryLogger) GetLevel() nfs.LogLevel    { return nfs.InfoLevel }
func (l *libraryLogger) Panic(args ...any)         { l.emit(slog.LevelError, args) }
func (l *libraryLogger) Fatal(args ...any)         { l.emit(slog.LevelError, args) }
func (l *libraryLogger) Error(args ...any)         { l.emit(slog.LevelError, args) }
func (l *libraryLogger) Warn(args ...any)          { l.emit(slog.LevelWarn, args) }
func (l *libraryLogger) Info(args ...any)          { l.emit(slog.LevelInfo, args) }
func (l *libraryLogger) Debug(args ...any)         { l.emit(slog.LevelDebug, args) }
func (l *libraryLogger) Trace(args ...any)         { l.emit(LevelTrace, args) }
func (l *libraryLogger) Print(args ...any)         { l.emit(slog.LevelInfo, args) }
func (l *libraryLogger) Panicf(f string, a ...any) { l.emitf(slog.LevelError, f, a) }
func (l *libraryLogger) Fatalf(f string, a ...any) { l.emitf(slog.LevelError, f, a) }
func (l *libraryLogger) Errorf(f string, a ...any) { l.emitf(slog.LevelError, f, a) }
func (l *libraryLogger) Warnf(f string, a ...any)  { l.emitf(slog.LevelWarn, f, a) }
func (l *libraryLogger) Infof(f string, a ...any)  { l.emitf(slog.LevelInfo, f, a) }
func (l *libraryLogger) Debugf(f string, a ...any) { l.emitf(slog.LevelDebug, f, a) }
func (l *libraryLogger) Tracef(f string, a ...any) { l.emitf(LevelTrace, f, a) }
func (l *libraryLogger) Printf(f string, a ...any) { l.emitf(slog.LevelInfo, f, a) }

func (l *libraryLogger) ParseLevel(level string) (nfs.LogLevel, error) {
	return nfs.InfoLevel, fmt.Errorf("log level %q: set the level on the slog handler", level)
}
