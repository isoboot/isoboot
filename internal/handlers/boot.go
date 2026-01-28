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

type BootHandler struct {
	host       string
	port       string
	ctrlClient *controllerclient.Client
	configMap  string
}

func NewBootHandler(host, port string, ctrlClient *controllerclient.Client, configMap string) *BootHandler {
	return &BootHandler{
		host:       host,
		port:       port,
		ctrlClient: ctrlClient,
		configMap:  configMap,
	}
}

// TemplateData is passed to boot templates
type TemplateData struct {
	Host          string
	Port          string
	MachineName   string // Full machine name (e.g., "vm-deb-0099.lan")
	Hostname      string // First part before dot (e.g., "vm-deb-0099")
	Domain        string // Everything after first dot (e.g., "lan")
	BootTarget    string
	ProvisionName string // Provision resource name (use for answer file URLs)
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

// ServeBootIPXE serves the initial boot.ipxe script
func (h *BootHandler) ServeBootIPXE(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	tmpl, err := h.loadTemplate(ctx, "boot.ipxe")
	if err != nil {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Host: h.host,
		Port: h.port,
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

	// Find pending provision for this MAC via controller
	bootInfo, err := h.ctrlClient.GetPendingBoot(ctx, mac)
	if err != nil {
		log.Printf("Error getting boot info for %s: %v", mac, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if bootInfo == nil {
		// No pending provision found - return 404 so iPXE falls back to local boot
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Load target template from BootTarget CRD
	bootTarget, err := h.ctrlClient.GetBootTarget(ctx, bootInfo.Target)
	if err != nil {
		log.Printf("Error loading BootTarget %s: %v", bootInfo.Target, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	tmpl, err := template.New(bootInfo.Target).Parse(bootTarget.Template)
	if err != nil {
		log.Printf("Error parsing template for %s: %v", bootInfo.Target, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	hostname, domain := splitHostDomain(bootInfo.MachineName)
	data := TemplateData{
		Host:          h.host,
		Port:          h.port,
		MachineName:   bootInfo.MachineName,
		Hostname:      hostname,
		Domain:        domain,
		BootTarget:    bootInfo.Target,
		ProvisionName: bootInfo.ProvisionName,
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

	// Mark provision as started via controller
	if err := h.ctrlClient.MarkBootStarted(ctx, mac); err != nil {
		log.Printf("Warning: failed to mark boot started for %s: %v", mac, err)
	}
}

// ServeBootDone marks a provision as completed
// GET /boot/done?id={machineName}
func (h *BootHandler) ServeBootDone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := r.URL.Query().Get("id")
	if id == "" {
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Extract client IP from RemoteAddr (handles both IPv4 and IPv6)
	// We use RemoteAddr directly since isoboot-http uses hostNetwork with no proxy
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		ip = r.RemoteAddr // fallback if no port present
	}

	if err := h.ctrlClient.MarkBootCompleted(ctx, id, ip); err != nil {
		log.Printf("Error marking boot completed for %s: %v", id, err)
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
	mux.HandleFunc("/boot/boot.ipxe", h.ServeBootIPXE)
	mux.HandleFunc("/boot/conditional-boot", h.ServeConditionalBoot)
	mux.HandleFunc("/boot/done", h.ServeBootDone)
}
