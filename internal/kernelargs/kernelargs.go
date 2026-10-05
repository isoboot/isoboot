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

// Package kernelargs renders a BootConfig's kernelArgs template. httpd
// renders it for each boot, and the BootConfig controller renders it with
// sample values to report a broken template on the BootConfig instead of at
// boot time.
package kernelargs

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"text/template"
)

// Data holds the values a kernelArgs template can use.
type Data struct {
	ProvisionAutomationBaseURL string
	ProxyURL                   string
	UpdatePhaseURL             string
	ProvisionName              string
	// NFSRoot is "<IPv4>:/<bootconfig>" for ISO-mode BootConfigs (the
	// casper nfsroot= value) and empty in netboot mode.
	NFSRoot string
}

// errLineBreak is returned for a rendered line that is not a single line:
// in the iPXE script every further line would be another command.
var errLineBreak = errors.New("kernel args must be a single line")

// Render renders args as a Go template with data. The result must be one line.
func Render(args string, data Data) (string, error) {
	tmpl, err := template.New("kernelArgs").
		Option("missingkey=error").Parse(args)
	if err != nil {
		return "", fmt.Errorf("parsing kernel args template: %w", err)
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("executing kernel args template: %w", err)
	}
	if strings.ContainsAny(buf.String(), "\r\n") {
		return "", errLineBreak
	}
	return buf.String(), nil
}

// sampleData has a value for every field, in the shape httpd fills them in
// (192.0.2.1 is reserved for documentation).
var sampleData = Data{
	ProvisionAutomationBaseURL: "http://192.0.2.1:8080/dynamic/automation/sample",
	ProxyURL:                   "http://192.0.2.1:3128",
	UpdatePhaseURL:             "http://192.0.2.1:8080/dynamic/status",
	ProvisionName:              "sample",
	NFSRoot:                    "192.0.2.1:/sample",
}

// Validate reports whether args renders, using sample values.
func Validate(args string) error {
	_, err := Render(args, sampleData)
	return err
}
