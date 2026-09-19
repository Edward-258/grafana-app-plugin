package handler

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

// 守护测试：编译进 zz_generated.go 的常量/映射必须与 src/plugin.json 的 roles[]/grants
// 保持一致。这里的推导逻辑与 scripts/gen-permissions.js 是两套独立实现，互相印证——
// 改了 plugin.json 却没跑 npm run build 时，本测试变红。
//
// 语义锚点（TestSemanticAnchors）是故意硬编码的权限设计，与生成器无关：
// 改策略（比如想给 Viewer 加 reveal）时必须连这里一起改，红得有理由。

const (
	semReadSuffix   = "ecs:read"
	semRevealSuffix = "ecs:reveal"
	semWriteSuffix  = "ecs:write"
)

type pluginMeta struct {
	Roles []struct {
		Role struct {
			Permissions []struct {
				Action string `json:"action"`
			} `json:"permissions"`
		} `json:"role"`
		Grants []string `json:"grants"`
	} `json:"roles"`
}

// deriveFromSources 独立重推导：package.json 的 name + src/plugin.json（含占位符替换）。
func deriveFromSources(t *testing.T) (string, map[string]map[string]bool) {
	t.Helper()
	root := "../../.."

	pkgRaw, err := os.ReadFile(root + "/package.json")
	if err != nil {
		t.Fatalf("读 package.json: %v", err)
	}
	var pkg struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(pkgRaw, &pkg); err != nil || pkg.Name == "" {
		t.Fatalf("解析 package.json name 失败: %v", err)
	}

	raw, err := os.ReadFile(root + "/src/plugin.json")
	if err != nil {
		t.Fatalf("读 src/plugin.json: %v", err)
	}
	var meta pluginMeta
	if err := json.Unmarshal([]byte(strings.ReplaceAll(string(raw), "%PLUGIN_ID%", pkg.Name)), &meta); err != nil {
		t.Fatalf("解析 src/plugin.json: %v", err)
	}

	granted := map[string]map[string]bool{}
	for _, r := range meta.Roles {
		for _, p := range r.Role.Permissions {
			full := p.Action
			if !strings.HasPrefix(full, pkg.Name+".") {
				t.Errorf("action 未以插件 ID 开头: %q", full)
				continue
			}
			suffix := full[len(pkg.Name)+1:]
			switch suffix {
			case semReadSuffix, semRevealSuffix, semWriteSuffix:
			default:
				t.Errorf("未知 action 后缀 %q：需要在生成器 SEMANTIC 与本测试同时登记", suffix)
			}
			for _, g := range r.Grants {
				if granted[g] == nil {
					granted[g] = map[string]bool{}
				}
				granted[g][full] = true
			}
		}
	}
	return pkg.Name, granted
}

func TestGeneratedMatchesPluginJSON(t *testing.T) {
	id, granted := deriveFromSources(t)

	if PluginID != id {
		t.Errorf("PluginID = %q, 源头是 %q（改了 package.json name 没重新生成？）", PluginID, id)
	}
	for suffix, got := range map[string]string{
		semReadSuffix:   actionRead,
		semRevealSuffix: actionReveal,
		semWriteSuffix:  actionWrite,
	} {
		if want := id + "." + suffix; got != want {
			t.Errorf("action 常量 = %q, 源头是 %q（改了 plugin.json 没跑 npm run build？）", got, want)
		}
	}
	if len(roleActions) != len(granted) {
		t.Errorf("roleActions 覆盖 %d 个基础角色, 源头是 %d 个", len(roleActions), len(granted))
	}
	for role, actions := range granted {
		got := roleActions[role]
		for a := range actions {
			if !got[a] {
				t.Errorf("roleActions[%q] 缺少 %q（plugin.json 改了 grants 没重新生成？）", role, a)
			}
		}
		for a := range got {
			if !actions[a] {
				t.Errorf("roleActions[%q] 多出 %q（plugin.json 撤销了该授予但没重新生成？）", role, a)
			}
		}
	}
}

// 语义锚点：权限设计本身，硬编码，独立于生成器。
func TestSemanticAnchors(t *testing.T) {
	cases := []struct {
		role                string
		read, reveal, write bool
	}{
		{"Viewer", true, false, false},
		{"Editor", true, true, false},
		{"Admin", true, true, true},
	}
	for _, tc := range cases {
		got := roleActions[tc.role]
		if got == nil {
			t.Fatalf("roleActions 缺少基础角色 %q", tc.role)
		}
		for action, want := range map[string]bool{actionRead: tc.read, actionReveal: tc.reveal, actionWrite: tc.write} {
			if got[action] != want {
				t.Errorf("%s 应%s持有 %s（当前=%v）", tc.role, boolWord(want), action, got[action])
			}
		}
	}
}

func boolWord(b bool) string {
	if b {
		return ""
	}
	return "不"
}

// TS 侧生成物同步检查：permissions.gen.ts 里必须能找到三个 action 字符串。
func TestTSPermissionsInSync(t *testing.T) {
	id, _ := deriveFromSources(t)
	raw, err := os.ReadFile("../../../src/permissions.gen.ts")
	if err != nil {
		t.Fatalf("读 src/permissions.gen.ts: %v", err)
	}
	ts := string(raw)
	for _, suffix := range []string{semReadSuffix, semRevealSuffix, semWriteSuffix} {
		if !strings.Contains(ts, `"`+id+"."+suffix+`"`) {
			t.Errorf("permissions.gen.ts 缺少 %q（改了 plugin.json 没跑 npm run build？）", id+"."+suffix)
		}
	}
}
