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

func TestMatchRequiresUniqueWeakHits(t *testing.T) {
	list := []Instance{
		{InstanceID: "i-1", HostName: "web-1", InstanceName: "app", PrivateIPs: []string{"10.0.0.1"}, RegionID: "cn-hangzhou"},
		{InstanceID: "i-2", HostName: "web-1", InstanceName: "db", PrivateIPs: []string{"10.0.0.2"}, RegionID: "cn-heyuan"},
		{InstanceID: "i-3", HostName: "cache", InstanceName: "cache", PublicIPs: []string{"203.0.113.5"}, RegionID: "cn-beijing"},
	}

	if inst, by, _, ok := Match("i-2", list); !ok || by != "instanceId" || inst.RegionID != "cn-heyuan" {
		t.Fatalf("instanceId match failed: by=%q ok=%v", by, ok)
	}
	if _, by, _, ok := Match("10.0.0.2:9100", list); !ok || by != "ip" {
		t.Fatalf("unique ip should match, by=%q ok=%v", by, ok)
	}
	if _, _, note, ok := Match("web-1", list); ok || note == "" {
		t.Fatalf("hostname duplicated across regions must stay unmatched, ok=%v note=%q", ok, note)
	}
	if _, by, _, ok := Match("db", list); !ok || by != "instanceName" {
		t.Fatalf("unique instanceName should match, by=%q ok=%v", by, ok)
	}
	if _, _, _, ok := Match("missing", list); ok {
		t.Fatal("unknown name must not match")
	}
	if _, _, note, ok := MatchIdentity(Identity{Instance: "10.0.0.1:9100", NodeName: "web-1"}, list); !ok {
		t.Fatalf("identity via ip should match, note=%q", note)
	}
}

func TestRegionSet(t *testing.T) {
	list := []Instance{
		{InstanceID: "i-1", RegionID: "cn-hangzhou"},
		{InstanceID: "i-2", RegionID: "cn-hangzhou"},
		{InstanceID: "i-3", RegionID: "cn-heyuan"},
		{InstanceID: "i-4"},
	}
	if got := RegionSet(list); len(got) != 2 {
		t.Fatalf("want 2 regions, got %v", got)
	}
}
