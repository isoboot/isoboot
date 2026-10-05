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
package main

import (
	"flag"
	"io"
	"net/netip"
	"slices"
	"testing"
)

func parseAllowCIDR(args ...string) (cidrList, error) {
	flags := flag.NewFlagSet("nfsd", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var allow cidrList
	flags.Var(&allow, "allow-cidr", "")
	return allow, flags.Parse(args)
}

func TestAllowCIDRFlag(t *testing.T) {
	allow, err := parseAllowCIDR("--allow-cidr=192.168.101.0/24", "--allow-cidr=10.0.0.7/16", "--allow-cidr=fd00::/64")
	if err != nil {
		t.Fatal(err)
	}
	want := []netip.Prefix{
		netip.MustParsePrefix("192.168.101.0/24"),
		netip.MustParsePrefix("10.0.0.0/16"), // host bits dropped
		netip.MustParsePrefix("fd00::/64"),
	}
	if !slices.Equal(allow, want) {
		t.Errorf("allow = %v, want %v", allow, want)
	}

	if allow, err := parseAllowCIDR(); err != nil || len(allow) != 0 {
		t.Errorf("no flag: allow = %v, %v; want empty", allow, err)
	}

	// A bare address or a typo must stop nfsd, not open it to everyone.
	for _, bad := range []string{"192.168.101.5", "192.168.101.0/33", "subnet", ""} {
		if _, err := parseAllowCIDR("--allow-cidr=" + bad); err == nil {
			t.Errorf("--allow-cidr=%q accepted", bad)
		}
	}
}
