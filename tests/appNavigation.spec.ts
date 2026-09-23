import { expect, test } from './fixtures';

test('root page（默认配置 tab）渲染页面标题、tab 导航与配置表单', async ({ goto, page }) => {
  await goto('/a/local-ecs-app');

  await expect(page.getByRole('heading', { name: 'ECS 资产', level: 1 })).toBeVisible();
  // Grafana 11.6 把 pageNav 子项渲染为 tablist
  await expect(page.getByRole('tab', { name: '资产列表' })).toBeVisible();
  await expect(page.getByText('Prometheus 数据源')).toBeVisible();
});

test('资产列表 tab 渲染工具栏与刷新按钮', async ({ goto, page }) => {
  await goto('/a/local-ecs-app?tab=assets');

  await expect(page.getByPlaceholder('搜索 ECS ID / 名称 / 规格 / 地域')).toBeVisible();
  // 刷新按钮：点击沿原查询链路重跑（无数据源配置时落到错误提示，按钮本身常驻）。
  // 冷缓存 + e2e 并发时后台要真跑一次阿里云全地域扫描，启用可能超过 20s。
  const refresh = page.getByRole('button', { name: '刷新资产列表' });
  await expect(refresh).toBeVisible();
  await expect(refresh).toBeEnabled({ timeout: 45_000 });
});
