import { expect, test } from './fixtures';

/*
 * 「告警数据源凭证待同步」横幅误报回归（2026-10-07）：配置页曾从 /api/datasources
 * 列表读 secureJsonFields，但列表接口根本不带该字段，凭证已同步也恒报待同步。
 * 修复 = 列表只取 uid，再查 /api/datasources/uid/:uid 详情。
 * 断言随实例真实状态双向成立：Node 侧按同一规则从详情接口推导期望值——
 * 本机实例已同步凭证 → 横幅必须不出现；CI 实例无 AK、sync 清空 ds → 横幅必须出现。
 */

const DS_UID = 'ecs-ds';
const BANNER = '告警数据源凭证待同步';

test('告警数据源凭证待同步横幅与数据源实际凭证状态一致', async ({ goto, page, request }) => {
  const res = await request.get(`/api/datasources/uid/${DS_UID}`);
  expect(res.ok()).toBeTruthy();
  const fields: Record<string, boolean> = (await res.json()).secureJsonFields || {};
  const credsReady = Object.keys(fields).some((k) => k.startsWith('ak:')) || Boolean(fields.accessKeySecret);

  await goto('/a/local-ecs-app?tab=config');
  // 表单在全部探测请求结束后才渲染（ready 门禁），按钮可见即横幅状态已定
  await expect(page.getByRole('button', { name: '添加 AK/SK' })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText(BANNER)).toHaveCount(credsReady ? 0 : 1);
});
