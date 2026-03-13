package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testHost = "192.168.1.1:8080"

func mustHandler(t *testing.T) http.HandlerFunc {
	t.Helper()
	h, err := conditionalBootHandler(":8080")
	if err != nil {
		t.Fatalf("conditionalBootHandler: %v", err)
	}
	return h
}

func TestConditionalBoot_ValidIPXEHeader(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.HasPrefix(string(body), "#!ipxe\n") {
		t.Errorf("expected body to start with #!ipxe header, got: %s", body)
	}
}

func TestConditionalBoot_ContentType(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	ct := w.Result().Header.Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type text/plain; charset=utf-8, got: %s", ct)
	}
}

func TestConditionalBoot_BothForwardedHeaders(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	req.Header.Set("X-Forwarded-Host", "proxy.example.com")
	req.Header.Set("X-Forwarded-Port", "443")
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "proxy.example.com:443") {
		t.Errorf("expected forwarded host:port in response, got: %s", body)
	}
}

func TestConditionalBoot_FallbackToHostHeader(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "192.168.1.1:8080") {
		t.Errorf("expected host header host + listener port in response, got: %s", body)
	}
}

func TestConditionalBoot_HostWithPort_UsesListenerPort(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = "192.168.101.2:9999"
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "192.168.101.2:8080") {
		t.Errorf("expected host from Host header + listener port, got: %s", body)
	}
}

func TestConditionalBoot_OnlyForwardedHost(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	req.Header.Set("X-Forwarded-Host", "proxy.example.com")
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "proxy.example.com:8080") {
		t.Errorf("expected forwarded host + listener port, got: %s", body)
	}
}

func TestConditionalBoot_OnlyForwardedPort(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	req.Header.Set("X-Forwarded-Port", "443")
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "192.168.1.1:443") {
		t.Errorf("expected host header host + forwarded port, got: %s", body)
	}
}

func TestConditionalBoot_MissingHostHeader(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = ""
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestHealthz(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()

	healthzHandler(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_MissingMac(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_EmptyMac(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_InvalidMacFormat(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=not-a-mac", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_ColonMacRejected(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa:bb:cc:dd:ee:ff", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_MacInjection(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff%0aboot", nil)
	req.Host = testHost
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_InvalidHost(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	req.Header.Set("X-Forwarded-Host", "evil.com/exploit#")
	req.Header.Set("X-Forwarded-Port", "443")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_InvalidPort(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	req.Header.Set("X-Forwarded-Port", "443/evil#")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_PortOutOfRange(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	req.Header.Set("X-Forwarded-Port", "99999")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_ForwardedHostWithPort(t *testing.T) {
	handler := mustHandler(t)
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = testHost
	req.Header.Set("X-Forwarded-Host", "proxy.example.com:443")
	req.Header.Set("X-Forwarded-Port", "443")
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if strings.Contains(string(body), "proxy.example.com:443:443") {
		t.Errorf("expected port stripped from X-Forwarded-Host, got: %s", body)
	}
	if !strings.Contains(string(body), "proxy.example.com:443") {
		t.Errorf("expected proxy.example.com:443 in response, got: %s", body)
	}
}

func TestConditionalBootHandler_InvalidListenAddr(t *testing.T) {
	_, err := conditionalBootHandler("bad-addr")
	if err == nil {
		t.Error("expected error for malformed listen address")
	}
}
