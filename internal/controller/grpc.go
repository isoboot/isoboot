package controller

import (
	"context"
	"log"
	"path"
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

// GetMachineByMAC retrieves a Machine by MAC address
func (s *GRPCServer) GetMachineByMAC(ctx context.Context, req *pb.GetMachineByMACRequest) (*pb.GetMachineByMACResponse, error) {
	mac := strings.ToLower(req.Mac)

	machine, err := s.ctrl.k8sClient.FindMachineByMAC(ctx, mac)
	if err != nil {
		log.Printf("gRPC: error finding machine for MAC %s: %v", mac, err)
		return &pb.GetMachineByMACResponse{Found: false}, nil
	}

	if machine == nil {
		return &pb.GetMachineByMACResponse{Found: false}, nil
	}

	return &pb.GetMachineByMACResponse{
		Found: true,
		Name:  machine.Name,
	}, nil
}

// GetMachine retrieves a Machine by name
func (s *GRPCServer) GetMachine(ctx context.Context, req *pb.GetMachineRequest) (*pb.GetMachineResponse, error) {
	machine, err := s.ctrl.k8sClient.GetMachine(ctx, req.Name)
	if err != nil {
		log.Printf("gRPC: error getting machine %s: %v", req.Name, err)
		return &pb.GetMachineResponse{Found: false}, nil
	}

	return &pb.GetMachineResponse{
		Found: true,
		Mac:   machine.MAC,
	}, nil
}

// GetProvisionsByMachine retrieves all Provisions referencing a Machine
func (s *GRPCServer) GetProvisionsByMachine(ctx context.Context, req *pb.GetProvisionsByMachineRequest) (*pb.GetProvisionsByMachineResponse, error) {
	provisions, err := s.ctrl.k8sClient.ListProvisionsByMachine(ctx, req.MachineName)
	if err != nil {
		log.Printf("gRPC: error listing provisions for machine %s: %v", req.MachineName, err)
		return &pb.GetProvisionsByMachineResponse{}, nil
	}

	var summaries []*pb.ProvisionSummary
	for _, p := range provisions {
		summaries = append(summaries, &pb.ProvisionSummary{
			Name:          p.Name,
			Status:        p.Status.Phase,
			BootTargetRef: p.Spec.BootTargetRef,
		})
	}

	return &pb.GetProvisionsByMachineResponse{
		Provisions: summaries,
	}, nil
}

// UpdateProvisionStatus updates a Provision's status
func (s *GRPCServer) UpdateProvisionStatus(ctx context.Context, req *pb.UpdateProvisionStatusRequest) (*pb.UpdateProvisionStatusResponse, error) {
	if err := s.ctrl.k8sClient.UpdateProvisionStatus(ctx, req.Name, req.Status, req.Message, req.Ip); err != nil {
		log.Printf("gRPC: error updating provision %s status: %v", req.Name, err)
		return &pb.UpdateProvisionStatusResponse{Success: false, Error: err.Error()}, nil
	}

	log.Printf("gRPC: updated %s to %s", req.Name, req.Status)
	return &pb.UpdateProvisionStatusResponse{Success: true}, nil
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
		DiskImage:           bt.DiskImageRef,
		Template:            bt.Template,
		IncludeFirmwarePath: bt.IncludeFirmwarePath,
	}, nil
}

// GetDiskImage retrieves a DiskImage by name
func (s *GRPCServer) GetDiskImage(ctx context.Context, req *pb.GetDiskImageRequest) (*pb.GetDiskImageResponse, error) {
	di, err := s.ctrl.k8sClient.GetDiskImage(ctx, req.Name)
	if err != nil {
		log.Printf("gRPC: error getting disk image %s: %v", req.Name, err)
		return &pb.GetDiskImageResponse{Found: false}, nil
	}

	diskImageFile := ""
	if di.ISO != "" {
		diskImageFile = path.Base(di.ISO)
	}

	return &pb.GetDiskImageResponse{
		Found:         true,
		DiskImageFile: diskImageFile,
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
