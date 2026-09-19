#!/usr/bin/env node
// RBAC 常量生成器：src/plugin.json 是唯一源头（SSOT），Go / TS 两侧的手写副本全部由它生成。
//
//   输入：package.json 的 name（插件 ID）+ src/plugin.json 的 roles[]/grants（含 %PLUGIN_ID% 占位符）
//   输出：pkg/app/handler/zz_generated.go（PluginID、actionRead/Reveal/Write、roleActions 回退映射）
//         src/permissions.gen.ts（ACTION_READ/REVEAL/WRITE）
//
// 触发：npm run build 前置执行，或手动 node scripts/gen-permissions.js。
// 守护：pkg/app/handler/zz_generated_test.go 独立重推导比对，改 plugin.json 忘记重新生成会红。
//
// 新增 action 时必须在下方 SEMANTIC 登记语义名——故意制造摩擦，让权限点的增加是显式决定。

const fs = require('fs');
const path = require('path');

const SEMANTIC = { Read: 'ecs:read', Reveal: 'ecs:reveal', Write: 'ecs:write' };

const root = path.join(__dirname, '..');
const pkg = JSON.parse(fs.readFileSync(path.join(root, 'package.json'), 'utf8'));
const pluginId = pkg.name;
if (!pluginId) throw new Error('package.json 缺少 name，无法确定插件 ID');

const raw = fs.readFileSync(path.join(root, 'src/plugin.json'), 'utf8');
const meta = JSON.parse(raw.replace(/%PLUGIN_ID%/g, pluginId));

const bySuffix = Object.fromEntries(Object.entries(SEMANTIC).map(([name, suffix]) => [suffix, name]));

// 从 roles[].permissions[].action × grants 反推出 基础角色 → 语义名集合
const granted = {}; // e.g. { Admin: ['Read','Reveal','Write'], ... }
const declared = [];
for (const r of meta.roles || []) {
  const perms = (r.role && r.role.permissions) || [];
  for (const p of perms) {
    const full = p.action || '';
    declared.push(full);
    if (!full.startsWith(pluginId + '.')) {
      throw new Error(`action 未以插件 ID 开头: "${full}"`);
    }
    const sem = bySuffix[full.slice(pluginId.length + 1)];
    if (!sem) {
      throw new Error(
        `未知 action "${full}"：请先在 scripts/gen-permissions.js 的 SEMANTIC 登记新 action 后再重新生成`
      );
    }
    for (const g of r.grants || []) {
      (granted[g] = granted[g] || new Set()).add(sem);
    }
  }
}
if (declared.length === 0) throw new Error('src/plugin.json 的 roles[] 里没有任何 action，无法生成');

const sortedRoles = Object.keys(granted).sort();
const semNamesOf = (role) => [...granted[role]].sort();

// ---- 生成 Go（输出按 gofmt 风格预对齐，生成后无需再跑 gofmt）----
// gofmt 的对齐组以空行分隔：PluginID 单独成组（不填充），action 常量一组，
// map 键的对齐宽度需把冒号计入（最长键 + 至少一个空格）。
const pad = (s, w) => s + ' '.repeat(Math.max(1, w - s.length));
const actionNames = Object.keys(SEMANTIC).map((n) => `action${n}`);
const actionWidth = Math.max(...actionNames.map((n) => n.length)) + 1;
const roleWidth = Math.max(...sortedRoles.map((r) => JSON.stringify(r).length + 1)) + 1;

const goLines = [];
goLines.push('// 由 scripts/gen-permissions.js 从 src/plugin.json 生成，请勿手改。');
goLines.push('// 改 roles[]/grants 后运行 npm run build 重新生成；');
goLines.push('// zz_generated_test.go 会独立重推导并守护一致性。');
goLines.push('');
goLines.push('package handler');
goLines.push('');
goLines.push('const (');
goLines.push(`\tPluginID = ${JSON.stringify(pluginId)}`);
goLines.push('');
for (const [name, suffix] of Object.entries(SEMANTIC)) {
  goLines.push(`\t${pad(`action${name}`, actionWidth)}= ${JSON.stringify(`${pluginId}.${suffix}`)}`);
}
goLines.push(')');
goLines.push('');
goLines.push('// roleActions 是无 id token 时的回退映射，与 plugin.json 各角色的 grants 等价（生成）。');
goLines.push('var roleActions = map[string]map[string]bool{');
for (const role of sortedRoles) {
  const entries = semNamesOf(role).map((n) => `action${n}: true`);
  goLines.push(`\t${pad(JSON.stringify(role) + ':', roleWidth)}{${entries.join(', ')}},`);
}
goLines.push('}');
fs.writeFileSync(path.join(root, 'pkg/app/handler/zz_generated.go'), goLines.join('\n') + '\n');

// ---- 生成 TS ----
const tsLines = [];
tsLines.push('// 由 scripts/gen-permissions.js 从 src/plugin.json 生成，请勿手改。');
tsLines.push('// 改 roles[]/grants 后运行 npm run build 重新生成。');
for (const [name, suffix] of Object.entries(SEMANTIC)) {
  tsLines.push(`export const ACTION_${name.toUpperCase()} = ${JSON.stringify(`${pluginId}.${suffix}`)};`);
}
fs.writeFileSync(path.join(root, 'src/permissions.gen.ts'), tsLines.join('\n') + '\n');

console.log(`已生成（插件 ID: ${pluginId}，声明 action: ${declared.length} 个）：`);
console.log('  pkg/app/handler/zz_generated.go');
console.log('  src/permissions.gen.ts');
