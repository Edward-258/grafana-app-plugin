package model

import (
	"net"
	"strconv"
	"strings"
)

// Identity is the Prometheus-side handle for a monitored machine.
type Identity struct {
	Instance string `json:"instance"`
	NodeName string `json:"nodename"`
	IP       string `json:"ip,omitempty"`
}

func Host(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	if strings.HasPrefix(s, "[") {
		if i := strings.Index(s, "]"); i > 0 {
			return s[1:i]
		}
	}
	if i := strings.LastIndex(s, ":"); i > 0 {
		if _, err := strconv.Atoi(s[i+1:]); err == nil {
			return s[:i]
		}
	}
	return s
}

func IPFromIdentity(s string) string {
	host := Host(s)
	if net.ParseIP(host) == nil {
		return ""
	}
	return host
}

// Match resolves one query string against the whole asset list. Weak
// identifiers (IP, hostName, instanceName) must hit exactly one instance —
// duplicates across regions stay unmatched so a row can never claim the
// wrong machine.
func Match(query string, list []Instance) (Instance, string, string, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return Instance{}, "", "", false
	}
	host := Host(q)
	steps := []struct {
		by   string
		note string
		hits func(Instance) bool
	}{
		{"instanceId", "", func(i Instance) bool { return i.InstanceID == q || (host != "" && i.InstanceID == host) }},
		{"ip", "IP 命中多个实例", func(i Instance) bool { return contains(i.PrivateIPs, q, host) || contains(i.PublicIPs, q, host) }},
		{"hostName", "主机名命中多个实例", func(i Instance) bool { return i.HostName != "" && (i.HostName == q || (host != "" && i.HostName == host)) }},
		{"instanceName", "实例名命中多个实例", func(i Instance) bool { return i.InstanceName != "" && (i.InstanceName == q || (host != "" && i.InstanceName == host)) }},
	}
	for _, step := range steps {
		found := collect(list, step.hits)
		switch len(found) {
		case 1:
			return found[0], step.by, "", true
		case 0:
			continue
		default:
			return Instance{}, "", step.note, false
		}
	}
	return Instance{}, "", "", false
}

func collect(list []Instance, hit func(Instance) bool) []Instance {
	var out []Instance
	seen := map[string]struct{}{}
	for _, inst := range list {
		if !hit(inst) {
			continue
		}
		if _, dup := seen[inst.InstanceID]; dup {
			continue
		}
		seen[inst.InstanceID] = struct{}{}
		out = append(out, inst)
	}
	return out
}

func contains(list []string, values ...string) bool {
	for _, v := range list {
		for _, want := range values {
			if want != "" && v == want {
				return true
			}
		}
	}
	return false
}

func MatchIdentity(ident Identity, list []Instance) (Instance, string, string, bool) {
	note := ""
	for _, q := range []string{ident.Instance, ident.NodeName, ident.IP} {
		inst, by, n, ok := Match(q, list)
		if ok {
			return inst, by, "", true
		}
		if note == "" {
			note = n
		}
	}
	return Instance{}, "", note, false
}

func MonitorName(ident Identity) string {
	if ident.NodeName != "" && IPFromIdentity(ident.NodeName) == "" {
		return ident.NodeName
	}
	host := Host(ident.Instance)
	if host != "" && IPFromIdentity(host) == "" {
		return host
	}
	return ""
}
