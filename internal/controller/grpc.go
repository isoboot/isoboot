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

// MarkBootCompleted marks a deploy as Complete (by hostname)
func (s *GRPCServer) MarkBootCompleted(ctx context.Context, req *pb.MarkBootCompletedRequest) (*pb.MarkBootCompletedResponse, error) {
	hostname := req.Hostname

	deploy, err := s.ctrl.k8sClient.FindDeployByHostname(ctx, hostname, "InProgress")
	if err != nil {
		log.Printf("gRPC: error finding deploy for %s: %v", hostname, err)
		return &pb.MarkBootCompletedResponse{Success: false, Error: err.Error()}, nil
	}

	if deploy == nil {
		return &pb.MarkBootCompletedResponse{Success: false, Error: "no in-progress deploy"}, nil
	}

	if err := s.ctrl.k8sClient.UpdateDeployStatus(ctx, deploy.Name, "Complete", "Installation completed"); err != nil {
		log.Printf("gRPC: error updating deploy %s: %v", deploy.Name, err)
		return &pb.MarkBootCompletedResponse{Success: false, Error: err.Error()}, nil
	}

	log.Printf("gRPC: marked %s as Complete", deploy.Name)
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

// GetBootTarget retrieves a BootTarget by name
func (s *GRPCServer) GetBootTarget(ctx context.Context, req *pb.GetBootTargetRequest) (*pb.GetBootTargetResponse, error) {
	bt, err := s.ctrl.k8sClient.GetBootTarget(ctx, req.Name)
	if err != nil {
		log.Printf("gRPC: error getting boottarget %s: %v", req.Name, err)
		return &pb.GetBootTargetResponse{Found: false}, nil
	}

	return &pb.GetBootTargetResponse{
		Found:        true,
		DiskImageRef: bt.DiskImageRef,
		Template:     bt.Template,
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

// GetRenderedTemplate renders a template file for a deploy
func (s *GRPCServer) GetRenderedTemplate(ctx context.Context, req *pb.GetRenderedTemplateRequest) (*pb.GetRenderedTemplateResponse, error) {
	// Find the deploy by hostname (InProgress or Pending - race between boot start and answer retrieval)
	deploy, err := s.ctrl.k8sClient.FindDeployByHostname(ctx, req.Hostname, "InProgress")
	if err != nil {
		log.Printf("gRPC: error finding deploy for %s: %v", req.Hostname, err)
		return &pb.GetRenderedTemplateResponse{Found: false, Error: err.Error()}, nil
	}
	// If not InProgress, try Pending (installer may request before MarkBootStarted completes)
	if deploy == nil {
		deploy, err = s.ctrl.k8sClient.FindDeployByHostname(ctx, req.Hostname, "Pending")
		if err != nil {
			log.Printf("gRPC: error finding pending deploy for %s: %v", req.Hostname, err)
			return &pb.GetRenderedTemplateResponse{Found: false, Error: err.Error()}, nil
		}
	}
	if deploy == nil {
		return &pb.GetRenderedTemplateResponse{Found: false, Error: "no active deploy for hostname"}, nil
	}

	// Get the response template
	rt, err := s.ctrl.k8sClient.GetResponseTemplate(ctx, deploy.Spec.ResponseTemplateRef)
	if err != nil {
		log.Printf("gRPC: error getting responsetemplate %s: %v", deploy.Spec.ResponseTemplateRef, err)
		return &pb.GetRenderedTemplateResponse{Found: false, Error: "response template not found"}, nil
	}

	// Get the template content
	templateContent, ok := rt.Files[req.Filename]
	if !ok {
		return &pb.GetRenderedTemplateResponse{Found: false, Error: "file not found in template"}, nil
	}

	// Render the template
	rendered, err := s.ctrl.RenderTemplate(ctx, deploy, templateContent)
	if err != nil {
		log.Printf("gRPC: error rendering template for %s: %v", req.Hostname, err)
		return &pb.GetRenderedTemplateResponse{Found: false, Error: err.Error()}, nil
	}

	return &pb.GetRenderedTemplateResponse{
		Found:   true,
		Content: rendered,
	}, nil
}
