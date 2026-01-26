package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"os"

	pb "github.com/isoboot/isoboot/api/controllerpb"
	"github.com/isoboot/isoboot/internal/controller"
	"github.com/isoboot/isoboot/internal/k8s"
	"google.golang.org/grpc"
	"google.golang.org/grpc/reflection"
)

func main() {
	var (
		port        string
		namespace   string
		isoBasePath string
		httpHost    string
		httpPort    string
	)

	flag.StringVar(&port, "port", "8081", "gRPC server port")
	flag.StringVar(&namespace, "namespace", "", "Kubernetes namespace")
	flag.StringVar(&isoBasePath, "iso-path", "/opt/isoboot/iso", "Base path for ISO storage")
	flag.StringVar(&httpHost, "http-host", "", "HTTP server host for template rendering")
	flag.StringVar(&httpPort, "http-port", "8080", "HTTP server port for template rendering")
	flag.Parse()

	// Track which flags were explicitly set
	flagsSet := make(map[string]bool)
	flag.Visit(func(f *flag.Flag) {
		flagsSet[f.Name] = true
	})

	if namespace == "" {
		namespace = os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			log.Fatal("--namespace or POD_NAMESPACE is required")
		}
	}

	// Only use env vars as fallback when flags weren't explicitly set
	if !flagsSet["http-host"] {
		httpHost = os.Getenv("HTTP_HOST")
	}
	if !flagsSet["http-port"] {
		if envPort := os.Getenv("HTTP_PORT"); envPort != "" {
			httpPort = envPort
		}
	}

	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClient(namespace)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create and start controller
	// NOTE: SetISOBasePath and SetHostPort must be called before Start;
	// the controller may use these values immediately after starting.
	ctrl := controller.New(k8sClient)
	ctrl.SetISOBasePath(isoBasePath)
	ctrl.SetHostPort(httpHost, httpPort)
	ctrl.Start()
	defer ctrl.Stop()

	// Create gRPC server
	grpcServer := grpc.NewServer()
	pb.RegisterControllerServiceServer(grpcServer, controller.NewGRPCServer(ctrl))

	// Enable reflection for debugging (grpcurl)
	reflection.Register(grpcServer)

	// Start listening
	addr := fmt.Sprintf(":%s", port)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to listen: %v", err)
	}

	log.Printf("Starting isoboot-controller gRPC on %s", addr)
	log.Printf("Namespace: %s", namespace)

	if err := grpcServer.Serve(lis); err != nil {
		log.Fatalf("Server failed: %v", err)
	}
}
