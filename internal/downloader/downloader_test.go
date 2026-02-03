package downloader

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// customHeaderTransport is a test helper that injects a custom header into
// every request, allowing tests to verify that a specific client was used.
type customHeaderTransport struct {
	header string
	value  string
	base   http.RoundTripper
}

func (t *customHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.Header.Set(t.header, t.value)
	return t.base.RoundTrip(req)
}

var _ = Describe("NewDefaultClient", func() {
	It("should return a client with expected transport timeouts", func() {
		client := NewDefaultClient()
		Expect(client).NotTo(BeNil())
		Expect(client.Timeout).To(Equal(time.Duration(0)), "no overall timeout so large downloads are not interrupted")

		transport, ok := client.Transport.(*http.Transport)
		Expect(ok).To(BeTrue())
		Expect(transport.TLSHandshakeTimeout).To(Equal(10 * time.Second))
		Expect(transport.ResponseHeaderTimeout).To(Equal(30 * time.Second))
	})

	It("should produce a working client", func() {
		expected := "hello from custom client test"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(expected)) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		client := NewDefaultClient()
		data, err := FetchContent(context.Background(), client, ts.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(expected))
	})
})

var _ = Describe("Download", func() {
	It("should download file content to the destination path", func() {
		expected := "file content for download test"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(expected)) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "downloaded-file")

		Expect(Download(context.Background(), nil, ts.URL, destPath)).To(Succeed())

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

		Expect(Download(context.Background(), nil, ts.URL, destPath)).To(Succeed())

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

		Expect(Download(context.Background(), nil, ts.URL, destPath)).To(HaveOccurred())

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

		Expect(Download(context.Background(), nil, ts.URL, destPath)).To(HaveOccurred())

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

		Expect(Download(ctx, nil, ts.URL, destPath)).To(HaveOccurred())

		_, statErr := os.Stat(destPath)
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})

	It("should use a provided custom client", func() {
		var receivedUA string
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedUA = r.Header.Get("User-Agent")
			w.Write([]byte("custom")) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		custom := &http.Client{
			Transport: &customHeaderTransport{
				header: "User-Agent",
				value:  "test-agent",
				base:   http.DefaultTransport,
			},
		}

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "custom-client-file")

		Expect(Download(context.Background(), custom, ts.URL, destPath)).To(Succeed())
		Expect(receivedUA).To(Equal("test-agent"))

		data, err := os.ReadFile(destPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal("custom"))
	})

	It("should respect custom client timeouts", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(500 * time.Millisecond)
			w.Write([]byte("slow")) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		short := &http.Client{
			Transport: &http.Transport{
				DialContext: (&net.Dialer{Timeout: 1 * time.Second}).DialContext,
			},
			Timeout: 100 * time.Millisecond,
		}

		dir := GinkgoT().TempDir()
		destPath := filepath.Join(dir, "should-not-exist")

		Expect(Download(context.Background(), short, ts.URL, destPath)).To(HaveOccurred())
	})
})

var _ = Describe("FetchContent", func() {
	It("should fetch and return content", func() {
		expected := "shasum file content"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte(expected)) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		data, err := FetchContent(context.Background(), nil, ts.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(expected))
	})

	It("should return error on 404", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusNotFound)
		}))
		defer ts.Close()

		_, err := FetchContent(context.Background(), nil, ts.URL)
		Expect(err).To(HaveOccurred())
	})

	It("should reject responses exceeding the size limit", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			// Write may fail if client disconnects early; that's expected.
			w.Write([]byte(strings.Repeat("x", maxFetchSize+1))) //nolint:errcheck // client may disconnect
		}))
		defer ts.Close()

		_, err := FetchContent(context.Background(), nil, ts.URL)
		Expect(err).To(HaveOccurred())
	})

	It("should return error on canceled context", func() {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Write([]byte("data")) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		_, err := FetchContent(ctx, nil, ts.URL)
		Expect(err).To(HaveOccurred())
	})

	It("should use a provided custom client", func() {
		var receivedUA string
		expected := "fetched with custom"
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			receivedUA = r.Header.Get("User-Agent")
			w.Write([]byte(expected)) //nolint:errcheck // test handler
		}))
		defer ts.Close()

		custom := &http.Client{
			Transport: &customHeaderTransport{
				header: "User-Agent",
				value:  "fetch-agent",
				base:   http.DefaultTransport,
			},
		}

		data, err := FetchContent(context.Background(), custom, ts.URL)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(data)).To(Equal(expected))
		Expect(receivedUA).To(Equal("fetch-agent"))
	})
})
