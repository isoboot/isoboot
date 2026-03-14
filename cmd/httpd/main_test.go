package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isoboot/isoboot/internal/httpd"
)

func fixedDirective() bootDirectiveFunc {
	return func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath: "test-config/kernel/vmlinuz",
			KernelArgs: "console=ttyS0",
			InitrdPath: "test-config/initrd/initrd.img",
		}, nil
	}
}

func noMatchDirective() bootDirectiveFunc {
	return func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return nil, nil
	}
}

func duplicateDirective() bootDirectiveFunc {
	return func(_ context.Context, mac string) (*httpd.BootDirective, error) {
		return nil, fmt.Errorf("%w with MAC %s", httpd.ErrMultipleMachines, mac)
	}
}

func errorDirective() bootDirectiveFunc {
	return func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return nil, errors.New("listing machines: connection refused")
	}
}

func TestConditionalBoot_BootDirective(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got: %d", w.Result().StatusCode)
	}
	body, _ := io.ReadAll(w.Result().Body)
	expected := "#!ipxe\nkernel /static/test-config/kernel/vmlinuz console=ttyS0\n" +
		"initrd /static/test-config/initrd/initrd.img\n" +
		"boot\n"
	if string(body) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, string(body))
	}
}

func TestConditionalBoot_ContentType(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	ct := w.Result().Header.Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type text/plain; charset=utf-8, got: %s", ct)
	}
}

func TestConditionalBoot_NoMatch(t *testing.T) {
	handler := conditionalBootHandler(noMatchDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_DuplicateMatch(t *testing.T) {
	handler := conditionalBootHandler(duplicateDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusConflict {
		t.Errorf("expected 409, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_InternalError(t *testing.T) {
	handler := conditionalBootHandler(errorDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_MissingMac(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_EmptyMac(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_InvalidMacFormat(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=not-a-mac", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_ColonMacRejected(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa:bb:cc:dd:ee:ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_MacInjection(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff%0aboot", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got: %d", w.Result().StatusCode)
	}
}

func TestConditionalBoot_NoKernelArgs(t *testing.T) {
	handler := conditionalBootHandler(func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath: "config/kernel/vmlinuz",
			InitrdPath: "config/initrd/initrd.img",
		}, nil
	})
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	if strings.Contains(string(body), "vmlinuz ") {
		t.Errorf("unexpected trailing space after kernel path: %s", body)
	}
	expected := "#!ipxe\nkernel /static/config/kernel/vmlinuz\ninitrd /static/config/initrd/initrd.img\nboot\n"
	if string(body) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, string(body))
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
