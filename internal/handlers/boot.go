package handlers

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"text/template"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

// BootClient defines the controller operations needed by BootHandler.
type BootClient interface {
	GetConfigMapValue(ctx context.Context, configMapName, key string) (string, error)
	GetMachineByMAC(ctx context.Context, mac string) (string, error)
	GetProvisionsByMachine(ctx context.Context, machineName string) ([]controllerclient.ProvisionSummary, error)
	GetBootTarget(ctx context.Context, name string) (*controllerclient.BootTargetInfo, error)
	UpdateProvisionStatus(ctx context.Context, name, status, message, ip string) error
}

type BootHandler struct {
	host       string
	proxyPort  string
	ctrlClient BootClient
	configMap  string
}

func NewBootHandler(host, proxyPort string, ctrlClient BootClient, configMap string) *BootHandler {
	return &BootHandler{
		host:       host,
		proxyPort:  proxyPort,
		ctrlClient: ctrlClient,
		configMap:  configMap,
	}
}

// TemplateData is passed to boot templates
type TemplateData struct {
	Host              string
	Port              string
	ProxyPort         string // Squid proxy port for mirror/http/proxy
	MachineName       string // Full machine name (e.g., "vm-deb-0099.lan")
	Hostname          string // First part before dot (e.g., "vm-deb-0099")
	Domain            string // Everything after first dot (e.g., "lan")
	BootTarget        string
	BootMedia         string // BootMedia resource name (for static file paths)
	UseFirmware bool   // Whether to use firmware-combined initrd
	ProvisionName     string // Provision resource name (use for answer file URLs)
	KernelFilename    string // e.g., "linux" or "vmlinuz"
	InitrdFilename    string // e.g., "initrd.gz"
	HasFirmware       bool   // Whether BootMedia has firmware
}

// portFromRequest returns the X-Forwarded-Port header if present,
// otherwise extracts the port from the Host header.
func portFromRequest(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-Port"); fwd != "" {
		return fwd
	}
	_, port, err := net.SplitHostPort(r.Host)
	if err == nil && port != "" {
		return port
	}
	return "80"
}

// splitHostDomain splits a machine name into hostname and domain.
// "abc.lan" -> ("abc", "lan")
// "web.example.com" -> ("web", "example.com")
// "server01" -> ("server01", "")
func splitHostDomain(name string) (hostname, domain string) {
	if idx := strings.Index(name, "."); idx != -1 {
		return name[:idx], name[idx+1:]
	}
	return name, ""
}

func (h *BootHandler) loadTemplate(ctx context.Context, name string) (*template.Template, error) {
	value, err := h.ctrlClient.GetConfigMapValue(ctx, h.configMap, name)
	if err != nil {
		return nil, err
	}
	return template.New(name).Parse(value)
}

// ServeConditionalBoot checks Provision CRDs and returns appropriate boot script
// Returns 404 with Content-Length if no provision found (iPXE falls back to local boot)
func (h *BootHandler) ServeConditionalBoot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	mac := r.URL.Query().Get("mac")
	if mac == "" {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// MAC must be dash-separated (xx-xx-xx-xx-xx-xx from iPXE)
	mac = strings.ToLower(mac)

	// 1. Find Machine by MAC
	machineName, err := h.ctrlClient.GetMachineByMAC(ctx, mac)
	if err != nil {
		log.Printf("Error getting machine for MAC %s: %v", mac, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if machineName == "" {
		// No machine with this MAC - return 404 so iPXE falls back to local boot
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// 2. Get Provisions for this Machine
	provisions, err := h.ctrlClient.GetProvisionsByMachine(ctx, machineName)
	if err != nil {
		log.Printf("Error getting provisions for machine %s: %v", machineName, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 3. Find Pending provision
	var pendingProvision *controllerclient.ProvisionSummary
	for i := range provisions {
		status := provisions[i].Status
		if status == "" {
			status = "Pending" // Empty status treated as Pending
		}
		if status == "Pending" {
			pendingProvision = &provisions[i]
			break
		}
	}

	if pendingProvision == nil {
		// No pending provision - return 404 so iPXE falls back to local boot
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// 4. Get BootTarget
	bootTarget, err := h.ctrlClient.GetBootTarget(ctx, pendingProvision.BootTargetRef)
	if err != nil {
		log.Printf("Error loading BootTarget %s: %v", pendingProvision.BootTargetRef, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// 5. Parse and render template
	tmpl, err := template.New(pendingProvision.BootTargetRef).Parse(bootTarget.Template)
	if err != nil {
		log.Printf("Error parsing template for %s: %v", pendingProvision.BootTargetRef, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	hostname, domain := splitHostDomain(machineName)
	data := TemplateData{
		Host:              h.host,
		Port:              portFromRequest(r),
		ProxyPort:         h.proxyPort,
		MachineName:       machineName,
		Hostname:          hostname,
		Domain:            domain,
		BootTarget:        pendingProvision.BootTargetRef,
		BootMedia:         bootTarget.BootMediaRef,
		UseFirmware: bootTarget.UseFirmware,
		ProvisionName:     pendingProvision.Name,
		KernelFilename:    bootTarget.KernelFilename,
		InitrdFilename:    bootTarget.InitrdFilename,
		HasFirmware:       bootTarget.HasFirmware,
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", buf.Len()))
	w.WriteHeader(http.StatusOK)
	w.Write(buf.Bytes())

	// 6. Mark provision as InProgress
	if err := h.ctrlClient.UpdateProvisionStatus(ctx, pendingProvision.Name, "InProgress", "Boot script served", ""); err != nil {
		log.Printf("Warning: failed to mark boot started for %s: %v", pendingProvision.Name, err)
	}
}

// ServeBootDone marks a provision as completed
// GET /boot/done?mac={mac}
func (h *BootHandler) ServeBootDone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	mac := r.URL.Query().Get("mac")
	if mac == "" {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// MAC must be dash-separated (xx-xx-xx-xx-xx-xx)
	mac = strings.ToLower(mac)

	// Extract client IP from RemoteAddr (handles both IPv4 and IPv6)
	// We use RemoteAddr directly since isoboot-http uses hostNetwork with no proxy
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr // fallback if no port present
	}

	// Find machine by MAC
	machineName, err := h.ctrlClient.GetMachineByMAC(ctx, mac)
	if err != nil {
		log.Printf("Error getting machine for MAC %s: %v", mac, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if machineName == "" {
		log.Printf("No machine found for MAC %s", mac)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Find InProgress provision for this machine
	provisions, err := h.ctrlClient.GetProvisionsByMachine(ctx, machineName)
	if err != nil {
		log.Printf("Error getting provisions for machine %s: %v", machineName, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Find InProgress provision
	var inProgressProvision *controllerclient.ProvisionSummary
	for i := range provisions {
		if provisions[i].Status == "InProgress" {
			inProgressProvision = &provisions[i]
			break
		}
	}

	if inProgressProvision == nil {
		log.Printf("No InProgress provision found for MAC %s (machine %s)", mac, machineName)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Update provision status to Complete
	if err := h.ctrlClient.UpdateProvisionStatus(ctx, inProgressProvision.Name, "Complete", "Installation completed", ip); err != nil {
		log.Printf("Error marking boot completed for %s: %v", inProgressProvision.Name, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Length", "2")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

// RegisterRoutes registers boot-related routes
func (h *BootHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/boot/conditional-boot", h.ServeConditionalBoot)
	mux.HandleFunc("/boot/done", h.ServeBootDone)
}
