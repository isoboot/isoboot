package controllerclient

import (
	"context"
	"errors"
	"fmt"

	pb "github.com/isoboot/isoboot/api/controllerpb"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// ErrNotFound is returned when a requested resource does not exist
var ErrNotFound = errors.New("not found")

// BootInfo returned by controller
type BootInfo struct {
	MachineName   string
	ProvisionName string
	Target        string
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

// GetPendingBoot returns boot info for a MAC with pending provision, or nil if none
func (c *Client) GetPendingBoot(ctx context.Context, mac string) (*BootInfo, error) {
	resp, err := c.client.GetPendingBoot(ctx, &pb.GetPendingBootRequest{Mac: mac})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, nil
	}

	return &BootInfo{
		MachineName:   resp.MachineName,
		ProvisionName: resp.ProvisionName,
		Target:        resp.Target,
	}, nil
}

// MarkBootStarted marks a provision as InProgress
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

// MarkBootCompleted marks a provision as Complete (by hostname)
func (c *Client) MarkBootCompleted(ctx context.Context, hostname, ip string) error {
	resp, err := c.client.MarkBootCompleted(ctx, &pb.MarkBootCompletedRequest{Hostname: hostname, Ip: ip})
	if err != nil {
		return fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Success {
		return fmt.Errorf("controller error: %s", resp.Error)
	}

	return nil
}

// GetConfigMapValue retrieves a value from a ConfigMap by key
func (c *Client) GetConfigMapValue(ctx context.Context, configMapName, key string) (string, error) {
	resp, err := c.client.GetConfigMapValue(ctx, &pb.GetConfigMapValueRequest{
		ConfigmapName: configMapName,
		Key:           key,
	})
	if err != nil {
		return "", fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return "", fmt.Errorf("configmap key not found: %s/%s", configMapName, key)
	}

	return resp.Value, nil
}

// BootTargetInfo returned by GetBootTarget
type BootTargetInfo struct {
	DiskImageRef        string
	IncludeFirmwarePath string
	Template            string
}

// GetBootTarget retrieves a BootTarget by name
func (c *Client) GetBootTarget(ctx context.Context, name string) (*BootTargetInfo, error) {
	resp, err := c.client.GetBootTarget(ctx, &pb.GetBootTargetRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, fmt.Errorf("boottarget %s: %w", name, ErrNotFound)
	}

	return &BootTargetInfo{
		DiskImageRef:        resp.DiskImageRef,
		IncludeFirmwarePath: resp.IncludeFirmwarePath,
		Template:            resp.Template,
	}, nil
}

// ProvisionInfo returned by GetProvision
type ProvisionInfo struct {
	MachineRef          string
	BootTargetRef       string
	ResponseTemplateRef string
	ConfigMaps          []string
	Secrets             []string
	MachineId           string
}

// GetProvision retrieves a Provision by name
func (c *Client) GetProvision(ctx context.Context, name string) (*ProvisionInfo, error) {
	resp, err := c.client.GetProvision(ctx, &pb.GetProvisionRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, fmt.Errorf("provision %s: %w", name, ErrNotFound)
	}

	return &ProvisionInfo{
		MachineRef:          resp.MachineRef,
		BootTargetRef:       resp.BootTargetRef,
		ResponseTemplateRef: resp.ResponseTemplateRef,
		ConfigMaps:          resp.ConfigMaps,
		Secrets:             resp.Secrets,
		MachineId:           resp.MachineId,
	}, nil
}

// GetConfigMaps retrieves and merges data from multiple ConfigMaps
func (c *Client) GetConfigMaps(ctx context.Context, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return make(map[string]string), nil
	}

	resp, err := c.client.GetConfigMaps(ctx, &pb.GetConfigMapsRequest{Names: names})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, fmt.Errorf("configmaps: %s", resp.Error)
	}

	return resp.Data, nil
}

// GetSecrets retrieves and merges data from multiple Secrets
func (c *Client) GetSecrets(ctx context.Context, names []string) (map[string]string, error) {
	if len(names) == 0 {
		return make(map[string]string), nil
	}

	resp, err := c.client.GetSecrets(ctx, &pb.GetSecretsRequest{Names: names})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, fmt.Errorf("secrets: %s", resp.Error)
	}

	return resp.Data, nil
}

// ResponseTemplateInfo returned by GetResponseTemplate
type ResponseTemplateInfo struct {
	Files map[string]string
}

// GetResponseTemplate retrieves a ResponseTemplate by name
func (c *Client) GetResponseTemplate(ctx context.Context, name string) (*ResponseTemplateInfo, error) {
	resp, err := c.client.GetResponseTemplate(ctx, &pb.GetResponseTemplateRequest{Name: name})
	if err != nil {
		return nil, fmt.Errorf("grpc call: %w", err)
	}

	if !resp.Found {
		return nil, fmt.Errorf("responsetemplate %s: %w", name, ErrNotFound)
	}

	return &ResponseTemplateInfo{
		Files: resp.Files,
	}, nil
}
