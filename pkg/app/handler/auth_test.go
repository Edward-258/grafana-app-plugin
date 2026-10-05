package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/grafana/grafana-plugin-sdk-go/backend"
)

func ctxWithUser(role string, settings *backend.AppInstanceSettings) context.Context {
	pCtx := backend.PluginContext{AppInstanceSettings: settings}
	if role != "" {
		pCtx.User = &backend.User{Login: "tester", Role: role}
	}
	return backend.WithPluginContext(context.Background(), pCtx)
}

// 无 X-Grafana-Id（匿名 / 未开 externalServiceAccounts）时按 org 角色回退，
// 授予关系必须与 plugin.json roles[] 的 grants 一致。
func TestRequireActionRoleFallback(t *testing.T) {
	cases := []struct {
		role   string // "" 表示无用户信息（服务端发起）
		action string
		want   int
	}{
		{"", actionRead, http.StatusForbidden},
		{"", actionReveal, http.StatusForbidden},
		{"", actionWrite, http.StatusForbidden},
		{"Viewer", actionRead, http.StatusOK},
		{"Viewer", actionReveal, http.StatusForbidden},
		{"Viewer", actionWrite, http.StatusForbidden},
		{"Editor", actionRead, http.StatusOK},
		{"Editor", actionReveal, http.StatusOK},
		{"Editor", actionWrite, http.StatusForbidden},
		{"Admin", actionRead, http.StatusOK},
		{"Admin", actionReveal, http.StatusOK},
		{"Admin", actionWrite, http.StatusOK},
		{"Nobody", actionRead, http.StatusForbidden}, // 未知角色一律拒绝
	}
	a := &App{}
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	for _, tc := range cases {
		r := httptest.NewRequest(http.MethodGet, "/x", nil).WithContext(ctxWithUser(tc.role, nil))
		w := httptest.NewRecorder()
		a.requireAction(tc.action, ok)(w, r)
		if w.Code != tc.want {
			t.Errorf("role=%q action=%q: got %d want %d", tc.role, tc.action, w.Code, tc.want)
		}
	}
}

func TestMaskAccessKeyID(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"abc", "***"},
		{"abcde", "***"},
		{"abcdef", "ab…ef"},
		{"abcdefghi", "ab…hi"},
		{"abcdefghij", "abc…hij"},
		{"LTAI5tExampleKeyinA", "LTA…inA"},
	}
	for _, tc := range cases {
		if got := maskAccessKeyID(tc.in); got != tc.want {
			t.Errorf("maskAccessKeyID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestHandleAK(t *testing.T) {
	const akID = "LTAI5tExampleKeyinA"
	settings := &backend.AppInstanceSettings{
		DecryptedSecureJSONData: map[string]string{"accessKeyId": akID},
	}
	a := &App{}

	get := func(role string) map[string]any {
		r := httptest.NewRequest(http.MethodGet, "/ecs/ak", nil).WithContext(ctxWithUser(role, settings))
		w := httptest.NewRecorder()
		a.handleAK(w, r)
		var m map[string]any
		if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
			t.Fatalf("role=%q: decode: %v", role, err)
		}
		if w.Code != http.StatusOK {
			t.Fatalf("role=%q: status %d body=%v", role, w.Code, m)
		}
		return m
	}
	firstPair := func(m map[string]any) map[string]any {
		pairs, ok := m["pairs"].([]any)
		if !ok || len(pairs) != 1 {
			t.Fatalf("应恰有一对凭证: %v", m)
		}
		return pairs[0].(map[string]any)
	}

	if viewer := get("Viewer"); firstPair(viewer)["full"] != nil || viewer["canReveal"] != nil {
		t.Errorf("Viewer 不应拿到完整 AccessKey ID: %v", viewer)
	} else if firstPair(viewer)["masked"] != "LTA…inA" {
		t.Errorf("Viewer masked = %v, want LTA…inA", firstPair(viewer)["masked"])
	}
	if editor := get("Editor"); firstPair(editor)["full"] != akID || editor["canReveal"] != true {
		t.Errorf("Editor 应拿到完整 AccessKey ID: %v", editor)
	}
	if admin := get("Admin"); firstPair(admin)["full"] != akID {
		t.Errorf("Admin 应拿到完整 AccessKey ID: %v", admin)
	}
}

func TestHandleAKLegacy(t *testing.T) {
	settings := &backend.AppInstanceSettings{
		JSONData: []byte(`{"accessKeyId":"LEGACYKEY12345"}`),
	}
	a := &App{}
	r := httptest.NewRequest(http.MethodGet, "/ecs/ak", nil).WithContext(ctxWithUser("Admin", settings))
	w := httptest.NewRecorder()
	a.handleAK(w, r)
	var m map[string]any
	if err := json.NewDecoder(w.Body).Decode(&m); err != nil {
		t.Fatalf("decode: %v", err)
	}
	pairs, ok := m["pairs"].([]any)
	if !ok || len(pairs) != 1 {
		t.Fatalf("应恰有一对凭证: %v", m)
	}
	p := pairs[0].(map[string]any)
	if p["legacy"] != true {
		t.Errorf("应标记 legacy: %v", p)
	}
	if p["full"] != "LEGACYKEY12345" {
		t.Errorf("Admin 应拿到 legacy 完整值: %v", p)
	}
	if p["masked"] != "LEG…345" {
		t.Errorf("masked = %v, want LEG…345", p["masked"])
	}
}
