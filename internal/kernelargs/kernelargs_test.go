/*
Copyright 2026.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package kernelargs

import (
	"strings"
	"testing"
)

func TestRender(t *testing.T) {
	base := "http://10.0.0.1:8080/dynamic/automation/my-provision"
	tests := []struct {
		name string
		args string
		data Data
		want string
	}{
		{"plain string unchanged", "console=ttyS0 ip=dhcp", Data{}, "console=ttyS0 ip=dhcp"},
		{"empty", "", Data{}, ""},
		{"ProvisionAutomationBaseURL", "ip=dhcp inst.ks={{.ProvisionAutomationBaseURL}}/ks.cfg",
			Data{ProvisionAutomationBaseURL: base}, "ip=dhcp inst.ks=" + base + "/ks.cfg"},
		{"ProxyURL", "inst.proxy={{.ProxyURL}}", Data{ProxyURL: "http://10.0.0.1:3128"},
			"inst.proxy=http://10.0.0.1:3128"},
		{"ProxyURL block omitted when empty", "ip=dhcp {{if .ProxyURL}}inst.proxy={{.ProxyURL}} {{end}}inst.repo=x",
			Data{}, "ip=dhcp inst.repo=x"},
		{"UpdatePhaseURL and ProvisionName", "s={{.UpdatePhaseURL}} n={{.ProvisionName}}",
			Data{UpdatePhaseURL: "http://10.0.0.1:8080/dynamic/status", ProvisionName: "my-provision"},
			"s=http://10.0.0.1:8080/dynamic/status n=my-provision"},
		{"NFSRoot", "netboot=nfs nfsroot={{.NFSRoot}} ds=nocloud;s={{.ProvisionAutomationBaseURL}}/ ---",
			Data{ProvisionAutomationBaseURL: base, NFSRoot: "10.0.0.1:/ubuntu-26.04"},
			"netboot=nfs nfsroot=10.0.0.1:/ubuntu-26.04 ds=nocloud;s=" + base + "/ ---"},
		// kernelArgs: > (a YAML folded scalar) keeps one final line break.
		{"final line break dropped", "console=ttyS0 ip=dhcp\n", Data{}, "console=ttyS0 ip=dhcp"},
		{"final CRLF dropped", "console=ttyS0 ip=dhcp\r\n", Data{}, "console=ttyS0 ip=dhcp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Render(tt.args, tt.data)
			if err != nil {
				t.Fatalf("Render: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRenderErrors(t *testing.T) {
	tests := []struct {
		name    string
		args    string
		data    Data
		wantErr string
	}{
		{"invalid syntax", "{{.Foo", Data{}, "parsing kernel args template"},
		{"unknown variable", "{{.UnknownVar}}", Data{}, "executing kernel args template"},
		{"ISOURL no longer exists", "url={{.ISOURL}}", Data{}, "ISOURL"},
		{"line break in the template", "ip=dhcp\nchain http://x", Data{}, "single line"},
		{"carriage return in the template", "ip=dhcp\rx", Data{}, "single line"},
		{"line break before the final one", "ip=dhcp\nchain http://x\n", Data{}, "single line"},
		{"line break from the data", "name={{.ProvisionName}}", Data{ProvisionName: "a\nb"}, "single line"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Render(tt.args, tt.data)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %v, want one containing %q", err, tt.wantErr)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	valid := "ks={{.ProvisionAutomationBaseURL}}/ks.cfg {{if .ProxyURL}}proxy={{.ProxyURL}}{{end}} " +
		"status={{.UpdatePhaseURL}} name={{.ProvisionName}} nfsroot={{.NFSRoot}}"
	if err := Validate(valid); err != nil {
		t.Errorf("Validate(%q) = %v, want nil", valid, err)
	}
	if err := Validate("url={{.ISOURL}}"); err == nil {
		t.Error("Validate accepted {{.ISOURL}}")
	}
}
