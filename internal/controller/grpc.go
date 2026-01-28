package controller

import (
	"context"
	"log"
	"strings"

	pb "github.com/isoboot/isoboot/api/controllerpb"
)

// GRPCServer implements the ControllerService gRPC interface
type GRPCServer struct {
	pb.UnimplementedControllerServiceServer
	ctrl *Controller
}

// NewGRPCServer creates a new gRPC server
func NewGRPCServer(ctrl *Controller) *GRPCServer {
	return &GRPCServer{ctrl: ctrl}
}

// GetPendingBoot returns boot info for a MAC with pending provision
func (s *GRPCServer) GetPendingBoot(ctx context.Context, req *pb.GetPendingBootRequest) (*pb.GetPendingBootResponse, error) {
	mac := strings.ToLower(req.Mac)

	provision, err := s.ctrl.k8sClient.FindProvisionByMAC(ctx, mac, "Pending")
	if err != nil {
		log.Printf("gRPC: error finding provision for %s: %v", mac, err)
		return &pb.GetPendingBootResponse{Found: false}, nil
	}

	if provision == nil {
		return &pb.GetPendingBootResponse{Found: false}, nil
	}

	return &pb.GetPendingBootResponse{
		Found:         true,
		MachineName:   provision.Spec.MachineRef,
		ProvisionName: provision.Name,
		Target:        provision.Spec.BootTargetRef,
	}, nil
}

// MarkBootStarted marks a provision as InProgress
func (s *GRPCServer) MarkBootStarted(ctx context.Context, req *pb.MarkBootStartedRequest) (*pb.MarkBootStartedResponse, error) {
	mac := strings.ToLower(req.Mac)

	provision, err := s.ctrl.k8sClient.FindProvisionByMAC(ctx, mac, "Pending")
	if err != nil {
		log.Printf("gRPC: error finding provision for %s: %v", mac, err)
		return &pb.MarkBootStartedResponse{Success: false, Error: err.Error()}, nil
	}

	if provision == nil {
		return &pb.MarkBootStartedResponse{Success: false, Error: "no pending provision"}, nil
	}

	if err := s.ctrl.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "InProgress", "Boot script served", ""); err != nil {
		log.Printf("gRPC: error updating provision %s: %v", provision.Name, err)
		return &pb.MarkBootStartedResponse{Success: false, Error: err.Error()}, nil
	}

	log.Printf("gRPC: marked %s as InProgress", provision.Name)
	return &pb.MarkBootStartedResponse{Success: true}, nil
}

// MarkBootCompleted marks a provision as Complete (by hostname)
func (s *GRPCServer) MarkBootCompleted(ctx context.Context, req *pb.MarkBootCompletedRequest) (*pb.MarkBootCompletedResponse, error) {
	hostname := req.Hostname

	provision, err := s.ctrl.k8sClient.FindProvisionByHostname(ctx, hostname, "InProgress")
	if err != nil {
		log.Printf("gRPC: error finding provision for %s: %v", hostname, err)
		return &pb.MarkBootCompletedResponse{Success: false, Error: err.Error()}, nil
	}

	if provision == nil {
		return &pb.MarkBootCompletedResponse{Success: false, Error: "no in-progress provision"}, nil
	}

	if err := s.ctrl.k8sClient.UpdateProvisionStatus(ctx, provision.Name, "Complete", "Installation completed", req.Ip); err != nil {
		log.Printf("gRPC: error updating provision %s: %v", provision.Name, err)
		return &pb.MarkBootCompletedResponse{Success: false, Error: err.Error()}, nil
	}

	log.Printf("gRPC: marked %s as Complete (IP: %s)", provision.Name, req.Ip)
	return &pb.MarkBootCompletedResponse{Success: true}, nil
}

// GetConfigMapValue retrieves a value from a ConfigMap by key
func (s *GRPCServer) GetConfigMapValue(ctx context.Context, req *pb.GetConfigMapValueRequest) (*pb.GetConfigMapValueResponse, error) {
	cm, err := s.ctrl.k8sClient.GetConfigMap(ctx, req.ConfigmapName)
	if err != nil {
		log.Printf("gRPC: error getting configmap %s: %v", req.ConfigmapName, err)
		return &pb.GetConfigMapValueResponse{Found: false}, nil
	}

	value, ok := cm.Data[req.Key]
	if !ok {
		return &pb.GetConfigMapValueResponse{Found: false}, nil
	}

	return &pb.GetConfigMapValueResponse{
		Found: true,
		Value: value,
	}, nil
}

// GetBootTarget retrieves a BootTarget by name
func (s *GRPCServer) GetBootTarget(ctx context.Context, req *pb.GetBootTargetRequest) (*pb.GetBootTargetResponse, error) {
	bt, err := s.ctrl.k8sClient.GetBootTarget(ctx, req.Name)
	if err != nil {
		log.Printf("gRPC: error getting boottarget %s: %v", req.Name, err)
		return &pb.GetBootTargetResponse{Found: false}, nil
	}

	return &pb.GetBootTargetResponse{
		Found:               true,
		DiskImageRef:        bt.DiskImageRef,
		Template:            bt.Template,
		IncludeFirmwarePath: bt.IncludeFirmwarePath,
	}, nil
}

// GetResponseTemplate retrieves a ResponseTemplate by name
func (s *GRPCServer) GetResponseTemplate(ctx context.Context, req *pb.GetResponseTemplateRequest) (*pb.GetResponseTemplateResponse, error) {
	rt, err := s.ctrl.k8sClient.GetResponseTemplate(ctx, req.Name)
	if err != nil {
		log.Printf("gRPC: error getting responsetemplate %s: %v", req.Name, err)
		return &pb.GetResponseTemplateResponse{Found: false}, nil
	}

	return &pb.GetResponseTemplateResponse{
		Found: true,
		Files: rt.Files,
	}, nil
}

// GetProvision retrieves a Provision by name
func (s *GRPCServer) GetProvision(ctx context.Context, req *pb.GetProvisionRequest) (*pb.GetProvisionResponse, error) {
	provision, err := s.ctrl.k8sClient.GetProvision(ctx, req.Name)
	if err != nil {
		log.Printf("gRPC: error getting provision %s: %v", req.Name, err)
		return &pb.GetProvisionResponse{Found: false}, nil
	}

	return &pb.GetProvisionResponse{
		Found:               true,
		MachineRef:          provision.Spec.MachineRef,
		BootTargetRef:       provision.Spec.BootTargetRef,
		ResponseTemplateRef: provision.Spec.ResponseTemplateRef,
		ConfigMaps:          provision.Spec.ConfigMaps,
		Secrets:             provision.Spec.Secrets,
		MachineId:           provision.Spec.MachineId,
	}, nil
}

// GetConfigMaps retrieves and merges data from multiple ConfigMaps
func (s *GRPCServer) GetConfigMaps(ctx context.Context, req *pb.GetConfigMapsRequest) (*pb.GetConfigMapsResponse, error) {
	data := make(map[string]string)

	for _, name := range req.Names {
		cm, err := s.ctrl.k8sClient.GetConfigMap(ctx, name)
		if err != nil {
			log.Printf("gRPC: error getting configmap %s: %v", name, err)
			return &pb.GetConfigMapsResponse{Found: false, Error: "ConfigMap '" + name + "' not found"}, nil
		}
		for k, v := range cm.Data {
			data[k] = v
		}
	}

	return &pb.GetConfigMapsResponse{
		Found: true,
		Data:  data,
	}, nil
}

// GetSecrets retrieves and merges data from multiple Secrets
func (s *GRPCServer) GetSecrets(ctx context.Context, req *pb.GetSecretsRequest) (*pb.GetSecretsResponse, error) {
	data := make(map[string]string)

	for _, name := range req.Names {
		secret, err := s.ctrl.k8sClient.GetSecret(ctx, name)
		if err != nil {
			log.Printf("gRPC: error getting secret %s: %v", name, err)
			return &pb.GetSecretsResponse{Found: false, Error: "Secret '" + name + "' not found"}, nil
		}
		for k, v := range secret.Data {
			data[k] = string(v)
		}
	}

	return &pb.GetSecretsResponse{
		Found: true,
		Data:  data,
	}, nil
}
