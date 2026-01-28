package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/isoboot/isoboot/internal/controllerclient"
	"github.com/isoboot/isoboot/internal/handlers"
)

// responseWriter wraps http.ResponseWriter to capture status code
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

// pathTraversalMiddleware rejects requests with path traversal attempts.
// Defense-in-depth: handlers must still perform their own containment validation.
func pathTraversalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Normalize path: handle backslashes
		normalizedPath := strings.ReplaceAll(r.URL.Path, "\\", "/")

		// Reject if any path segment is ".."
		for _, segment := range strings.Split(normalizedPath, "/") {
			if segment == ".." {
				log.Printf("blocked path traversal: path=%q remote_addr=%s", r.URL.Path, r.RemoteAddr)
				http.Error(w, "invalid path", http.StatusBadRequest)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs requests with status code
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rw.status, time.Since(start))
	})
}

func main() {
	var (
		host               string
		port               string
		proxyPort          string
		controllerAddr     string
		templatesConfigMap string
		isoPath            string
	)

	flag.StringVar(&host, "host", "", "Host IP to advertise in boot scripts")
	flag.StringVar(&port, "port", "8080", "HTTP server port")
	flag.StringVar(&proxyPort, "proxy-port", "3128", "Squid proxy port")
	flag.StringVar(&controllerAddr, "controller", "localhost:8081", "Controller gRPC address")
	flag.StringVar(&templatesConfigMap, "templates-configmap", "", "ConfigMap containing boot templates")
	flag.StringVar(&isoPath, "iso-path", "/opt/isoboot/iso", "Path to ISO storage directory")
	flag.Parse()

	if host == "" {
		log.Fatal("--host is required")
	}
	if templatesConfigMap == "" {
		log.Fatal("--templates-configmap is required")
	}

	// Connect to controller via gRPC
	log.Printf("Connecting to controller at %s...", controllerAddr)
	ctrlClient, err := controllerclient.New(controllerAddr)
	if err != nil {
		log.Fatalf("Failed to connect to controller: %v", err)
	}
	defer ctrlClient.Close()
	log.Printf("Connected to controller")

	// Set up HTTP handlers
	mux := http.NewServeMux()

	// Health check
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "2")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	// Boot handlers
	bootHandler := handlers.NewBootHandler(host, port, proxyPort, ctrlClient, templatesConfigMap)
	bootHandler.RegisterRoutes(mux)

	// ISO content handlers
	isoHandler := handlers.NewISOHandler(isoPath, ctrlClient)
	isoHandler.RegisterRoutes(mux)

	// Answer file handlers
	answerHandler := handlers.NewAnswerHandler(host, port, proxyPort, ctrlClient)
	answerHandler.RegisterRoutes(mux)

	// Start server
	addr := fmt.Sprintf(":%s", port)
	log.Printf("Starting isoboot-http on %s:%s", host, port)
	log.Printf("ISO path: %s", isoPath)
	log.Printf("Templates ConfigMap: %s", templatesConfigMap)

	var handler http.Handler = mux
	handler = pathTraversalMiddleware(handler)
	handler = loggingMiddleware(handler)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
