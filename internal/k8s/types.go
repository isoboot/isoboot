package k8s

import (
	"fmt"
	"net/url"
	"path"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +kubebuilder:object:root=true
// Machine represents a Machine CRD
type Machine struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              MachineSpec `json:"spec"`
}

type MachineSpec struct {
	MAC string `json:"mac"`
}

// +kubebuilder:object:root=true
type MachineList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Machine `json:"items"`
}

// +kubebuilder:object:root=true
// Provision represents a Provision CRD
type Provision struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ProvisionSpec   `json:"spec"`
	Status            ProvisionStatus `json:"status,omitempty"`
}

type ProvisionSpec struct {
	MachineRef          string   `json:"machineRef"`
	BootTargetRef       string   `json:"bootTargetRef,omitempty"`
	Target              string   `json:"target,omitempty"` // legacy field, use GetBootTargetRef()
	ResponseTemplateRef string   `json:"responseTemplateRef,omitempty"`
	ConfigMaps          []string `json:"configMaps,omitempty"`
	Secrets             []string `json:"secrets,omitempty"`
	MachineId           string   `json:"machineId,omitempty"`
}

// GetBootTargetRef returns the boot target reference, falling back to the legacy Target field.
func (s *ProvisionSpec) GetBootTargetRef() string {
	if s.BootTargetRef != "" {
		return s.BootTargetRef
	}
	return s.Target
}

type ProvisionStatus struct {
	Phase       string      `json:"phase,omitempty"`
	Message     string      `json:"message,omitempty"`
	LastUpdated metav1.Time `json:"lastUpdated,omitempty"`
	IP          string      `json:"ip,omitempty"`
}

// +kubebuilder:object:root=true
type ProvisionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Provision `json:"items"`
}

// BootMediaFileRef represents a file to download (kernel, initrd, or firmware)
type BootMediaFileRef struct {
	URL         string `json:"url"`
	ChecksumURL string `json:"checksumURL,omitempty"`
}

// BootMediaISO represents an ISO to download and extract files from
type BootMediaISO struct {
	URL         string `json:"url"`
	ChecksumURL string `json:"checksumURL,omitempty"`
	Kernel      string `json:"kernel"` // path within ISO
	Initrd      string `json:"initrd"` // path within ISO
}

// +kubebuilder:object:root=true
// BootMedia represents a BootMedia CRD (owns file downloads)
type BootMedia struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BootMediaSpec   `json:"spec"`
	Status            BootMediaStatus `json:"status,omitempty"`
}

type BootMediaSpec struct {
	Kernel   *BootMediaFileRef `json:"kernel,omitempty"`
	Initrd   *BootMediaFileRef `json:"initrd,omitempty"`
	ISO      *BootMediaISO     `json:"iso,omitempty"`
	Firmware *BootMediaFileRef `json:"firmware,omitempty"`
}

// BootMediaStatus represents the status of a BootMedia
type BootMediaStatus struct {
	Phase          string      `json:"phase,omitempty"`
	Message        string      `json:"message,omitempty"`
	Kernel         *FileStatus `json:"kernel,omitempty"`
	Initrd         *FileStatus `json:"initrd,omitempty"`
	ISO            *FileStatus `json:"iso,omitempty"`
	Firmware       *FileStatus `json:"firmware,omitempty"`
	FirmwareInitrd *FileStatus `json:"firmwareInitrd,omitempty"`
}

// FileStatus represents the download status of a single file
type FileStatus struct {
	Name   string `json:"name,omitempty"`
	Phase  string `json:"phase,omitempty"`
	SHA256 string `json:"sha256,omitempty"`
}

// +kubebuilder:object:root=true
type BootMediaList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BootMedia `json:"items"`
}

// KernelFilename returns the basename of the kernel file
func (bm *BootMedia) KernelFilename() string {
	if bm.Spec.Kernel != nil {
		if name, err := FilenameFromURL(bm.Spec.Kernel.URL); err == nil {
			return name
		}
	}
	if bm.Spec.ISO != nil {
		return path.Base(bm.Spec.ISO.Kernel)
	}
	return ""
}

// InitrdFilename returns the basename of the initrd file
func (bm *BootMedia) InitrdFilename() string {
	if bm.Spec.Initrd != nil {
		if name, err := FilenameFromURL(bm.Spec.Initrd.URL); err == nil {
			return name
		}
	}
	if bm.Spec.ISO != nil {
		return path.Base(bm.Spec.ISO.Initrd)
	}
	return ""
}

// HasFirmware returns whether this BootMedia has firmware
func (bm *BootMedia) HasFirmware() bool {
	return bm.Spec.Firmware != nil
}

// Validate checks BootMedia spec for correctness
func (bm *BootMedia) Validate() error {
	hasDirect := bm.Spec.Kernel != nil || bm.Spec.Initrd != nil
	hasISO := bm.Spec.ISO != nil

	// Mutual exclusivity: direct XOR ISO
	if hasDirect && hasISO {
		return fmt.Errorf("cannot specify both kernel/initrd and iso")
	}
	if !hasDirect && !hasISO {
		return fmt.Errorf("must specify either kernel+initrd or iso")
	}

	// Direct mode: both kernel and initrd required
	if hasDirect {
		if bm.Spec.Kernel == nil {
			return fmt.Errorf("kernel requires initrd")
		}
		if bm.Spec.Initrd == nil {
			return fmt.Errorf("initrd requires kernel")
		}
		if bm.Spec.Kernel.URL == "" {
			return fmt.Errorf("kernel.url is required")
		}
		if bm.Spec.Initrd.URL == "" {
			return fmt.Errorf("initrd.url is required")
		}
	}

	// ISO mode: kernel and initrd paths required
	if hasISO {
		if bm.Spec.ISO.URL == "" {
			return fmt.Errorf("iso.url is required")
		}
		if bm.Spec.ISO.Kernel == "" {
			return fmt.Errorf("iso.kernel is required")
		}
		if bm.Spec.ISO.Initrd == "" {
			return fmt.Errorf("iso.initrd is required")
		}
	}

	// Basename uniqueness
	basenames := make(map[string]string) // basename -> source description
	addBasename := func(name, source string) error {
		if prev, exists := basenames[name]; exists {
			return fmt.Errorf("duplicate basename %q: used by %s and %s", name, prev, source)
		}
		basenames[name] = source
		return nil
	}

	if bm.Spec.Kernel != nil {
		name, err := FilenameFromURL(bm.Spec.Kernel.URL)
		if err != nil {
			return fmt.Errorf("kernel: %w", err)
		}
		if err := addBasename(name, "kernel"); err != nil {
			return err
		}
	}
	if bm.Spec.Initrd != nil {
		name, err := FilenameFromURL(bm.Spec.Initrd.URL)
		if err != nil {
			return fmt.Errorf("initrd: %w", err)
		}
		if err := addBasename(name, "initrd"); err != nil {
			return err
		}
	}
	if bm.Spec.ISO != nil {
		name, err := FilenameFromURL(bm.Spec.ISO.URL)
		if err != nil {
			return fmt.Errorf("iso: %w", err)
		}
		if err := addBasename(name, "iso"); err != nil {
			return err
		}
		if err := addBasename(path.Base(bm.Spec.ISO.Kernel), "iso.kernel"); err != nil {
			return err
		}
		if err := addBasename(path.Base(bm.Spec.ISO.Initrd), "iso.initrd"); err != nil {
			return err
		}
	}
	if bm.Spec.Firmware != nil {
		if bm.Spec.Firmware.URL == "" {
			return fmt.Errorf("firmware.url is required")
		}
		name, err := FilenameFromURL(bm.Spec.Firmware.URL)
		if err != nil {
			return fmt.Errorf("firmware: %w", err)
		}
		if err := addBasename(name, "firmware"); err != nil {
			return err
		}
	}

	return nil
}

// FilenameFromURL extracts the filename from a URL
func FilenameFromURL(rawURL string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse URL: %w", err)
	}
	filename := path.Base(u.Path)
	if filename == "." || filename == "/" {
		return "", fmt.Errorf("URL has no filename: %s", rawURL)
	}
	return filename, nil
}

// +kubebuilder:object:root=true
// BootTarget represents a BootTarget CRD (references a BootMedia, adds template)
type BootTarget struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              BootTargetSpec `json:"spec"`
}

type BootTargetSpec struct {
	BootMediaRef string `json:"bootMediaRef"`
	UseFirmware  bool   `json:"useFirmware,omitempty"`
	Template     string `json:"template"`
}

// +kubebuilder:object:root=true
type BootTargetList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []BootTarget `json:"items"`
}

// +kubebuilder:object:root=true
// ResponseTemplate represents a ResponseTemplate CRD
type ResponseTemplate struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec              ResponseTemplateSpec `json:"spec"`
}

type ResponseTemplateSpec struct {
	Files map[string]string `json:"files,omitempty"`
}

// +kubebuilder:object:root=true
type ResponseTemplateList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ResponseTemplate `json:"items"`
}
