package handler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"

	"local-ecs-app/pkg/app/service"
)
func testAppSettings(jsonData string, secure map[string]string) backend.PluginContext {
	return backend.PluginContext{AppInstanceSettings: &backend.AppInstanceSettings{
		JSONData:                []byte(jsonData),
		DecryptedSecureJSONData: secure,
	}}
}

// 多插槽解析：akList 定义插槽，secureJsonData 提供值；配置不全的插槽跳过。
func TestCredentialsFromMultiSlot(t *testing.T) {
	pCtx := testAppSettings(
		`{"akList":[{"slot":"s1","label":"主账号"},{"slot":"s2","label":"残缺"},{"slot":"s3","label":"也残缺"}]}`,
		map[string]string{
			SecureKeyID("s1"): "LTAI1", SecureKeySecret("s1"): "sk1",
			SecureKeyID("s2"): "LTAI2", // secret 缺失 → 跳过
			SecureKeySecret("s3"): "sk3", // id 缺失 → 跳过
		},
	)
	creds, err := credentialsFrom(pCtx)
	if err != nil || len(creds) != 1 {
		t.Fatalf("只有配齐的插槽应入选: creds=%+v err=%v", creds, err)
	}
	if creds[0].ID != "s1" || creds[0].Label != "主账号" || creds[0].AccessKeyID != "LTAI1" || creds[0].AccessKeySecret != "sk1" {
		t.Fatalf("凭证字段不符: %+v", creds[0])
	}
}

// legacy 兜底三态：旧 secure 键、jsonData 明文（最老格式）、akList 显式引用
// legacy 插槽时回退旧键。
func TestCredentialsFromLegacyFallback(t *testing.T) {
	pCtx := testAppSettings("", map[string]string{"accessKeyId": "LTAI-old", "accessKeySecret": "sk-old"})
	creds, err := credentialsFrom(pCtx)
	if err != nil || len(creds) != 1 || creds[0].ID != service.LegacyCredentialID || creds[0].AccessKeyID != "LTAI-old" {
		t.Fatalf("旧 secure 键应兜底为 legacy 插槽: %+v err=%v", creds, err)
	}

	pCtx2 := testAppSettings(`{"accessKeyId":"LTAI-json"}`, map[string]string{"accessKeySecret": "sk-old"})
	creds2, err := credentialsFrom(pCtx2)
	if err != nil || len(creds2) != 1 || creds2[0].AccessKeyID != "LTAI-json" {
		t.Fatalf("jsonData 明文应兜底: %+v err=%v", creds2, err)
	}

	pCtx3 := testAppSettings(
		`{"akList":[{"slot":"legacy","label":"默认"}]}`,
		map[string]string{"accessKeyId": "LTAI-old", "accessKeySecret": "sk-old"},
	)
	creds3, err := credentialsFrom(pCtx3)
	if err != nil || len(creds3) != 1 || creds3[0].ID != service.LegacyCredentialID || creds3[0].Label != "默认" || creds3[0].AccessKeyID != "LTAI-old" {
		t.Fatalf("akList 引用 legacy 插槽应回退旧键取值: %+v err=%v", creds3, err)
	}

	if _, err := credentialsFrom(testAppSettings("", nil)); !errors.Is(err, service.ErrNoSettings) {
		t.Fatalf("全空应 ErrNoSettings, got %v", err)
	}
}

// 插槽键优先于 legacy 键：ak:legacy:* 一旦写入即生效（迁移完成后）。
func TestLegacySlotPrefersNewKeys(t *testing.T) {
	pCtx := testAppSettings(
		`{"akList":[{"slot":"legacy","label":"默认"}]}`,
		map[string]string{
			SecureKeyID("legacy"): "LTAI-new", SecureKeySecret("legacy"): "sk-new",
			"accessKeyId": "LTAI-old", "accessKeySecret": "sk-old",
		},
	)
	creds, err := credentialsFrom(pCtx)
	if err != nil || len(creds) != 1 || creds[0].AccessKeyID != "LTAI-new" {
		t.Fatalf("新键应优先于 legacy 键: %+v err=%v", creds, err)
	}
}

// 读取侧上限兜底：设置保存不经插件 handler，51 对在读时拒绝。
func TestCredentialsFromOverLimit(t *testing.T) {
	entries := make([]string, 0, 51)
	secure := map[string]string{}
	for i := 0; i < 51; i++ {
		slot := fmt.Sprintf("s%02d", i)
		entries = append(entries, fmt.Sprintf(`{"slot":%q,"label":%q}`, slot, slot))
		secure[SecureKeyID(slot)] = "LTAI" + slot
		secure[SecureKeySecret(slot)] = "sk" + slot
	}
	pCtx := testAppSettings(`{"akList":[`+strings.Join(entries, ",")+`]}`, secure)
	if _, err := credentialsFrom(pCtx); !errors.Is(err, ErrTooManyAKPairs) {
		t.Fatalf("51 对应超上限, got %v", err)
	}
}

// handleAK 下发配置不全的插槽（UI 需要呈现待补全态），明文与密钥绝不出现在
// 响应里（full 仅 reveal 者可见 AK ID；Secret 任何人都不可见）。
func TestHandleAKIncludesIncompleteSlots(t *testing.T) {
	pCtx := testAppSettings(
		`{"akList":[{"slot":"s1","label":"全"},{"slot":"s2","label":"半"}]}`,
		map[string]string{SecureKeyID("s1"): "LTAI1", SecureKeySecret("s1"): "sk1", SecureKeyID("s2"): "LTAI2"},
	)
	a := &App{}
	pCtx.User = &backend.User{Login: "tester", Role: "Admin"}
	r := httptest.NewRequest(http.MethodGet, "/ecs/ak", nil).WithContext(backend.WithPluginContext(context.Background(), pCtx))
	w := httptest.NewRecorder()
	a.handleAK(w, r)
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatal(err)
	}
	pairs := m["pairs"].([]any)
	if len(pairs) != 2 {
		t.Fatalf("配置不全的插槽也应下发: %v", m)
	}
	full := pairs[0].(map[string]any)
	half := pairs[1].(map[string]any)
	if full["secretConfigured"] != true || half["secretConfigured"] != false {
		t.Fatalf("secretConfigured 标记不符: %v %v", full, half)
	}
	body, _ := json.Marshal(m)
	if strings.Contains(string(body), "sk1") {
		t.Fatalf("Secret 明文泄漏进响应: %s", body)
	}
}
