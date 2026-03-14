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

func TestConditionalBoot_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		directive  bootDirectiveFunc
		url        string
		wantStatus int
	}{
		{"ok", fixedDirective(), "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", http.StatusOK},
		{"no match", noMatchDirective(), "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", http.StatusNotFound},
		{"duplicate", duplicateDirective(), "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", http.StatusConflict},
		{"internal error", errorDirective(), "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", http.StatusInternalServerError},
		{"missing mac", fixedDirective(), "/conditional-boot", http.StatusBadRequest},
		{"empty mac", fixedDirective(), "/conditional-boot?mac=", http.StatusBadRequest},
		{"invalid mac format", fixedDirective(), "/conditional-boot?mac=not-a-mac", http.StatusBadRequest},
		{"colon mac rejected", fixedDirective(), "/conditional-boot?mac=aa:bb:cc:dd:ee:ff", http.StatusBadRequest},
		{"mac injection", fixedDirective(), "/conditional-boot?mac=aa-bb-cc-dd-ee-ff%0aboot", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := conditionalBootHandler(tt.directive)
			req := httptest.NewRequest(http.MethodGet, tt.url, nil)
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Result().StatusCode != tt.wantStatus {
				t.Errorf("expected %d, got: %d", tt.wantStatus, w.Result().StatusCode)
			}
		})
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

func TestConditionalBoot_BootDirective(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective())
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	body, _ := io.ReadAll(w.Result().Body)
	expected := "#!ipxe\nkernel /static/test-config/kernel/vmlinuz console=ttyS0\n" +
		"initrd /static/test-config/initrd/initrd.img\n" +
		"boot\n"
	if string(body) != expected {
		t.Errorf("expected:\n%s\ngot:\n%s", expected, string(body))
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
