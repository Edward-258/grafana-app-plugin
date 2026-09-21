import { expect, test } from './fixtures';

// 红线回归（AGENTS.md #1）：代码 addLink() 与 plugin.json addedLinks[] 双登记的面板菜单扩展
// 必须真实出现在 dashboard 面板菜单的 Extensions 子菜单里，且点击能打开弹窗。
// 缺登记时 Grafana 静默拒绝注册（无 server 日志），只有这里能抓住。
// 选择器策略沿用 ~/.zcode/tools/pw-browser/rbac-panel-menu.js 的验证过的写法。

const DASH_UID = 'rbac-text';

test('面板菜单 Extensions 子菜单含「ECS 资产信息」且弹窗可开', async ({ goto, page, request }) => {
  // 自包含准备：无 dashboard 则经 API 创建 text 面板（无数据源依赖，避免无限查询）
  const probe = await request.get(`/api/dashboards/uid/${DASH_UID}`);
  if (!probe.ok()) {
    const created = await request.post('/api/dashboards/db', {
      data: {
        dashboard: {
          title: 'RBAC text panel',
          uid: DASH_UID,
          schemaVersion: 39,
          version: 0,
          refresh: false,
          time: { from: 'now-1h', to: 'now' },
          panels: [
            {
              id: 1,
              type: 'text',
              title: 'hello',
              gridPos: { x: 0, y: 0, w: 12, h: 6 },
              options: { mode: 'markdown', content: '# hi' },
            },
          ],
        },
        overwrite: true,
      },
    });
    expect(created.ok()).toBeTruthy();
  }

  await goto(`/d/${DASH_UID}/${DASH_UID}`);

  // hover 面板头 → 头部最后一个可见按钮即菜单按钮（Grafana 11.6 验证过的定位法）
  const header = page.locator('[data-testid="header-container"]').first();
  await header.waitFor();
  await header.hover();
  const menuButton = header.locator('button:visible').last();
  await menuButton.click();

  // 扩展挂在 Extensions 子菜单下
  const extensions = page.locator('[role="menu"] [role="menuitem"]', { hasText: 'Extensions' }).first();
  await expect(extensions).toBeVisible();
  await extensions.click();

  const leaf = page
    .locator('[role="menu"] [role="menuitem"]')
    .filter({ hasText: /^ECS 资产信息$/ })
    .first();
  await expect(leaf).toBeVisible();
  await leaf.click();

  const dialog = page.locator('[role="dialog"]').first();
  await expect(dialog).toBeVisible();
  await expect(dialog).toContainText('ECS 资产信息');
});
