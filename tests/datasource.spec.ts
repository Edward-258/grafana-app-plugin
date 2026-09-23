import { expect, test } from './fixtures';

// 捆绑告警数据源（local-ecs-app-ds，uid ecs-ds 由 provisioning 预置）回归：
// - 插件注册、实例存在、后端健康端点可达（有 AK → OK；无 AK（CI）→ UNKNOWN）；
// - 资产 tab 的告警引导卡恒显（入口保障，见 App.tsx）；
// - 有凭证时 assets 帧为「宽序列」（time + 每实例一个 daysToExpire 字段），
//   account 帧携带 availableAmount——告警引擎靠这个形态做 reduce/threshold。
// 本 spec 不打印任何 AK 内容。

const dsUid = 'ecs-ds';

test('捆绑数据源实例存在且类型正确', async ({ request }) => {
  const res = await request.get(`/api/datasources/uid/${dsUid}`);
  expect(res.status()).toBe(200);
  const ds = await res.json();
  expect(ds.type).toBe('local-ecs-app-ds');
});

test('捆绑数据源后端健康端点可达（凭证齐备为 OK，未配置为 UNKNOWN）', async ({ request }) => {
  const res = await request.get(`/api/datasources/uid/${dsUid}/health`);
  // Grafana 约定：健康 OK → 200；UNKNOWN → 400（错误体里仍带 status 字段）
  expect([200, 400]).toContain(res.status());
  const health = await res.json();
  expect(['OK', 'UNKNOWN']).toContain(health.status);
});

test('资产 tab 渲染告警引导卡与创建入口', async ({ goto, page }) => {
  await goto('/a/local-ecs-app?tab=assets');
  await expect(page.getByText('接入 Grafana 告警')).toBeVisible({ timeout: 15000 });
  await expect(page.getByRole('button', { name: '创建告警规则' })).toBeVisible();
});

test('有凭证时 assets 帧为宽序列且 account 帧带余额字段', async ({ request }) => {
  const healthRes = await request.get(`/api/datasources/uid/${dsUid}/health`);
  const health = await healthRes.json().catch(() => ({ status: 'UNKNOWN' }));
  test.skip(health.status !== 'OK', '实例未配置 AK（CI 临时容器）：跳过数据帧断言');
  const query = (frame: string) =>
    request
      .post('/api/ds/query', {
        data: {
          from: 'now-1h',
          to: 'now',
          queries: [{ refId: 'A', datasource: { uid: dsUid, type: 'local-ecs-app-ds' }, frame }],
        },
      })
      .then((r) => r.json());
  const assets = await query('assets');
  const assetsFrame = assets.results.A.frames[0];
  const names = assetsFrame.schema.fields.map((f: { name: string }) => f.name);
  expect(names[0]).toBe('time');
  expect(names).toContain('daysToExpire');
  expect(assetsFrame.schema.fields[1].labels).toHaveProperty('instanceId');

  const account = await query('account');
  const accountNames = account.results.A.frames[0].schema.fields.map((f: { name: string }) => f.name);
  expect(accountNames).toContain('availableAmount');
});
