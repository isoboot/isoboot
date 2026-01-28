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

// GetRenderedTemplate renders a template file for a provision
func (s *GRPCServer) GetRenderedTemplate(ctx context.Context, req *pb.GetRenderedTemplateRequest) (*pb.GetRenderedTemplateResponse, error) {
	// Direct lookup by provision name (O(1) instead of O(n) hostname search)
	provision, err := s.ctrl.k8sClient.GetProvision(ctx, req.ProvisionName)
	if err != nil {
		log.Printf("gRPC: error getting provision %s: %v", req.ProvisionName, err)
		return &pb.GetRenderedTemplateResponse{Found: false, Error: "provision not found"}, nil
	}

	// Get the response template
	rt, err := s.ctrl.k8sClient.GetResponseTemplate(ctx, provision.Spec.ResponseTemplateRef)
	if err != nil {
		log.Printf("gRPC: error getting responsetemplate %s: %v", provision.Spec.ResponseTemplateRef, err)
		return &pb.GetRenderedTemplateResponse{Found: false, Error: "response template not found"}, nil
	}

	// Get the template content
	templateContent, ok := rt.Files[req.Filename]
	if !ok {
		return &pb.GetRenderedTemplateResponse{Found: false, Error: "file not found in template"}, nil
	}

	// Render the template
	rendered, err := s.ctrl.RenderTemplate(ctx, provision, templateContent)
	if err != nil {
		log.Printf("gRPC: error rendering template for %s: %v", req.ProvisionName, err)
		return &pb.GetRenderedTemplateResponse{Found: false, Error: err.Error()}, nil
	}

	return &pb.GetRenderedTemplateResponse{
		Found:   true,
		Content: rendered,
	}, nil
}
