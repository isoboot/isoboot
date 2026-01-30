package controller

import (
	"context"
	"testing"

	pb "github.com/isoboot/isoboot/api/controllerpb"
	"github.com/isoboot/isoboot/internal/k8s/typed"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestGRPC_GetMachineByMAC_Found(t *testing.T) {
	k := newTestTypedClient(
		&typed.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       typed.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetMachineByMAC(context.Background(), &pb.GetMachineByMACRequest{Mac: "AA-BB-CC-DD-EE-FF"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.Name != "vm-01" {
		t.Errorf("expected name vm-01, got %q", resp.Name)
	}
}

func TestGRPC_GetMachineByMAC_NotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetMachineByMAC(context.Background(), &pb.GetMachineByMACRequest{Mac: "aa-bb-cc-dd-ee-ff"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false for unknown MAC")
	}
}

func TestGRPC_GetMachine_Found(t *testing.T) {
	k := newTestTypedClient(
		&typed.Machine{
			ObjectMeta: metav1.ObjectMeta{Name: "vm-01", Namespace: "default"},
			Spec:       typed.MachineSpec{MAC: "aa-bb-cc-dd-ee-ff"},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetMachine(context.Background(), &pb.GetMachineRequest{Name: "vm-01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.Mac != "aa-bb-cc-dd-ee-ff" {
		t.Errorf("expected MAC aa-bb-cc-dd-ee-ff, got %q", resp.Mac)
	}
}

func TestGRPC_GetMachine_NotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetMachine(context.Background(), &pb.GetMachineRequest{Name: "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false for unknown machine")
	}
}

func TestGRPC_GetProvisionsByMachine(t *testing.T) {
	k := newTestTypedClient(
		&typed.Provision{
			ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
			Spec:       typed.ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-13"},
			Status:     typed.ProvisionStatus{Phase: "Pending"},
		},
		&typed.Provision{
			ObjectMeta: metav1.ObjectMeta{Name: "prov-2", Namespace: "default"},
			Spec:       typed.ProvisionSpec{MachineRef: "vm-02", BootTargetRef: "debian-13"},
			Status:     typed.ProvisionStatus{Phase: "InProgress"},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetProvisionsByMachine(context.Background(), &pb.GetProvisionsByMachineRequest{MachineName: "vm-01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Provisions) != 1 {
		t.Fatalf("expected 1 provision, got %d", len(resp.Provisions))
	}
	if resp.Provisions[0].Name != "prov-1" {
		t.Errorf("expected prov-1, got %q", resp.Provisions[0].Name)
	}
	if resp.Provisions[0].Status != "Pending" {
		t.Errorf("expected status Pending, got %q", resp.Provisions[0].Status)
	}
}

func TestGRPC_GetProvisionsByMachine_Empty(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetProvisionsByMachine(context.Background(), &pb.GetProvisionsByMachineRequest{MachineName: "vm-01"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(resp.Provisions) != 0 {
		t.Errorf("expected 0 provisions, got %d", len(resp.Provisions))
	}
}

func TestGRPC_UpdateProvisionStatus(t *testing.T) {
	k := newTestTypedClient(
		&typed.Provision{
			ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
			Spec:       typed.ProvisionSpec{MachineRef: "vm-01", BootTargetRef: "debian-13"},
			Status:     typed.ProvisionStatus{Phase: "Pending"},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.UpdateProvisionStatus(context.Background(), &pb.UpdateProvisionStatusRequest{
		Name:    "prov-1",
		Status:  "InProgress",
		Message: "Boot started",
		Ip:      "10.0.0.5",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Success {
		t.Errorf("expected Success=true, got error: %s", resp.Error)
	}

	var updated typed.Provision
	if err := k.Get(context.Background(), k.Key("prov-1"), &updated); err != nil {
		t.Fatalf("failed to get provision: %v", err)
	}
	if updated.Status.Phase != "InProgress" {
		t.Errorf("expected phase InProgress, got %q", updated.Status.Phase)
	}
	if updated.Status.IP != "10.0.0.5" {
		t.Errorf("expected IP 10.0.0.5, got %q", updated.Status.IP)
	}
}

func TestGRPC_UpdateProvisionStatus_NotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.UpdateProvisionStatus(context.Background(), &pb.UpdateProvisionStatusRequest{
		Name:   "missing",
		Status: "InProgress",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Success {
		t.Error("expected Success=false for missing provision")
	}
}

func TestGRPC_GetConfigMapValue_Found(t *testing.T) {
	k := newTestTypedClient(
		newConfigMap("isoboot-templates", map[string]string{
			"boot.ipxe": "#!ipxe\nchain ...\n",
		}),
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetConfigMapValue(context.Background(), &pb.GetConfigMapValueRequest{
		ConfigmapName: "isoboot-templates",
		Key:           "boot.ipxe",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.Value != "#!ipxe\nchain ...\n" {
		t.Errorf("unexpected value: %q", resp.Value)
	}
}

func TestGRPC_GetConfigMapValue_KeyNotFound(t *testing.T) {
	k := newTestTypedClient(
		newConfigMap("cm", map[string]string{"a": "b"}),
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetConfigMapValue(context.Background(), &pb.GetConfigMapValueRequest{
		ConfigmapName: "cm",
		Key:           "missing-key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false for missing key")
	}
}

func TestGRPC_GetConfigMapValue_ConfigMapNotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetConfigMapValue(context.Background(), &pb.GetConfigMapValueRequest{
		ConfigmapName: "missing",
		Key:           "key",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false for missing configmap")
	}
}

func TestGRPC_GetBootTarget_Found(t *testing.T) {
	k := newTestTypedClient(
		&typed.BootTarget{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec:       typed.BootTargetSpec{BootMediaRef: "debian-media", UseFirmware: true, Template: "#!ipxe\nkernel ...\n"},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetBootTarget(context.Background(), &pb.GetBootTargetRequest{Name: "debian-13"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.Template != "#!ipxe\nkernel ...\n" {
		t.Errorf("unexpected template: %q", resp.Template)
	}
	if resp.BootMediaRef != "debian-media" {
		t.Errorf("expected BootMediaRef debian-media, got %q", resp.BootMediaRef)
	}
	if !resp.UseFirmware {
		t.Error("expected UseFirmware=true")
	}
}

func TestGRPC_GetBootTarget_NotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetBootTarget(context.Background(), &pb.GetBootTargetRequest{Name: "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false")
	}
}

func TestGRPC_GetResponseTemplate_Found(t *testing.T) {
	k := newTestTypedClient(
		&typed.ResponseTemplate{
			ObjectMeta: metav1.ObjectMeta{Name: "preseed-tmpl", Namespace: "default"},
			Spec: typed.ResponseTemplateSpec{
				Files: map[string]string{
					"preseed.cfg": "d-i netcfg/hostname string {{ .Hostname }}",
				},
			},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetResponseTemplate(context.Background(), &pb.GetResponseTemplateRequest{Name: "preseed-tmpl"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.Files["preseed.cfg"] != "d-i netcfg/hostname string {{ .Hostname }}" {
		t.Errorf("unexpected file content: %q", resp.Files["preseed.cfg"])
	}
}

func TestGRPC_GetResponseTemplate_NotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetResponseTemplate(context.Background(), &pb.GetResponseTemplateRequest{Name: "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false")
	}
}

func TestGRPC_GetProvision_Found(t *testing.T) {
	k := newTestTypedClient(
		&typed.Provision{
			ObjectMeta: metav1.ObjectMeta{Name: "prov-1", Namespace: "default"},
			Spec: typed.ProvisionSpec{
				MachineRef:          "vm-01",
				BootTargetRef:       "debian-13",
				ResponseTemplateRef: "preseed",
				ConfigMaps:          []string{"net-cfg"},
				Secrets:             []string{"ssh-keys"},
				MachineId:           "0123456789abcdef0123456789abcdef",
			},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetProvision(context.Background(), &pb.GetProvisionRequest{Name: "prov-1"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.MachineRef != "vm-01" {
		t.Errorf("expected MachineRef vm-01, got %q", resp.MachineRef)
	}
	if resp.BootTargetRef != "debian-13" {
		t.Errorf("expected BootTargetRef debian-13, got %q", resp.BootTargetRef)
	}
	if resp.ResponseTemplateRef != "preseed" {
		t.Errorf("expected ResponseTemplateRef preseed, got %q", resp.ResponseTemplateRef)
	}
	if len(resp.ConfigMaps) != 1 || resp.ConfigMaps[0] != "net-cfg" {
		t.Errorf("unexpected ConfigMaps: %v", resp.ConfigMaps)
	}
	if len(resp.Secrets) != 1 || resp.Secrets[0] != "ssh-keys" {
		t.Errorf("unexpected Secrets: %v", resp.Secrets)
	}
	if resp.MachineId != "0123456789abcdef0123456789abcdef" {
		t.Errorf("unexpected MachineId: %q", resp.MachineId)
	}
}

func TestGRPC_GetProvision_NotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetProvision(context.Background(), &pb.GetProvisionRequest{Name: "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false")
	}
}

func TestGRPC_GetConfigMaps_MergesData(t *testing.T) {
	k := newTestTypedClient(
		newConfigMap("cm-1", map[string]string{"a": "1", "b": "2"}),
		newConfigMap("cm-2", map[string]string{"c": "3"}),
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetConfigMaps(context.Background(), &pb.GetConfigMapsRequest{Names: []string{"cm-1", "cm-2"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.Data["a"] != "1" || resp.Data["b"] != "2" || resp.Data["c"] != "3" {
		t.Errorf("unexpected merged data: %v", resp.Data)
	}
}

func TestGRPC_GetConfigMaps_MissingConfigMap(t *testing.T) {
	k := newTestTypedClient(
		newConfigMap("cm-1", map[string]string{"a": "1"}),
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetConfigMaps(context.Background(), &pb.GetConfigMapsRequest{Names: []string{"cm-1", "missing"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false when a configmap is missing")
	}
}

func TestGRPC_GetSecrets_MergesData(t *testing.T) {
	k := newTestTypedClient(
		newSecret("s-1", map[string][]byte{"key1": []byte("val1")}),
		newSecret("s-2", map[string][]byte{"key2": []byte("val2")}),
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetSecrets(context.Background(), &pb.GetSecretsRequest{Names: []string{"s-1", "s-2"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.Data["key1"] != "val1" || resp.Data["key2"] != "val2" {
		t.Errorf("unexpected merged data: %v", resp.Data)
	}
}

func TestGRPC_GetSecrets_MissingSecret(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetSecrets(context.Background(), &pb.GetSecretsRequest{Names: []string{"missing"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false when a secret is missing")
	}
}

func TestGRPC_GetBootMedia_Found(t *testing.T) {
	k := newTestTypedClient(
		&typed.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec: typed.BootMediaSpec{
				Kernel: &typed.BootMediaFileRef{URL: "http://example.com/linux"},
				Initrd: &typed.BootMediaFileRef{URL: "http://example.com/initrd.gz"},
			},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetBootMedia(context.Background(), &pb.GetBootMediaRequest{Name: "debian-13"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if resp.KernelFilename != "linux" {
		t.Errorf("expected KernelFilename=linux, got %q", resp.KernelFilename)
	}
	if resp.InitrdFilename != "initrd.gz" {
		t.Errorf("expected InitrdFilename=initrd.gz, got %q", resp.InitrdFilename)
	}
	if resp.HasFirmware {
		t.Error("expected HasFirmware=false")
	}
}

func TestGRPC_GetBootMedia_WithFirmware(t *testing.T) {
	k := newTestTypedClient(
		&typed.BootMedia{
			ObjectMeta: metav1.ObjectMeta{Name: "debian-13", Namespace: "default"},
			Spec: typed.BootMediaSpec{
				Kernel:   &typed.BootMediaFileRef{URL: "http://example.com/linux"},
				Initrd:   &typed.BootMediaFileRef{URL: "http://example.com/initrd.gz"},
				Firmware: &typed.BootMediaFileRef{URL: "http://example.com/firmware.cpio.gz"},
			},
		},
	)

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetBootMedia(context.Background(), &pb.GetBootMediaRequest{Name: "debian-13"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Found {
		t.Fatal("expected Found=true")
	}
	if !resp.HasFirmware {
		t.Error("expected HasFirmware=true")
	}
	if resp.KernelFilename != "linux" {
		t.Errorf("expected KernelFilename=linux, got %q", resp.KernelFilename)
	}
}

func TestGRPC_GetBootMedia_NotFound(t *testing.T) {
	k := newTestTypedClient()

	srv := NewGRPCServer(New(k))
	resp, err := srv.GetBootMedia(context.Background(), &pb.GetBootMediaRequest{Name: "missing"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Found {
		t.Error("expected Found=false")
	}
}
