package controllerclient

import (
	"context"
	"fmt"
	"time"

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

// New creates a new controller client
func New(controllerAddr string) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := grpc.DialContext(ctx, controllerAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to controller: %w", err)
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

// MarkBootCompleted marks a deploy as Completed
func (c *Client) MarkBootCompleted(ctx context.Context, mac string) error {
	resp, err := c.client.MarkBootCompleted(ctx, &pb.MarkBootCompletedRequest{Mac: mac})
	if err != nil {
		return fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("controller error: %s", resp.Error)
	}

	return nil
}

// GetTemplate retrieves a template from the controller
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
