package handler

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"local-ecs-app/pkg/app/service"
)

func TestValidateIdentities(t *testing.T) {
	cases := []struct {
		name    string
		count   int
		wantErr bool
	}{
		{"空列表放行", 0, false},
		{"单条放行", 1, false},
		{"恰好上限放行", DefaultMaxIdentities, false},
		{"超一条拒绝", DefaultMaxIdentities + 1, true},
	}
	for _, tc := range cases {
		ids := make([]service.Identity, tc.count)
		err := ValidateIdentities(ids)
		if tc.wantErr && !errors.Is(err, ErrTooManyIdentities) {
			t.Errorf("%s: 期望 ErrTooManyIdentities, got %v", tc.name, err)
		}
		if !tc.wantErr && err != nil {
			t.Errorf("%s: 不应报错, got %v", tc.name, err)
		}
	}
}

func TestDecodeBody(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	// 正常 JSON
	var got payload
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(`{"name":"ok"}`))
	if !decodeBody(w, r, &got) || got.Name != "ok" || w.Code != 200 {
		t.Errorf("正常解码失败: code=%d body=%v", w.Code, got)
	}

	// 坏格式 → 400
	w = httptest.NewRecorder()
	r = httptest.NewRequest(http.MethodPost, "/", strings.NewReader("not-json"))
	if decodeBody(w, r, &got) || w.Code != http.StatusBadRequest {
		t.Errorf("坏格式应 400, got %d", w.Code)
	}

	// 超 1MB → 413。用一个"合法 JSON 形状的超大字符串 token"：
	// 解码器必须流式读完整个 token，途中才会撞上 MaxBytesReader。
	w = httptest.NewRecorder()
	big := append(append([]byte{'"'}, bytes.Repeat([]byte("a"), maxBodyBytes+1024)...), '"')
	r = httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(big))
	if decodeBody(w, r, &got) || w.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("超限应 413, got %d", w.Code)
	}
}

func TestFailMapsCallerErrorsTo400(t *testing.T) {
	w := httptest.NewRecorder()
	fail(w, ErrTooManyIdentities, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("ErrTooManyIdentities 应 400, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	fail(w, service.ErrNoSettings, nil)
	if w.Code != http.StatusBadRequest {
		t.Errorf("ErrNoSettings 应 400, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	fail(w, errors.New("阿里云 HTTP 500: x"), nil)
	if w.Code != http.StatusBadGateway {
		t.Errorf("上游错误应 502, got %d", w.Code)
	}
	var resp map[string]any
	_ = json.NewDecoder(w.Body).Decode(&resp)
	if resp["error"] == nil {
		t.Error("错误响应应包含 error 字段")
	}
}
