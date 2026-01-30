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
	)

	flag.StringVar(&port, "port", "8081", "gRPC server port")
	flag.StringVar(&namespace, "namespace", "", "Kubernetes namespace")
	flag.StringVar(&isoBasePath, "iso-path", "/opt/isoboot/iso", "Base path for ISO storage")
	flag.Parse()

	if namespace == "" {
		namespace = os.Getenv("POD_NAMESPACE")
		if namespace == "" {
			log.Fatal("--namespace or POD_NAMESPACE is required")
		}
	}

	// Initialize Kubernetes client
	k8sClient, err := k8s.NewClient(namespace)
	if err != nil {
		log.Fatalf("Failed to create Kubernetes client: %v", err)
	}

	// Create and start controller
	// NOTE: SetISOBasePath must be called before Start.
	ctrl := controller.New(k8sClient)
	ctrl.SetISOBasePath(isoBasePath)
	ctrl.Start()
	defer ctrl.Stop()

	// Create gRPC server
	grpcServer := grpc.NewServer()
	pb.RegisterControllerServiceServer(grpcServer, controller.NewGRPCServer(ctrl, nil))

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
