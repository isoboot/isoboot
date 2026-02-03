package downloader

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Download", func() {
	It("should download file content to the destination path", func() {
		expected := "file content for download test"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(expected)) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "downloaded-file")

		Expect(Download(context.Background(), ts.URL, destPath)).To(Succeed())

		data, err := os.ReadFile(destPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(expected))
	})

	It("should create intermediate directories", func() {
		expected := "data"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(expected)) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "sub", "dir", "file")

		Expect(Download(context.Background(), ts.URL, destPath)).To(Succeed())

		data, err := os.ReadFile(destPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(expected))
	})

	It("should return error on 404 and not leave a file", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "should-not-exist")

		Expect(Download(context.Background(), ts.URL, destPath)).To(HaveOccurred())

		_, statErr := os.Stat(destPath)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("should not leave partial files on truncated download", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				panic("server does not support hijacking")
			}
			w.Header().Set("Content-Length", "1000000")
			w.WriteHeader(http.StatusOK)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			conn, _, err := hj.Hijack()
			if err != nil {
				panic("hijack failed: " + err.Error())
			}
			conn.Close() //nolint:errcheck // intentional abrupt close for test
		}))
		defer ts.Close()

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "should-not-exist")

		Expect(Download(context.Background(), ts.URL, destPath)).To(HaveOccurred())

		_, statErr := os.Stat(destPath)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("should return error on canceled context", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("data")) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "should-not-exist")

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		Expect(Download(ctx, ts.URL, destPath)).To(HaveOccurred())

		_, statErr := os.Stat(destPath)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})
})

var _ = Describe("FetchContent", func() {
	It("should fetch and return content", func() {
		expected := "shasum file content"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(expected)) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		data, err := FetchContent(context.Background(), ts.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(expected))
	})

	It("should return error on 404", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		_, err := FetchContent(context.Background(), ts.URL)
		Expect(err).To(HaveOccurred())
	})

	It("should reject responses exceeding the size limit", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// Write may fail if client disconnects early; that's expected.
			w.Write([]byte(strings.Repeat("x", maxFetchSize+1))) //nolint:errcheck // client may disconnect
		}))
		defer ts.Close()

		_, err := FetchContent(context.Background(), ts.URL)
		Expect(err).To(HaveOccurred())
	})

	It("should return error on canceled context", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("data")) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		_, err := FetchContent(ctx, ts.URL)
		Expect(err).To(HaveOccurred())
	})
})
