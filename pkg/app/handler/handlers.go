package handler

import (
	"encoding/json"
	"net/http"

	"local-ecs-app/pkg/app/service"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
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
	ident := service.Identity{Instance: r.URL.Query().Get("q")}
	if r.Body != nil && r.Method != http.MethodGet {
		_ = json.NewDecoder(r.Body).Decode(&ident)
	}
	asset, ok, note, err := a.resolver.Resolve(r.Context(), cfg, ident)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"matched": false, "error": err.Error()})
		return
	}
	resp := map[string]any{"matched": ok, "monitorName": a.resolver.MonitorName(ident)}
	if note != "" {
		resp["note"] = note
	}
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
		Identities []service.Identity `json:"identities"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "无效的 Prometheus 标识列表"})
		return
	}
	out, err := a.resolver.Enrich(r.Context(), cfg, req.Identities)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"instances": out})
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	n, regions, err := a.resolver.Test(r.Context(), cfg)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": n, "regions": regions})
}
