package handler

import (
	"encoding/json"
	"net/http"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"local-ecs-app/pkg/app/service"
)

// AKSlot 是 jsonData.akList 的条目（app 与捆绑 ds 共用的 JSON 契约）：slot 是前端生成的随机 uuid（插件内主键），
// label 用户可读标签。jsonData 只存这类非敏感值——AK/SK 明文只出现在
// secureJsonData 的 ak:<slot>:* 键下（红线4）。
type AKSlot struct {
	Slot      string `json:"slot"`
	Label     string `json:"label,omitempty"`
	CreatedAt string `json:"createdAt,omitempty"`
}

// appJSONData 是本插件 jsonData 的解析体。
type appJSONData struct {
	AccessKeyID string   `json:"accessKeyId"` // 最老格式（jsonData 明文），保存时迁入插槽
	AKList      []AKSlot `json:"akList"`
}

// SecureKeyID / SecureKeySecret 构造一对凭证在 secureJsonData 里的键名，
// app 与捆绑 ds 共用（ds 读、sync.go 写），键名 scheme 只此一处。
func SecureKeyID(slot string) string { return "ak:" + slot + ":id" }

func SecureKeySecret(slot string) string { return "ak:" + slot + ":secret" }

func credentialsFromRequest(r *http.Request) ([]service.Credential, error) {
	return credentialsFrom(backend.PluginConfigFromContext(r.Context()))
}

// akPairView 是一个插槽的完整视图（含未配齐的）：AK/SK 明文仅服务端内部
// 使用，序列化响应必须另行构造，绝不直接输出本类型。
type akPairView struct {
	Slot             string
	Label            string
	AccessKeyID      string
	AccessKeySecret  string
	Legacy           bool
	AKConfigured     bool
	SecretConfigured bool
}

// akPairViews 列出全部插槽（含配置不全的）。akList 定义插槽顺序；slot 为
// legacy 的插槽回退读旧格式键（accessKeyId/accessKeySecret），使迁移期每个
// 中间状态都保持可用；akList 缺失（老安装）时整个回退为单个 legacy 插槽。
func akPairViews(pCtx backend.PluginContext) []akPairView {
	if pCtx.AppInstanceSettings == nil {
		return nil
	}
	secure := pCtx.AppInstanceSettings.DecryptedSecureJSONData
	var data appJSONData
	if len(pCtx.AppInstanceSettings.JSONData) > 0 {
		_ = json.Unmarshal(pCtx.AppInstanceSettings.JSONData, &data)
	}

	legacyID := secure["accessKeyId"]
	if legacyID == "" {
		legacyID = data.AccessKeyID
	}
	legacySecret := secure["accessKeySecret"]

	out := make([]akPairView, 0, len(data.AKList)+1)
	for _, slot := range data.AKList {
		if slot.Slot == "" {
			continue
		}
		id := secure[SecureKeyID(slot.Slot)]
		secret := secure[SecureKeySecret(slot.Slot)]
		if slot.Slot == service.LegacyCredentialID {
			if id == "" {
				id = legacyID
			}
			if secret == "" {
				secret = legacySecret
			}
		}
		out = append(out, akPairView{
			Slot:             slot.Slot,
			Label:            slot.Label,
			AccessKeyID:      id,
			AccessKeySecret:  secret,
			AKConfigured:     id != "",
			SecretConfigured: secret != "",
			Legacy:           slot.Slot == service.LegacyCredentialID,
		})
	}
	if len(out) == 0 && (legacyID != "" || legacySecret != "") {
		out = append(out, akPairView{
			Slot:             service.LegacyCredentialID,
			Label:            "默认",
			AccessKeyID:      legacyID,
			AccessKeySecret:  legacySecret,
			AKConfigured:     legacyID != "",
			SecretConfigured: legacySecret != "",
			Legacy:           true,
		})
	}
	return out
}

// credentialsFrom 返回全部配齐的凭证对：跳过配置不全的插槽，一对都没有时
// ErrNoSettings，超过 maxAKPairs 报错（读时兜底——Grafana 的设置保存不经
// 插件 handler，写入侧上限只能靠 UI 约束）。
func credentialsFrom(pCtx backend.PluginContext) ([]service.Credential, error) {
	var out []service.Credential
	for _, v := range akPairViews(pCtx) {
		if v.AKConfigured && v.SecretConfigured {
			out = append(out, service.Credential{
				ID:              v.Slot,
				Label:           v.Label,
				AccessKeyID:     v.AccessKeyID,
				AccessKeySecret: v.AccessKeySecret,
			})
		}
	}
	if len(out) == 0 {
		return nil, service.ErrNoSettings
	}
	if err := ValidateAKPairCount(len(out)); err != nil {
		return nil, err
	}
	return out, nil
}
