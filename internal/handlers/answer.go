package handlers

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"github.com/isoboot/isoboot/internal/controllerclient"
)

type AnswerHandler struct {
	host       string
	port       string
	proxyPort  string
	ctrlClient *controllerclient.Client
}

func NewAnswerHandler(host, port, proxyPort string, ctrlClient *controllerclient.Client) *AnswerHandler {
	return &AnswerHandler{
		host:       host,
		port:       port,
		proxyPort:  proxyPort,
		ctrlClient: ctrlClient,
	}
}

// ServeAnswer serves response template files (preseed/kickstart/autoinstall)
// Path format: /answer/{provisionName}/{filename}
func (h *AnswerHandler) ServeAnswer(w http.ResponseWriter, r *http.Request) {
	// Parse path: /answer/{provisionName}/{filename}
	path := strings.TrimPrefix(r.URL.Path, "/answer/")
	parts := strings.SplitN(path, "/", 2)
	if len(parts) < 2 {
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	provisionName := parts[0]
	filename := parts[1]
	ctx := r.Context()

	// 1. Get Provision
	provision, err := h.ctrlClient.GetProvision(ctx, provisionName)
	if err != nil {
		log.Printf("Error getting provision %s: %v", provisionName, err)
		if strings.Contains(err.Error(), "grpc call:") {
			w.WriteHeader(http.StatusBadGateway)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}

	// 2. Get ResponseTemplate
	if provision.ResponseTemplateRef == "" {
		log.Printf("Provision %s has no responseTemplateRef", provisionName)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	responseTemplate, err := h.ctrlClient.GetResponseTemplate(ctx, provision.ResponseTemplateRef)
	if err != nil {
		log.Printf("Error getting response template %s: %v", provision.ResponseTemplateRef, err)
		if strings.Contains(err.Error(), "grpc call:") {
			w.WriteHeader(http.StatusBadGateway)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
		return
	}

	// Get the template content for the requested filename
	templateContent, ok := responseTemplate.Files[filename]
	if !ok {
		log.Printf("File %s not found in response template %s", filename, provision.ResponseTemplateRef)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	// 3. Get ConfigMaps (if any)
	configMapData, err := h.ctrlClient.GetConfigMaps(ctx, provision.ConfigMaps)
	if err != nil {
		log.Printf("Error getting configmaps for %s: %v", provisionName, err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// 4. Get Secrets (if any)
	secretData, err := h.ctrlClient.GetSecrets(ctx, provision.Secrets)
	if err != nil {
		log.Printf("Error getting secrets for %s: %v", provisionName, err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}

	// 5. Build template data
	data := make(map[string]interface{})

	// Merge ConfigMap data
	for k, v := range configMapData {
		data[k] = v
	}

	// Merge Secret data last so that Secret values intentionally overwrite
	// ConfigMap values for the same key; this defines the data precedence.
	for k, v := range secretData {
		data[k] = v
	}

	// Derive SSH public keys from private keys in secrets
	if err := deriveSSHPublicKeys(data); err != nil {
		log.Printf("Error deriving SSH public keys for %s: %v", provisionName, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	// Add system variables
	data["Host"] = h.host
	data["Port"] = h.port
	data["Hostname"] = provision.MachineRef
	data["Target"] = provision.BootTargetRef

	// Add MachineId if set
	if provision.MachineId != "" {
		data["MachineId"] = provision.MachineId
	}

	// Add MAC address from Machine
	mac, err := h.ctrlClient.GetMachine(ctx, provision.MachineRef)
	if err != nil {
		log.Printf("Error getting machine %s: %v", provision.MachineRef, err)
		w.WriteHeader(http.StatusBadGateway)
		return
	}
	if mac != "" {
		data["MAC"] = mac
	}

	// 6. Render template
	content, err := renderAnswerTemplate(templateContent, data)
	if err != nil {
		log.Printf("Error rendering template for %s/%s: %v", provisionName, filename, err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(content)))
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(content))
}

// RegisterRoutes registers answer file routes
func (h *AnswerHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/answer/", h.ServeAnswer)
}
