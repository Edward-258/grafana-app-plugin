package model

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestPublicCarriesLeaseFields(t *testing.T) {
	inst := Instance{
		InstanceID:     "i-x",
		CreationTime:   "2024-05-20T08:30Z",
		ExpiredTime:    "2026-05-20T08:30Z",
		ChargeType:     "PrePaid",
		LeaseStartTime: "2024-05-20T08:31Z",
		PrivateIPs:     []string{"10.0.0.1"},
	}
	b, err := json.Marshal(inst.Public())
	if err != nil {
		t.Fatal(err)
	}
	s := string(b)
	for _, want := range []string{"creationTime", "2024-05-20T08:30Z", "expiredTime", "2026-05-20T08:30Z", "chargeType", "PrePaid", "leaseStart", "2024-05-20T08:31Z"} {
		if !strings.Contains(s, want) {
			t.Errorf("Public() 输出缺少 %q: %s", want, s)
		}
	}
	if strings.Contains(s, "10.0.0.1") {
		t.Errorf("Public() 不应包含 IP: %s", s)
	}
}
