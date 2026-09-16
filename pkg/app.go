package main

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
	"github.com/grafana/grafana-plugin-sdk-go/backend/instancemgmt"
	"github.com/grafana/grafana-plugin-sdk-go/backend/resource/httpadapter"
)

var (
	_ backend.CallResourceHandler   = (*App)(nil)
	_ instancemgmt.InstanceDisposer = (*App)(nil)
	_ backend.CheckHealthHandler    = (*App)(nil)
)

type App struct {
	backend.CallResourceHandler

	mu    sync.Mutex
	cache *ecsCache
}

type ecsCache struct {
	key       string
	at        time.Time
	instances []Instance
}

type promIdentity struct {
	Instance string `json:"instance"`
	NodeName string `json:"nodename"`
	IP       string `json:"ip,omitempty"`
}

type enrichedAsset struct {
	PublicAsset
	MonitorName string `json:"monitorName,omitempty"`
	Matched     bool   `json:"matched"`
}

func NewApp(_ context.Context, _ backend.AppInstanceSettings) (instancemgmt.Instance, error) {
	a := &App{}
	mux := http.NewServeMux()
	mux.HandleFunc("/health", a.handleHealth)
	mux.HandleFunc("/ecs/enrich", a.handleEnrich)
	mux.HandleFunc("/ecs/resolve", a.handleResolve)
	mux.HandleFunc("/ecs/test", a.handleTest)
	a.CallResourceHandler = httpadapter.New(mux)
	return a, nil
}

func (a *App) Dispose() {}

func (a *App) CheckHealth(ctx context.Context, req *backend.CheckHealthRequest) (*backend.CheckHealthResult, error) {
	cfg, err := configFrom(req.PluginContext)
	if err != nil || cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return &backend.CheckHealthResult{
			Status:  backend.HealthStatusUnknown,
			Message: "未配置 AccessKey",
		}, nil
	}
	if _, err := a.instances(ctx, cfg); err != nil {
		return &backend.CheckHealthResult{Status: backend.HealthStatusError, Message: err.Error()}, nil
	}
	return &backend.CheckHealthResult{Status: backend.HealthStatusOk, Message: "ok"}, nil
}

func (a *App) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (a *App) handleResolve(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"matched": false, "error": err.Error()})
		return
	}
	ident := promIdentity{Instance: r.URL.Query().Get("q")}
	if r.Body != nil && r.Method != http.MethodGet {
		_ = json.NewDecoder(r.Body).Decode(&ident)
	}
	asset, ok, err := a.resolveAsset(r.Context(), cfg, ident)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"matched": false, "error": err.Error()})
		return
	}
	resp := map[string]any{"matched": ok, "monitorName": monitorName(ident)}
	if ok {
		resp["instance"] = asset
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *App) handleEnrich(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}
	var req struct {
		Identities []promIdentity `json:"identities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无效的 Prometheus 标识列表"})
		return
	}
	out := make([]enrichedAsset, 0, len(req.Identities))
	for _, ident := range req.Identities {
		row := enrichedAsset{MonitorName: monitorName(ident)}
		asset, ok, err := a.resolveAsset(r.Context(), cfg, ident)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		if ok {
			row.PublicAsset = asset
			row.Matched = true
		}
		out = append(out, row)
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out})
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	list, err := newECSClient(cfg).List(r.Context())
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	a.storeCache(cfg, list)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(list)})
}

func (a *App) resolveAsset(ctx context.Context, cfg Config, ident promIdentity) (PublicAsset, bool, error) {
	ip := strings.TrimSpace(ident.IP)
	if ip == "" {
		ip = ipFromIdentity(ident.Instance)
	}
	if ip == "" {
		ip = ipFromIdentity(ident.NodeName)
	}
	if ip == "" {
		list, err := a.instances(ctx, cfg)
		if err != nil {
			return PublicAsset{}, false, err
		}
		inst, _, ok := matchIdentity(ident, list)
		if !ok {
			return PublicAsset{}, false, nil
		}
		if len(inst.PrivateIPs) > 0 {
			ip = inst.PrivateIPs[0]
		} else if len(inst.PublicIPs) > 0 {
			ip = inst.PublicIPs[0]
		} else {
			return inst.Public(), true, nil
		}
	}

	found, ok, err := newECSClient(cfg).GetByIP(ctx, ip)
	if err != nil {
		return PublicAsset{}, false, err
	}
	if ok {
		return found.Public(), true, nil
	}
	return PublicAsset{}, false, nil
}

func (a *App) instances(ctx context.Context, cfg Config) ([]Instance, error) {
	key := cfg.Region + "|" + cfg.AccessKeyID
	a.mu.Lock()
	if a.cache != nil && a.cache.key == key && time.Since(a.cache.at) < 5*time.Minute {
		out := a.cache.instances
		a.mu.Unlock()
		return out, nil
	}
	a.mu.Unlock()

	list, err := newECSClient(cfg).List(ctx)
	if err != nil {
		return nil, err
	}
	a.storeCache(cfg, list)
	return list, nil
}

func (a *App) storeCache(cfg Config, list []Instance) {
	a.mu.Lock()
	a.cache = &ecsCache{key: cfg.Region + "|" + cfg.AccessKeyID, at: time.Now(), instances: list}
	a.mu.Unlock()
}

type jsonData struct {
	Region      string `json:"region"`
	AccessKeyID string `json:"accessKeyId"`
}

func configFromRequest(r *http.Request) (Config, error) {
	return configFrom(backend.PluginConfigFromContext(r.Context()))
}

func configFrom(pCtx backend.PluginContext) (Config, error) {
	cfg := Config{Region: "cn-hangzhou"}
	if pCtx.AppInstanceSettings == nil {
		return cfg, errNoSettings
	}
	var data jsonData
	if len(pCtx.AppInstanceSettings.JSONData) > 0 {
		_ = json.Unmarshal(pCtx.AppInstanceSettings.JSONData, &data)
		if data.Region != "" {
			cfg.Region = data.Region
		}
		cfg.AccessKeyID = data.AccessKeyID
	}
	if pCtx.AppInstanceSettings.DecryptedSecureJSONData != nil {
		cfg.AccessKeySecret = pCtx.AppInstanceSettings.DecryptedSecureJSONData["accessKeySecret"]
	}
	if cfg.AccessKeyID == "" || cfg.AccessKeySecret == "" {
		return cfg, errNoSettings
	}
	return cfg, nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
