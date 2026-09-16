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

func Match(query string, list []Instance) (Instance, string, bool) {
	q := strings.TrimSpace(query)
	if q == "" {
		return Instance{}, "", false
	}
	host := Host(q)

	for _, inst := range list {
		if inst.InstanceID == q || inst.InstanceID == host {
			return inst, "instanceId", true
		}
	}
	for _, inst := range list {
		for _, ip := range inst.PrivateIPs {
			if ip == host || ip == q {
				return inst, "privateIp", true
			}
		}
		for _, ip := range inst.PublicIPs {
			if ip == host || ip == q {
				return inst, "publicIp", true
			}
		}
	}
	for _, inst := range list {
		if inst.HostName != "" && (inst.HostName == host || inst.HostName == q) {
			return inst, "hostName", true
		}
	}
	for _, inst := range list {
		if inst.InstanceName != "" && (inst.InstanceName == host || inst.InstanceName == q) {
			return inst, "instanceName", true
		}
	}
	return Instance{}, "", false
}

func MatchIdentity(ident Identity, list []Instance) (Instance, string, bool) {
	for _, q := range []string{ident.Instance, ident.NodeName, ident.IP} {
		if inst, by, ok := Match(q, list); ok {
			return inst, by, true
		}
	}
	return Instance{}, "", false
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
