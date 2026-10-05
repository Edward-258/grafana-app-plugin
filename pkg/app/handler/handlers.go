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

// fail 统一错误出口：配置缺失、标识/凭证对超量是调用方问题（400），其余视为
// 上游/阿里云故障（502）。extra 里的键会并入响应体（如 matched:false /
// ok:false），保持前端契约不变。
func fail(w http.ResponseWriter, err error, extra map[string]any) {
	status := http.StatusBadGateway
	if errors.Is(err, service.ErrNoSettings) || errors.Is(err, ErrTooManyIdentities) || errors.Is(err, ErrTooManyAKPairs) {
		status = http.StatusBadRequest
	}
	resp := map[string]any{"error": err.Error()}
	for k, v := range extra {
		resp[k] = v
	}
	writeJSON(w, status, resp)
}

func (a *App) handleResolve(w http.ResponseWriter, r *http.Request) {
	creds, err := credentialsFromRequest(r)
	if err != nil {
		fail(w, err, map[string]any{"matched": false})
		return
	}
	var ident service.Identity
	if !decodeBody(w, r, &ident) {
		return
	}
	row, ok, note, err := a.resolver.Resolve(r.Context(), creds, ident)
	if err != nil {
		fail(w, err, map[string]any{"matched": false})
		return
	}
	resp := map[string]any{"matched": ok, "monitorName": model.MonitorName(ident)}
	if note != "" {
		resp["note"] = note
	}
	if ok {
		// Enriched 内嵌 PublicAsset + ak/akLabel，instance 一并带出来源 AK
		resp["instance"] = row
	}
	a.attachBillings(resp, r, creds)
	writeJSON(w, http.StatusOK, resp)
}

// attachBillings 下发各 AK 的账户概览（余额/代金券/当月账单）：仅 ecs:reveal
//（Editor/Admin）可见——财务信息不对 Viewer 开放（2026-09-23 拍板）。
// 缓存中没有的（BSS 软失败/尚未拉取）该 AK 缺省，对所有角色一致。
func (a *App) attachBillings(resp map[string]any, r *http.Request, creds []service.Credential) {
	if !a.hasAction(r, actionReveal) {
		return
	}
	if rows := a.resolver.Billings(creds); len(rows) > 0 {
		resp["billings"] = rows
	}
}

func (a *App) handleEnrich(w http.ResponseWriter, r *http.Request) {
	creds, err := credentialsFromRequest(r)
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
	out, err := a.resolver.Enrich(r.Context(), creds, req.Identities, req.Refresh)
	if err != nil {
		fail(w, err, nil)
		return
	}
	resp := map[string]any{"instances": out}
	// 各 AK 的账户概览（余额/代金券/当月账单聚合）随响应下发；仅 ecs:reveal
	// 可见，BSS 整体软失败的 AK 缺省
	a.attachBillings(resp, r, creds)
	writeJSON(w, http.StatusOK, resp)
}

func (a *App) handleTest(w http.ResponseWriter, r *http.Request) {
	creds, err := credentialsFromRequest(r)
	if err != nil {
		fail(w, err, map[string]any{"ok": false})
		return
	}
	results := a.resolver.Test(r.Context(), creds)
	ok := len(results) > 0
	for _, res := range results {
		if res.Error != "" {
			ok = false
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": ok, "results": results})
}

// handleAK 下发全部插槽的展示信息：所有人拿脱敏值，完整值仅持有 ecs:reveal
// 的调用者（Editor/Admin）可见。配置不全的插槽也下发（UI 需要呈现待补全态）。
func (a *App) handleAK(w http.ResponseWriter, r *http.Request) {
	pCtx := backend.PluginConfigFromContext(r.Context())
	views := akPairViews(pCtx)
	canReveal := a.hasAction(r, actionReveal)
	pairs := make([]map[string]any, 0, len(views))
	configured := false
	for _, v := range views {
		if v.AKConfigured {
			configured = true
		}
		p := map[string]any{
			"slot":            v.Slot,
			"label":           v.Label,
			"masked":          maskAccessKeyID(v.AccessKeyID),
			"akConfigured":    v.AKConfigured,
			"secretConfigured": v.SecretConfigured,
		}
		if v.Legacy {
			p["legacy"] = true
		}
		if canReveal && v.AKConfigured {
			p["full"] = v.AccessKeyID
		}
		pairs = append(pairs, p)
	}
	resp := map[string]any{"configured": configured, "pairs": pairs}
	if canReveal {
		resp["canReveal"] = true
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
