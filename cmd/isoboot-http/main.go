package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
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
		listenPort         string
		proxyPort          string
		controllerAddr     string
		templatesConfigMap string
	)

	flag.StringVar(&host, "host", "", "Host IP to advertise in boot scripts")
	flag.StringVar(&port, "port", "8080", "Advertised HTTP port (used in templates)")
	flag.StringVar(&listenPort, "listen-port", "8082", "Port the Go server binds to (localhost only)")
	flag.StringVar(&proxyPort, "proxy-port", "3128", "Squid proxy port")
	flag.StringVar(&controllerAddr, "controller", "localhost:8081", "Controller gRPC address")
	flag.StringVar(&templatesConfigMap, "templates-configmap", "", "ConfigMap containing boot templates")
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

	// Answer file handlers
	answerHandler := handlers.NewAnswerHandler(host, port, proxyPort, ctrlClient)
	answerHandler.RegisterRoutes(mux)

	// Start server
	addr := fmt.Sprintf("127.0.0.1:%s", listenPort)
	log.Printf("Starting isoboot-http on 127.0.0.1:%s (advertised as %s:%s)", listenPort, host, port)
	log.Printf("Templates ConfigMap: %s", templatesConfigMap)

	var handler http.Handler = mux
	handler = loggingMiddleware(handler)

	if err := http.ListenAndServe(addr, handler); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
