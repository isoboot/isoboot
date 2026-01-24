package handlers

import (
	"bytes"
	"context"
	"fmt"
	"log"
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
	Host     string
	Port     string
	Hostname string
}

func (h *BootHandler) loadTemplate(ctx context.Context, name string) (*template.Template, error) {
	content, err := h.ctrlClient.GetTemplate(ctx, name, h.configMap)
	if err != nil {
		return nil, err
	}
	return template.New(name).Parse(content)
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

// ServeConditionalBoot checks Deploy CRDs and returns appropriate boot script
// Returns 404 with Content-Length if no deploy found (iPXE falls back to local boot)
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

	// Find pending deploy for this MAC via controller
	bootInfo, err := h.ctrlClient.GetPendingBoot(ctx, mac)
	if err != nil {
		log.Printf("Error getting boot info for %s: %v", mac, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if bootInfo == nil {
		// No pending deploy found - return 404 so iPXE falls back to local boot
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// Load target template (e.g., debian-13.ipxe)
	templateName := bootInfo.Target + ".ipxe"
	tmpl, err := h.loadTemplate(ctx, templateName)
	if err != nil {
		log.Printf("Error loading template %s: %v", templateName, err)
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	data := TemplateData{
		Host:     h.host,
		Port:     h.port,
		Hostname: bootInfo.MachineName,
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

	// Mark deploy as started via controller (using MAC)
	if err := h.ctrlClient.MarkBootStarted(ctx, mac); err != nil {
		log.Printf("Warning: failed to mark boot started for %s: %v", mac, err)
	}
}

// RegisterRoutes registers boot-related routes
func (h *BootHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/boot/boot.ipxe", h.ServeBootIPXE)
	mux.HandleFunc("/boot/conditional-boot", h.ServeConditionalBoot)
}
