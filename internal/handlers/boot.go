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
	proxyPort  string
	ctrlClient *controllerclient.Client
	configMap  string
}

func NewBootHandler(host, port, proxyPort string, ctrlClient *controllerclient.Client, configMap string) *BootHandler {
	return &BootHandler{
		host:       host,
		port:       port,
		proxyPort:  proxyPort,
		ctrlClient: ctrlClient,
		configMap:  configMap,
	}
}

// TemplateData is passed to boot templates
type TemplateData struct {
	Host          string
	Port          string
	ProxyPort     string // Squid proxy port for mirror/http/proxy
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
		Host:      h.host,
		Port:      h.port,
		ProxyPort: h.proxyPort,
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
		Host:          h.host,
		Port:          h.port,
		ProxyPort:     h.proxyPort,
		MachineName:   machineName,
		Hostname:      hostname,
		Domain:        domain,
		BootTarget:    pendingProvision.BootTargetRef,
		ProvisionName: pendingProvision.Name,
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
// GET /boot/done?provisionName={provisionName}
func (h *BootHandler) ServeBootDone(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	provisionName := r.URL.Query().Get("provisionName")
	if provisionName == "" {
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

	// Direct lookup by provision name
	provision, err := h.ctrlClient.GetProvision(ctx, provisionName)
	if err != nil {
		log.Printf("Error getting provision %s: %v", provisionName, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Verify provision is InProgress
	if provision.Status != "InProgress" {
		log.Printf("Provision %s is not InProgress (status: %s)", provisionName, provision.Status)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusConflict)
		return
	}

	// Update provision status to Complete
	if err := h.ctrlClient.UpdateProvisionStatus(ctx, provisionName, "Complete", "Installation completed", ip); err != nil {
		log.Printf("Error marking boot completed for %s: %v", provisionName, err)
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
