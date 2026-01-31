package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

// mockBootClient implements BootClient for testing.
type mockBootClient struct {
	getConfigMapValue      func(ctx context.Context, configMapName, key string) (string, error)
	getMachineByMAC        func(ctx context.Context, mac string) (string, error)
	getProvisionsByMachine func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error)
	getBootTarget          func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error)
	getBootSource           func(ctx context.Context, name string) (*controllerclient.BootSourceInfo, error)
	updateProvisionStatus  func(ctx context.Context, name, status, message, ip string) error
}

func (m *mockBootClient) GetConfigMapValue(ctx context.Context, configMapName, key string) (string, error) {
	return m.getConfigMapValue(ctx, configMapName, key)
}
func (m *mockBootClient) GetMachineByMAC(ctx context.Context, mac string) (string, error) {
	return m.getMachineByMAC(ctx, mac)
}
func (m *mockBootClient) GetProvisionsByMachine(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
	return m.getProvisionsByMachine(ctx, machineName)
}
func (m *mockBootClient) GetBootTarget(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
	return m.getBootTarget(ctx, name)
}
func (m *mockBootClient) GetBootSource(ctx context.Context, name string) (*controllerclient.BootSourceInfo, error) {
	return m.getBootSource(ctx, name)
}
func (m *mockBootClient) UpdateProvisionStatus(ctx context.Context, name, status, message, ip string) error {
	return m.updateProvisionStatus(ctx, name, status, message, ip)
}

func TestServeBootDone_MACNormalization(t *testing.T) {
	// Test that MAC addresses are normalized to lowercase
	tests := []struct {
		input    string
		expected string
	}{
		{"AA-BB-CC-DD-EE-FF", "aa-bb-cc-dd-ee-ff"},
		{"aa-bb-cc-dd-ee-ff", "aa-bb-cc-dd-ee-ff"},
		{"Aa-Bb-Cc-Dd-Ee-Ff", "aa-bb-cc-dd-ee-ff"},
	}

	for _, tt := range tests {
		mac := tt.input
		mac = strings.ToLower(mac)
		if mac != tt.expected {
			t.Errorf("MAC normalization: input %q, got %q, want %q", tt.input, mac, tt.expected)
		}
	}
}

func TestSplitHostDomain(t *testing.T) {
	tests := []struct {
		name         string
		wantHostname string
		wantDomain   string
	}{
		{"abc.lan", "abc", "lan"},
		{"web.example.com", "web", "example.com"},
		{"server01", "server01", ""},
		{"vm-deb-0099.internal.example.com", "vm-deb-0099", "internal.example.com"},
		{"", "", ""},
		{".domain", "", "domain"},
	}

	for _, tt := range tests {
		hostname, domain := splitHostDomain(tt.name)
		if hostname != tt.wantHostname {
			t.Errorf("splitHostDomain(%q) hostname = %q, want %q", tt.name, hostname, tt.wantHostname)
		}
		if domain != tt.wantDomain {
			t.Errorf("splitHostDomain(%q) domain = %q, want %q", tt.name, domain, tt.wantDomain)
		}
	}
}

func TestPortFromRequest(t *testing.T) {
	tests := []struct {
		name          string
		forwardedPort string
		host          string
		expectedPort  string
	}{
		{"forwarded port only", "8080", "example.com", "8080"},
		{"host port only", "", "example.com:9090", "9090"},
		{"both set, forwarded wins", "8080", "example.com:9090", "8080"},
		{"default port 80", "", "example.com", "80"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/", nil)
			req.Host = tt.host
			if tt.forwardedPort != "" {
				req.Header.Set("X-Forwarded-Port", tt.forwardedPort)
			}
			got := portFromRequest(req)
			if got != tt.expectedPort {
				t.Errorf("portFromRequest() = %q, want %q", got, tt.expectedPort)
			}
		})
	}
}

func TestServeConditionalBoot_PendingProvision(t *testing.T) {
	var updatedName, updatedStatus string
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "debian-13"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				BootSourceRef: "debian-13",
				Template:     "#!ipxe\nkernel http://{{ .Host }}:{{ .Port }}/static/{{ .BootSource }}/{{ .KernelFilename }}\nboot\n",
			}, nil
		},
		getBootSource: func(ctx context.Context, name string) (*controllerclient.BootSourceInfo, error) {
			return &controllerclient.BootSourceInfo{
				KernelFilename: "linux",
				InitrdFilename: "initrd.gz",
				HasFirmware:    false,
			}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			updatedName = name
			updatedStatus = status
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "isoboot-templates")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	req.Header.Set("X-Forwarded-Port", "8080")
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "10.0.0.1:8080") {
		t.Errorf("expected host:port in body, got %q", body)
	}
	if !strings.Contains(body, "static/debian-13/linux") {
		t.Errorf("expected static file path in body, got %q", body)
	}
	if updatedName != "prov-1" || updatedStatus != "InProgress" {
		t.Errorf("expected provision prov-1 marked InProgress, got name=%q status=%q", updatedName, updatedStatus)
	}
}

func TestServeConditionalBoot_BootSourceAndFirmwareRendered(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "debian-13-firmware"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				BootSourceRef: "debian-13",
				UseFirmware:  true,
				Template:     "kernel /static/{{ .BootSource }}/{{ .KernelFilename }} fw={{ .UseFirmware }}",
			}, nil
		},
		getBootSource: func(ctx context.Context, name string) (*controllerclient.BootSourceInfo, error) {
			return &controllerclient.BootSourceInfo{KernelFilename: "linux", InitrdFilename: "initrd.gz", HasFirmware: true}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "fw=true") {
		t.Errorf("expected UseFirmware=true in body, got %q", body)
	}
	if !strings.Contains(body, "static/debian-13/linux") {
		t.Errorf("expected static file path in body, got %q", body)
	}
}

func TestServeConditionalBoot_NewTemplateVariables(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "ubuntu-24"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				BootSourceRef: "ubuntu-24",
				Template:     "kernel={{ .KernelFilename }} initrd={{ .InitrdFilename }} fw={{ .HasFirmware }}",
			}, nil
		},
		getBootSource: func(ctx context.Context, name string) (*controllerclient.BootSourceInfo, error) {
			return &controllerclient.BootSourceInfo{KernelFilename: "vmlinuz", InitrdFilename: "initrd.gz", HasFirmware: true}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	body := w.Body.String()
	if !strings.Contains(body, "kernel=vmlinuz") {
		t.Errorf("expected KernelFilename in body, got %q", body)
	}
	if !strings.Contains(body, "initrd=initrd.gz") {
		t.Errorf("expected InitrdFilename in body, got %q", body)
	}
	if !strings.Contains(body, "fw=true") {
		t.Errorf("expected HasFirmware=true in body, got %q", body)
	}
}

func TestServeConditionalBoot_NoMachine(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "", nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeConditionalBoot_NoPendingProvision(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Complete", BootTargetRef: "debian-13"},
			}, nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}

func TestServeBootDone_Success(t *testing.T) {
	var updatedName, updatedStatus string
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "InProgress", BootTargetRef: "debian-13"},
			}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			updatedName = name
			updatedStatus = status
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/done?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", w.Body.String())
	}
	if updatedName != "prov-1" || updatedStatus != "Complete" {
		t.Errorf("expected prov-1 marked Complete, got name=%q status=%q", updatedName, updatedStatus)
	}
}

func TestServeConditionalBoot_NoMAC(t *testing.T) {
	h := NewBootHandler("10.0.0.1", "3128", &mockBootClient{}, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServeConditionalBoot_EmptyStatusTreatedAsPending(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "", BootTargetRef: "debian-13"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				BootSourceRef: "debian-13",
				Template:     "#!ipxe\nboot\n",
			}, nil
		},
		getBootSource: func(ctx context.Context, name string) (*controllerclient.BootSourceInfo, error) {
			return &controllerclient.BootSourceInfo{}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 (empty status treated as Pending), got %d", w.Code)
	}
}

func TestServeConditionalBoot_BootSourceNotFound(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "debian-13"},
			}, nil
		},
		getBootTarget: func(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error) {
			return &controllerclient.BootTargetInfo{
				BootSourceRef: "missing-media",
				Template:     "#!ipxe\nboot\n",
			}, nil
		},
		getBootSource: func(ctx context.Context, name string) (*controllerclient.BootSourceInfo, error) {
			return nil, fmt.Errorf("not found")
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/conditional-boot?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeConditionalBoot(w, req)

	if w.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", w.Code)
	}
}

func TestServeBootDone_NoMAC(t *testing.T) {
	h := NewBootHandler("10.0.0.1", "3128", &mockBootClient{}, "cm")
	req := httptest.NewRequest("GET", "/boot/done", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", w.Code)
	}
}

func TestServeBootDone_UpdateStatusError(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "InProgress", BootTargetRef: "debian-13"},
			}, nil
		},
		updateProvisionStatus: func(ctx context.Context, name, status, message, ip string) error {
			return fmt.Errorf("grpc call: connection refused")
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/done?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", w.Code)
	}
}

func TestServeBootDone_NoInProgress(t *testing.T) {
	mock := &mockBootClient{
		getMachineByMAC: func(ctx context.Context, mac string) (string, error) {
			return "vm-01.lan", nil
		},
		getProvisionsByMachine: func(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error) {
			return []controllerclient.ProvisionSummary{
				{Name: "prov-1", Status: "Pending", BootTargetRef: "debian-13"},
			}, nil
		},
	}

	h := NewBootHandler("10.0.0.1", "3128", mock, "cm")
	req := httptest.NewRequest("GET", "/boot/done?mac=aa-bb-cc-dd-ee-ff", nil)
	w := httptest.NewRecorder()

	h.ServeBootDone(w, req)

	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d", w.Code)
	}
}
