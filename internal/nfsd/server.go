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
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"

	nfs "github.com/willscott/go-nfs"
)

// Timeouts for NFS connections. The Linux client closes a connection it
// has not used for five minutes and reconnects by itself, so an idle
// timeout above that only ever hits dead peers.
const (
	DefaultIdleTimeout  = 10 * time.Minute
	DefaultWriteTimeout = time.Minute
)

// Server serves NFS version 3 and MOUNT version 3 on one listener.
type Server struct {
	Handler *Handler
	// ConcurrentHandlers is how many requests of one connection are
	// handled at the same time.
	ConcurrentHandlers int
	IdleTimeout        time.Duration
	WriteTimeout       time.Duration
}

// Serve accepts connections until the listener is closed.
func (s *Server) Serve(ctx context.Context, l net.Listener) error {
	srv := &nfs.Server{
		Handler:            s.Handler,
		ConcurrentHandlers: s.ConcurrentHandlers,
		Context:            ctx,
	}
	return srv.Serve(&deadlineListener{Listener: l, idle: s.IdleTimeout, write: s.WriteTimeout})
}

// deadlineListener gives every accepted connection read and write
// deadlines, so that a stuck peer cannot hold a connection for ever.
type deadlineListener struct {
	net.Listener
	idle, write time.Duration
}

func (l *deadlineListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	return &deadlineConn{Conn: conn, idle: l.idle, write: l.write}, nil
}

type deadlineConn struct {
	net.Conn
	idle, write time.Duration
}

func (c *deadlineConn) Read(p []byte) (int, error) {
	if c.idle > 0 {
		if err := c.SetReadDeadline(time.Now().Add(c.idle)); err != nil {
			return 0, err
		}
	}
	n, err := c.Conn.Read(p)
	if err != nil {
		_ = c.Close()
	}
	return n, err
}

func (c *deadlineConn) Write(p []byte) (int, error) {
	if c.write > 0 {
		if err := c.SetWriteDeadline(time.Now().Add(c.write)); err != nil {
			return 0, err
		}
	}
	n, err := c.Conn.Write(p)
	if err != nil {
		_ = c.Close()
	}
	return n, err
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
