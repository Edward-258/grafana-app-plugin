package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"local-ecs-app/pkg/aliyun/model"
	"local-ecs-app/pkg/app/service"
)

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// fail 统一错误出口：配置缺失、标识超量是调用方问题（400），其余视为上游/阿里云故障（502）。
// extra 里的键会并入响应体（如 matched:false / ok:false），保持前端契约不变。
func fail(w http.ResponseWriter, err error, extra map[string]any) {
	status := http.StatusBadGateway
	if errors.Is(err, service.ErrNoSettings) || errors.Is(err, ErrTooManyIdentities) {
		status = http.StatusBadRequest
	}
	resp := map[string]any{"error": err.Error()}
	for k, v := range extra {
		resp[k] = v
	}
	writeJSON(w, status, resp)
}

func (a *App) handleResolve(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		fail(w, err, map[string]any{"matched": false})
		return
	}
	var ident service.Identity
	if !decodeBody(w, r, &ident) {
		return
	}
	asset, ok, note, err := a.resolver.Resolve(r.Context(), cfg, ident)
	if err != nil {
		fail(w, err, map[string]any{"matched": false})
		return
	}
	resp := map[string]any{"matched": ok, "monitorName": model.MonitorName(ident)}
	if note != "" {
		resp["note"] = note
	}
	if ok {
		resp["instance"] = asset
	}
	a.attachBilling(resp, r)
	writeJSON(w, http.StatusOK, resp)
}

// attachBilling 下发账户概览（余额/代金券/当月账单）：仅 ecs:reveal
//（Editor/Admin）可见——财务信息不对 Viewer 开放（2026-09-23 拍板）。
// BSS 整体软失败时缺省，对所有角色一致。
func (a *App) attachBilling(resp map[string]any, r *http.Request) {
	if ov, ok := a.resolver.Billing(); ok && a.hasAction(r, actionReveal) {
		resp["billing"] = ov
	}
}

func (a *App) handleEnrich(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		fail(w, err, nil)
		return
	}
	var req struct {
		Identities []service.Identity `json:"identities"`
		// refresh=true：绕过缓存强制实时拉取（前端刷新键语义），仍走同一
		// 完整性约束：全地域枚举、任一地域失败整体失败。
		Refresh bool `json:"refresh"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if err := ValidateIdentities(req.Identities); err != nil {
		fail(w, err, nil)
		return
	}
	out, err := a.resolver.Enrich(r.Context(), cfg, req.Identities, req.Refresh)
	if err != nil {
		fail(w, err, nil)
		return
	}
	resp := map[string]any{"instances": out}
	// 账户概览（余额/代金券/当月账单聚合，按量付费资源共同消耗余额池）随响应
	// 下发；仅 ecs:reveal 可见，BSS 整体软失败时缺省
	a.attachBilling(resp, r)
	writeJSON(w, http.StatusOK, resp)
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	cfg, err := configFromRequest(r)
	if err != nil {
		fail(w, err, map[string]any{"ok": false})
		return
	}
	n, regions, err := a.resolver.Test(r.Context(), cfg)
	if err != nil {
		fail(w, err, map[string]any{"ok": false})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "count": n, "regions": regions})
}

// handleAK 下发 AccessKey ID 的展示信息：所有人拿脱敏值，
// 完整值仅持有 ecs:reveal 的调用者（Editor/Admin）可见。
func (a *App) handleAK(w http.ResponseWriter, r *http.Request) {
	pCtx := backend.PluginConfigFromContext(r.Context())
	value, legacy, configured := currentAccessKeyID(pCtx)
	resp := map[string]any{
		"configured": configured,
		"masked":     maskAccessKeyID(value),
	}
	if legacy {
		resp["legacy"] = true
	}
	if configured && a.hasAction(r, actionReveal) {
		resp["canReveal"] = true
		resp["full"] = value
	}
	writeJSON(w, http.StatusOK, resp)
}

// maskAccessKeyID 脱敏：常规长度前 3 后 3，过短则更严。
func maskAccessKeyID(v string) string {
	runes := []rune(v)
	n := len(runes)
	switch {
	case n == 0:
		return ""
	case n < 6:
		return "***"
	case n < 10:
		return string(runes[:2]) + "…" + string(runes[n-2:])
	default:
		return string(runes[:3]) + "…" + string(runes[n-3:])
	}
}
