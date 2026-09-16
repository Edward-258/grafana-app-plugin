package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicAssetOmitsIPs(t *testing.T) {
	inst := Instance{
		InstanceID:   "i-test",
		InstanceName: "web",
		HostName:     "web-host",
		InstanceType: "ecs.g7.large",
		CPU:          2,
		MemoryGiB:    8,
		PrivateIPs:   []string{"10.0.0.8"},
		PublicIPs:    []string{"203.0.113.9"},
	}
	b, err := json.Marshal(inst.Public())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, ip := range []string{"10.0.0.8", "203.0.113.9", "privateIp", "publicIp"} {
		if strings.Contains(s, ip) {
			t.Fatalf("public JSON leaked %q: %s", ip, s)
		}
	}
	if !strings.Contains(s, "i-test") || !strings.Contains(s, "ecs.g7.large") {
		t.Fatalf("missing asset fields: %s", s)
	}
}

func TestIPFromIdentity(t *testing.T) {
	if got := IPFromIdentity("10.1.2.3:9100"); got != "10.1.2.3" {
		t.Fatalf("got %q", got)
	}
	if got := IPFromIdentity("web-1:9100"); got != "" {
		t.Fatalf("hostname should not be IP, got %q", got)
	}
	if got := MonitorName(Identity{Instance: "10.1.2.3:9100", NodeName: "web-1"}); got != "web-1" {
		t.Fatalf("MonitorName=%q", got)
	}
	if got := MonitorName(Identity{Instance: "10.1.2.3:9100"}); got != "" {
		t.Fatalf("must not surface IP as MonitorName, got %q", got)
	}
}
