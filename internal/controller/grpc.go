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

// GetPendingBoot returns boot info for a MAC with pending deploy
func (s *GRPCServer) GetPendingBoot(ctx context.Context, req *pb.GetPendingBootRequest) (*pb.GetPendingBootResponse, error) {
	mac := strings.ToLower(req.Mac)

	deploy, err := s.ctrl.k8sClient.FindDeployByMAC(ctx, mac, "Pending")
	if err != nil {
		log.Printf("gRPC: error finding deploy for %s: %v", mac, err)
		return &pb.GetPendingBootResponse{Found: false}, nil
	}

	if deploy == nil {
		return &pb.GetPendingBootResponse{Found: false}, nil
	}

	return &pb.GetPendingBootResponse{
		Found:       true,
		MachineName: deploy.Spec.MachineRef,
		DeployName:  deploy.Name,
		Target:      deploy.Spec.Target,
	}, nil
}

// MarkBootStarted marks a deploy as InProgress
func (s *GRPCServer) MarkBootStarted(ctx context.Context, req *pb.MarkBootStartedRequest) (*pb.MarkBootStartedResponse, error) {
	mac := strings.ToLower(req.Mac)

	deploy, err := s.ctrl.k8sClient.FindDeployByMAC(ctx, mac, "Pending")
	if err != nil {
		log.Printf("gRPC: error finding deploy for %s: %v", mac, err)
		return &pb.MarkBootStartedResponse{Success: false, Error: err.Error()}, nil
	}

	if deploy == nil {
		return &pb.MarkBootStartedResponse{Success: false, Error: "no pending deploy"}, nil
	}

	if err := s.ctrl.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "InProgress", "Boot script served"); err != nil {
		log.Printf("gRPC: error updating deploy %s: %v", deploy.Name, err)
		return &pb.MarkBootStartedResponse{Success: false, Error: err.Error()}, nil
	}

	log.Printf("gRPC: marked %s as InProgress", deploy.Name)
	return &pb.MarkBootStartedResponse{Success: true}, nil
}

// MarkBootCompleted marks a deploy as Completed
func (s *GRPCServer) MarkBootCompleted(ctx context.Context, req *pb.MarkBootCompletedRequest) (*pb.MarkBootCompletedResponse, error) {
	mac := strings.ToLower(req.Mac)

	deploy, err := s.ctrl.k8sClient.FindDeployByMAC(ctx, mac, "InProgress")
	if err != nil {
		log.Printf("gRPC: error finding deploy for %s: %v", mac, err)
		return &pb.MarkBootCompletedResponse{Success: false, Error: err.Error()}, nil
	}

	if deploy == nil {
		return &pb.MarkBootCompletedResponse{Success: false, Error: "no in-progress deploy"}, nil
	}

	if err := s.ctrl.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "Completed", "Installation completed"); err != nil {
		log.Printf("gRPC: error updating deploy %s: %v", deploy.Name, err)
		return &pb.MarkBootCompletedResponse{Success: false, Error: err.Error()}, nil
	}

	log.Printf("gRPC: marked %s as Completed", deploy.Name)
	return &pb.MarkBootCompletedResponse{Success: true}, nil
}

// GetTemplate retrieves a boot template from ConfigMap
func (s *GRPCServer) GetTemplate(ctx context.Context, req *pb.GetTemplateRequest) (*pb.GetTemplateResponse, error) {
	cm, err := s.ctrl.k8sClient.GetConfigMap(ctx, req.Configmap)
	if err != nil {
		log.Printf("gRPC: error getting configmap %s: %v", req.Configmap, err)
		return &pb.GetTemplateResponse{Found: false}, nil
	}

	content, ok := cm.Data[req.Name]
	if !ok {
		return &pb.GetTemplateResponse{Found: false}, nil
	}

	return &pb.GetTemplateResponse{
		Found:   true,
		Content: content,
	}, nil
}
