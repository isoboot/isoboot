package controllerclient

import (
	"context"
	"fmt"

	pb "github.com/isoboot/isoboot/api/controllerpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// BootInfo returned by controller
type BootInfo struct {
	MachineName string
	DeployName  string
	Target      string
}

// Client communicates with the isoboot-controller via gRPC
type Client struct {
	conn   *grpc.ClientConn
	client pb.ControllerServiceClient
}

// New creates a new controller client.
// Connection is established lazily on first RPC call, allowing the HTTP server
// to start before the controller is ready.
func New(controllerAddr string) (*Client, error) {
	conn, err := grpc.NewClient(controllerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return nil, fmt.Errorf("create grpc client: %w", err)
	}

	return &Client{
		conn:   conn,
		client: pb.NewControllerServiceClient(conn),
	}, nil
}

// Close closes the connection
func (c *Client) Close() error {
	return c.conn.Close()
}

// GetPendingBoot returns boot info for a MAC with pending deploy, or nil if none
func (c *Client) GetPendingBoot(ctx context.Context, mac string) (*BootInfo, error) {
	resp, err := c.client.GetPendingBoot(ctx, &pb.GetPendingBootRequest{Mac: mac})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, nil
	}

	return &BootInfo{
		MachineName: resp.MachineName,
		DeployName:  resp.DeployName,
		Target:      resp.Target,
	}, nil
}

// MarkBootStarted marks a deploy as InProgress
func (c *Client) MarkBootStarted(ctx context.Context, mac string) error {
	resp, err := c.client.MarkBootStarted(ctx, &pb.MarkBootStartedRequest{Mac: mac})
	if err != nil {
		return fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("controller error: %s", resp.Error)
	}

	return nil
}

// MarkBootCompleted marks a deploy as Complete (by hostname)
func (c *Client) MarkBootCompleted(ctx context.Context, hostname string) error {
	resp, err := c.client.MarkBootCompleted(ctx, &pb.MarkBootCompletedRequest{Hostname: hostname})
	if err != nil {
		return fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("controller error: %s", resp.Error)
	}

	return nil
}

// GetTemplate retrieves a template from the controller (ConfigMap)
func (c *Client) GetTemplate(ctx context.Context, name, configMap string) (string, error) {
	resp, err := c.client.GetTemplate(ctx, &pb.GetTemplateRequest{
		Name:      name,
		Configmap: configMap,
	})
	if err != nil {
		return "", fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return "", fmt.Errorf("template not found: %s", name)
	}

	return resp.Content, nil
}

// BootTargetInfo returned by GetBootTarget
type BootTargetInfo struct {
	DiskImageRef string
	Template     string
}

// GetBootTarget retrieves a BootTarget by name
func (c *Client) GetBootTarget(ctx context.Context, name string) (*BootTargetInfo, error) {
	resp, err := c.client.GetBootTarget(ctx, &pb.GetBootTargetRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, fmt.Errorf("boottarget not found: %s", name)
	}

	return &BootTargetInfo{
		DiskImageRef: resp.DiskImageRef,
		Template:     resp.Template,
	}, nil
}

// GetRenderedTemplate retrieves a rendered template file for a deploy
func (c *Client) GetRenderedTemplate(ctx context.Context, hostname, filename string) (string, error) {
	resp, err := c.client.GetRenderedTemplate(ctx, &pb.GetRenderedTemplateRequest{
		Hostname: hostname,
		Filename: filename,
	})
	if err != nil {
		return "", fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return "", fmt.Errorf("template not found: %s", resp.Error)
	}

	return resp.Content, nil
}
