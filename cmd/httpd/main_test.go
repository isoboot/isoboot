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

	isobootgithubiov1alpha1 "github.com/isoboot/isoboot/api/v1alpha1"
	"github.com/isoboot/isoboot/internal/httpd"
	"k8s.io/apimachinery/pkg/runtime/schema"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

func fixedDirective() bootDirectiveFunc {
	return func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath:    "test-config/kernel/vmlinuz",
			KernelArgs:    "console=ttyS0",
			InitrdPath:    "test-config/initrd/initrd.img",
			ProvisionName: "test-provision",
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
			handler := conditionalBootHandler(tt.directive, "")
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
	handler := conditionalBootHandler(fixedDirective(), "")
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	ct := w.Result().Header.Get("Content-Type")
	if ct != "text/plain; charset=utf-8" {
		t.Errorf("expected Content-Type text/plain; charset=utf-8, got: %s", ct)
	}
}

func TestConditionalBoot_BootDirective(t *testing.T) {
	handler := conditionalBootHandler(fixedDirective(), "")
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
	}, "")
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

func TestConditionalBoot_TemplateRendering(t *testing.T) {
	handler := conditionalBootHandler(func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath:    "config/kernel/vmlinuz",
			KernelArgs:    "ip=dhcp inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg",
			InitrdPath:    "config/initrd/initrd.img",
			ProvisionName: "my-provision",
		}, nil
	}, "")
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Header.Set("X-Forwarded-Host", "10.0.0.1")
	req.Header.Set("X-Forwarded-Port", "8080")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got: %d", w.Result().StatusCode)
	}
	body, _ := io.ReadAll(w.Result().Body)
	expected := "ip=dhcp inst.ks=http://10.0.0.1:8080/dynamic/automation/my-provision/ks.cfg"
	if !strings.Contains(string(body), expected) {
		t.Errorf("expected body to contain:\n%s\ngot:\n%s", expected, body)
	}
}

func TestConditionalBoot_TemplateRenderingFallback(t *testing.T) {
	handler := conditionalBootHandler(func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath:    "config/kernel/vmlinuz",
			KernelArgs:    "ip=dhcp inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg",
			InitrdPath:    "config/initrd/initrd.img",
			ProvisionName: "my-provision",
		}, nil
	}, "")
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = "10.0.0.1:8080"
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got: %d", w.Result().StatusCode)
	}
	body, _ := io.ReadAll(w.Result().Body)
	expected := "ip=dhcp inst.ks=http://10.0.0.1:8080/dynamic/automation/my-provision/ks.cfg"
	if !strings.Contains(string(body), expected) {
		t.Errorf("expected body to contain:\n%s\ngot:\n%s", expected, body)
	}
}

func TestConditionalBoot_ProxyURL(t *testing.T) {
	handler := conditionalBootHandler(func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath:    "config/kernel/vmlinuz",
			KernelArgs:    "ip=dhcp inst.proxy={{.ProxyURL}} inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg",
			InitrdPath:    "config/initrd/initrd.img",
			ProvisionName: "my-provision",
		}, nil
	}, "3128")
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Header.Set("X-Forwarded-Host", "10.0.0.1")
	req.Header.Set("X-Forwarded-Port", "8080")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got: %d", w.Result().StatusCode)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "inst.proxy=http://10.0.0.1:3128") {
		t.Errorf("expected body to contain inst.proxy=http://10.0.0.1:3128, got:\n%s", body)
	}
	if !strings.Contains(string(body), "inst.ks=http://10.0.0.1:8080/dynamic/automation/my-provision/ks.cfg") {
		t.Errorf("expected body to contain inst.ks URL, got:\n%s", body)
	}
}

func TestConditionalBoot_TemplateError(t *testing.T) {
	handler := conditionalBootHandler(func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath:    "config/kernel/vmlinuz",
			KernelArgs:    "{{.UnknownVar}}",
			InitrdPath:    "config/initrd/initrd.img",
			ProvisionName: "my-provision",
		}, nil
	}, "")
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got: %d", w.Result().StatusCode)
	}
}

func noopUpdatePhase() updatePhaseFunc {
	return func(_ context.Context, _ string,
		_ isobootgithubiov1alpha1.ProvisionPhase, _ string,
	) error {
		return nil
	}
}

func TestUpdateStatus_StatusCodes(t *testing.T) {
	tests := []struct {
		name       string
		update     updatePhaseFunc
		body       string
		wantStatus int
	}{
		{
			"ok InProgress",
			noopUpdatePhase(),
			"provisionName=my-provision&phase=InProgress",
			http.StatusOK,
		},
		{
			"ok Complete",
			noopUpdatePhase(),
			"provisionName=my-provision&phase=Complete",
			http.StatusOK,
		},
		{
			"wrong phase",
			func(_ context.Context, _ string,
				_ isobootgithubiov1alpha1.ProvisionPhase, _ string,
			) error {
				return fmt.Errorf(
					"%w: cannot transition from Pending to Complete",
					httpd.ErrInvalidPhaseTransition)
			},
			"provisionName=my-provision&phase=Complete",
			http.StatusConflict,
		},
		{
			"not found",
			func(_ context.Context, _ string,
				_ isobootgithubiov1alpha1.ProvisionPhase, _ string,
			) error {
				return fmt.Errorf("getting provision %q: %w",
					"missing",
					apierrors.NewNotFound(
						schema.GroupResource{
							Group:    "isoboot.github.io",
							Resource: "provisions",
						}, "missing"))
			},
			"provisionName=missing&phase=InProgress",
			http.StatusNotFound,
		},
		{
			"internal error",
			func(_ context.Context, _ string,
				_ isobootgithubiov1alpha1.ProvisionPhase, _ string,
			) error {
				return errors.New("connection refused")
			},
			"provisionName=my-provision&phase=InProgress",
			http.StatusInternalServerError,
		},
		{
			"missing provisionName",
			noopUpdatePhase(),
			"phase=InProgress",
			http.StatusBadRequest,
		},
		{
			"missing phase",
			noopUpdatePhase(),
			"provisionName=my-provision",
			http.StatusBadRequest,
		},
		{
			"invalid phase",
			noopUpdatePhase(),
			"provisionName=my-provision&phase=Failed",
			http.StatusBadRequest,
		},
		{
			"invalid name",
			noopUpdatePhase(),
			"provisionName=INVALID_NAME&phase=InProgress",
			http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := updateStatusHandler(tt.update)
			req := httptest.NewRequest(http.MethodPost, "/status",
				strings.NewReader(tt.body))
			req.Header.Set("Content-Type",
				"application/x-www-form-urlencoded")
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Result().StatusCode != tt.wantStatus {
				t.Errorf("expected %d, got: %d",
					tt.wantStatus, w.Result().StatusCode)
			}
		})
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

func isoDirective(args string) bootDirectiveFunc {
	return func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath:    "ubuntu-26.04/vmlinuz",
			KernelArgs:    args,
			InitrdPath:    "ubuntu-26.04/initrd",
			NFSExport:     "/ubuntu-26.04",
			ProvisionName: "my-provision",
		}, nil
	}
}

func TestConditionalBoot_NFSRoot(t *testing.T) {
	args := "ip=dhcp netboot=nfs nfsroot={{.NFSRoot}} fsck.mode=skip " +
		"autoinstall ds=nocloud;s={{.ProvisionAutomationBaseURL}}/ ---"
	tests := []struct {
		name       string
		host       string // Host header
		fwdHost    string // X-Forwarded-Host
		fwdPort    string // X-Forwarded-Port
		wantStatus int
		want       string
	}{
		{"forwarded ipv4", "", "10.0.0.1", "8080", http.StatusOK,
			"nfsroot=10.0.0.1:/ubuntu-26.04 fsck.mode=skip " +
				"autoinstall ds=nocloud;s=http://10.0.0.1:8080/dynamic/automation/my-provision/ ---"},
		{"host header ipv4", "192.168.1.5:8080", "", "", http.StatusOK, "nfsroot=192.168.1.5:/ubuntu-26.04 "},
		{"ipv4 without port", "192.168.1.5", "", "", http.StatusOK, "nfsroot=192.168.1.5:/ubuntu-26.04 "},
		{"hostname", "isoboot.example:8080", "", "", http.StatusInternalServerError, ""},
		{"ipv6", "[fd00::1]:8080", "", "", http.StatusInternalServerError, ""},
		{"ipv4-mapped ipv6", "[::ffff:10.0.0.1]:8080", "", "", http.StatusInternalServerError, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := conditionalBootHandler(isoDirective(args), "")
			req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
			if tt.host != "" {
				req.Host = tt.host
			}
			if tt.fwdHost != "" {
				req.Header.Set("X-Forwarded-Host", tt.fwdHost)
				req.Header.Set("X-Forwarded-Port", tt.fwdPort)
			}
			w := httptest.NewRecorder()

			handler(w, req)

			if w.Result().StatusCode != tt.wantStatus {
				t.Fatalf("expected %d, got: %d", tt.wantStatus, w.Result().StatusCode)
			}
			body, _ := io.ReadAll(w.Result().Body)
			if tt.wantStatus == http.StatusOK && !strings.Contains(string(body), tt.want) {
				t.Errorf("expected body to contain:\n%s\ngot:\n%s", tt.want, body)
			}
			if tt.wantStatus != http.StatusOK && strings.Contains(string(body), "nfsroot=") {
				t.Errorf("error body must not carry a command line: %s", body)
			}
		})
	}
}

func TestConditionalBoot_NFSRootEmptyInNetbootMode(t *testing.T) {
	handler := conditionalBootHandler(func(_ context.Context, _ string) (*httpd.BootDirective, error) {
		return &httpd.BootDirective{
			KernelPath:    "config/kernel/vmlinuz",
			KernelArgs:    "ip=dhcp root=[{{.NFSRoot}}]",
			InitrdPath:    "config/initrd/initrd.img",
			ProvisionName: "my-provision",
		}, nil
	}, "")
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = "isoboot.example:8080" // a hostname is fine without NFS
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got: %d", w.Result().StatusCode)
	}
	body, _ := io.ReadAll(w.Result().Body)
	if !strings.Contains(string(body), "root=[]") {
		t.Errorf("expected empty NFSRoot, got:\n%s", body)
	}
}

func TestConditionalBoot_ISOURLRemoved(t *testing.T) {
	handler := conditionalBootHandler(isoDirective("url={{.ISOURL}}"), "")
	req := httptest.NewRequest(http.MethodGet, "/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Host = "10.0.0.1:8080"
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500 for removed {{.ISOURL}}, got: %d", w.Result().StatusCode)
	}
}

func TestAutomationFile_URLs(t *testing.T) {
	tests := []struct {
		name      string
		proxyPort string
		wantProxy string
	}{
		{"squid on", "3128", "http://10.0.0.1:3128"},
		{"squid off", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var gotStatus, gotProxy string
			render := func(_ context.Context, _, _, statusURL, proxyURL string) (string, error) {
				gotStatus, gotProxy = statusURL, proxyURL
				return "ok", nil
			}
			mux := http.NewServeMux()
			mux.HandleFunc("GET /automation/{provisionName}/{fileName}", automationFileHandler(render, tt.proxyPort))
			req := httptest.NewRequest(http.MethodGet, "/automation/my-provision/user-data", nil)
			req.Header.Set("X-Forwarded-Host", "10.0.0.1")
			req.Header.Set("X-Forwarded-Port", "8080")
			w := httptest.NewRecorder()

			mux.ServeHTTP(w, req)

			if w.Result().StatusCode != http.StatusOK {
				t.Fatalf("expected 200, got: %d", w.Result().StatusCode)
			}
			if gotStatus != "http://10.0.0.1:8080/dynamic/status" {
				t.Errorf("status URL: got %q", gotStatus)
			}
			if gotProxy != tt.wantProxy {
				t.Errorf("proxy URL: got %q, want %q", gotProxy, tt.wantProxy)
			}
		})
	}
}

func TestAutomationFile_NotFound(t *testing.T) {
	render := func(_ context.Context, _, _, _, _ string) (string, error) {
		return "", httpd.ErrFileNotFound
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /automation/{provisionName}/{fileName}", automationFileHandler(render, ""))
	req := httptest.NewRequest(http.MethodGet, "/automation/my-provision/nope", nil)
	w := httptest.NewRecorder()

	mux.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got: %d", w.Result().StatusCode)
	}
}
