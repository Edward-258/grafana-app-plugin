package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
)

type dsPut struct {
	Name     string `json:"name"`
	JSONData struct {
		AKList []AKSlot `json:"akList"`
	} `json:"jsonData"`
	SecureJSONData map[string]string `json:"secureJsonData"`
}

// fakeGrafana 模拟数据源 API：ds 现存 s1、s2 两个插槽（legacy 为真时另有旧
// 格式键），记录 PUT 体。
func fakeGrafana(t *testing.T, legacy bool) (*httptest.Server, **dsPut) {
	fields := map[string]bool{
		SecureKeyID("s1"): true, SecureKeySecret("s1"): true,
		SecureKeyID("s2"): true, SecureKeySecret("s2"): true,
	}
	if legacy {
		fields["accessKeyId"], fields["accessKeySecret"] = true, true
	}
	var put *dsPut
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "GET /api/datasources":
			_ = json.NewEncoder(w).Encode([]map[string]string{{"uid": "prom", "type": "prometheus"}, {"uid": "ecs-ds", "type": AlertingDatasourceType}})
		case "GET /api/datasources/uid/ecs-ds":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"name":             "ECS",
				"jsonData":         map[string]any{"akList": []AKSlot{{Slot: "s1", Label: "主"}, {Slot: "s2", Label: "副"}}},
				"secureJsonFields": fields,
			})
		case "PUT /api/datasources/uid/ecs-ds":
			put = &dsPut{}
			if err := json.NewDecoder(r.Body).Decode(put); err != nil {
				t.Errorf("PUT 体解析失败: %v", err)
			}
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &put
}

// 整体覆写：插槽表与 label 不变、只换 AK 值也必须到达 ds（ds 侧比不出值变化，
// 不能幂等跳过）；删光插槽 ds 随之清空（含 legacy 键）；超限读失败则不碰 ds。
func TestSyncAlertingDatasource(t *testing.T) {
	overLimit, overSecure := make([]string, 0, 51), map[string]string{}
	for i := range 51 {
		slot := fmt.Sprintf("s%02d", i)
		overLimit = append(overLimit, fmt.Sprintf(`{"slot":%q}`, slot))
		overSecure[SecureKeyID(slot)], overSecure[SecureKeySecret(slot)] = "LTAI"+slot, "sk"+slot
	}
	rotated := map[string]string{
		SecureKeyID("s1"): "LTAI-new", SecureKeySecret("s1"): "sk-new",
		SecureKeyID("s2"): "LTAI2", SecureKeySecret("s2"): "sk2",
	}

	cases := []struct {
		name     string
		legacy   bool
		jsonData string
		secure   map[string]string
		wantErr  bool
		wantList []AKSlot
		wantSec  map[string]string
	}{
		{
			name:     "插槽内换AK",
			jsonData: `{"akList":[{"slot":"s1","label":"主"},{"slot":"s2","label":"副"}]}`,
			secure:   rotated,
			wantList: []AKSlot{{Slot: "s1", Label: "主"}, {Slot: "s2", Label: "副"}},
			wantSec:  rotated,
		},
		{
			name: "删光插槽", legacy: true, jsonData: `{"akList":[]}`, wantList: []AKSlot{},
			wantSec: map[string]string{
				SecureKeyID("s1"): "", SecureKeySecret("s1"): "",
				SecureKeyID("s2"): "", SecureKeySecret("s2"): "",
				"accessKeyId": "", "accessKeySecret": "",
			},
		},
		{name: "超限不动ds", jsonData: `{"akList":[` + strings.Join(overLimit, ",") + `]}`, secure: overSecure, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv, put := fakeGrafana(t, tc.legacy)
			settings := *testAppSettings(tc.jsonData, tc.secure).AppInstanceSettings
			err := (&App{}).syncAlertingDatasource(context.Background(), srv.URL+"/", "tok", settings)
			if tc.wantErr {
				if err == nil || *put != nil {
					t.Fatalf("应报错且不写 ds: err=%v put=%+v", err, *put)
				}
				return
			}
			if err != nil || *put == nil {
				t.Fatalf("应写 ds: err=%v", err)
			}
			got := **put
			if got.Name != "ECS" {
				t.Errorf("PUT 须回传完整主体, name=%q", got.Name)
			}
			if !reflect.DeepEqual(got.JSONData.AKList, tc.wantList) {
				t.Errorf("akList = %+v, want %+v", got.JSONData.AKList, tc.wantList)
			}
			if !reflect.DeepEqual(got.SecureJSONData, tc.wantSec) {
				t.Errorf("secureJsonData = %v, want %v", got.SecureJSONData, tc.wantSec)
			}
		})
	}
}
