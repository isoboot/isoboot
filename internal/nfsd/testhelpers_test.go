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
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	nfsc "github.com/willscott/go-nfs-client/nfs"
	"github.com/willscott/go-nfs-client/nfs/rpc"
)

const bigFileSize = 3<<20 + 123

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustWrite(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func mustSymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
}

// newTree builds a root directory with two exports, "iso" and "other",
// and things next to them that must never be reachable.
func newTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	iso := filepath.Join(root, "iso")

	mustWrite(t, filepath.Join(root, "secret"), []byte("outside every export"))
	mustWrite(t, filepath.Join(root, ".hidden", "file"), []byte("hidden"))
	mustWrite(t, filepath.Join(root, "other", "hello.txt"), []byte("other export"))
	mustSymlink(t, "iso", filepath.Join(root, "link"))

	mustWrite(t, filepath.Join(iso, "hello.txt"), []byte("hello over NFS\n"))
	mustWrite(t, filepath.Join(iso, ".disk", "info"), []byte("Ubuntu-Server"))
	mustWrite(t, filepath.Join(iso, "casper", "vmlinuz"), []byte("kernel"))
	big := make([]byte, bigFileSize)
	if _, err := rand.Read(big); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(iso, "casper", "big.squashfs"), big)
	for i := range 100 {
		mustWrite(t, filepath.Join(iso, "many", fmt.Sprintf("f-%03d.deb", i)), []byte{byte(i)})
	}
	mustSymlink(t, ".", filepath.Join(iso, "ubuntu"))
	mustSymlink(t, "../secret", filepath.Join(iso, "escape"))
	mustSymlink(t, "/etc/hosts", filepath.Join(iso, "absolute"))
	return root
}

// startServer serves root on a loopback port until the test ends or stop
// is called.
func startServer(t *testing.T, root string) (addr string, stop func()) {
	t.Helper()
	return startServerWith(t, root, &Server{})
}

// startServerWith is startServer with the limits and timeouts of server.
func startServerWith(t *testing.T, root string, server *Server) (addr string, stop func()) {
	t.Helper()
	handler, err := NewHandler(root, testLogger())
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server.Handler = handler
	if server.ConcurrentHandlers == 0 {
		server.ConcurrentHandlers = 4
	}
	if server.Log == nil {
		server.Log = testLogger()
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = server.Serve(context.Background(), listener)
	}()
	stopped := false
	stop = func() {
		if stopped {
			return
		}
		stopped = true
		_ = listener.Close()
		<-done
		_ = handler.Close()
	}
	t.Cleanup(stop)
	return listener.Addr().String(), stop
}

// mount mounts an export with the NFS client library.
func mount(t *testing.T, addr, export string) (*nfsc.Target, error) {
	t.Helper()
	// rpc.DialTCP binds a random local port and, outside its privileged
	// mode, does not retry when that port is already taken.
	var client *rpc.Client
	var err error
	for range 10 {
		client, err = rpc.DialTCP("tcp", addr, false)
		if !errors.Is(err, syscall.EADDRINUSE) {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { client.Close() })
	mounter := &nfsc.Mount{Client: client}
	return mounter.Mount(export, rpc.AuthNull)
}

func mustMount(t *testing.T, addr, export string) *nfsc.Target {
	t.Helper()
	target, err := mount(t, addr, export)
	if err != nil {
		t.Fatalf("mount %s: %v", export, err)
	}
	return target
}

// xdrWords encodes big-endian 32-bit words.
func xdrWords(words ...uint32) []byte {
	var out []byte
	for _, w := range words {
		out = binary.BigEndian.AppendUint32(out, w)
	}
	return out
}

// xdrOpaque encodes variable-length opaque data with its padding.
func xdrOpaque(data []byte) []byte {
	out := binary.BigEndian.AppendUint32(nil, uint32(len(data)))
	out = append(out, data...)
	return append(out, make([]byte, (4-len(data)%4)%4)...)
}

// callBody builds the body of a version 3 NFS or MOUNT call with AUTH_NULL
// credentials and verifier, followed by the given arguments.
func callBody(xid, prog, proc uint32, args ...[]byte) []byte {
	body := xdrWords(xid, rpcCall, rpcVersion, prog, 3, proc, 0, 0, 0, 0)
	for _, arg := range args {
		body = append(body, arg...)
	}
	return body
}

// withRecordMark prepends a last-fragment record mark to a call body.
func withRecordMark(body []byte) []byte {
	return append(binary.BigEndian.AppendUint32(nil, lastFragment|uint32(len(body))), body...)
}

// dialRaw opens a plain TCP connection to the server.
func dialRaw(t *testing.T, addr string) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

// readReply reads one reply record and returns its body.
func readReply(t *testing.T, conn net.Conn) []byte {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	var mark [4]byte
	if _, err := io.ReadFull(conn, mark[:]); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	body := make([]byte, binary.BigEndian.Uint32(mark[:])&^lastFragment)
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read reply: %v", err)
	}
	return body
}

// expectClosed fails the test unless the server closes conn, without
// sending anything, within the given time.
func expectClosed(t *testing.T, conn net.Conn, within time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(within)); err != nil {
		t.Fatal(err)
	}
	n, err := conn.Read(make([]byte, 64))
	var netErr net.Error
	switch {
	case err == nil:
		t.Errorf("read %d bytes, want the connection closed", n)
	case errors.As(err, &netErr) && netErr.Timeout():
		t.Errorf("connection still open after %v", within)
	}
}

// expectOpen fails the test if the server closes conn within the given
// time.
func expectOpen(t *testing.T, conn net.Conn, within time.Duration) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(within)); err != nil {
		t.Fatal(err)
	}
	_, err := conn.Read(make([]byte, 64))
	var netErr net.Error
	if !errors.As(err, &netErr) || !netErr.Timeout() {
		t.Errorf("read = %v, want the connection left open", err)
	}
}
