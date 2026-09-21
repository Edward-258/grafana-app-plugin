import { expect, test } from './fixtures';

// RBAC 回归：匿名 Viewer 对照实例（:3001，见 docker-compose.viewer.yaml）。
// 结论依据 docs/grafana-plugin-official-spec.md §8：
// - ecs:read（/ecs/ak 脱敏读取）对 Viewer 放行，但响应永不带 full 字段；
// - ecs:reveal（/ecs/test 连通测试）对 Viewer 403；
// - 匿名会话的前端页面守卫会把 /a/<id> 重定向回首页。
// 本 spec 不需要任何凭证，也绝不打印 AK 相关内容。

test.beforeAll(async ({ request }) => {
  const res = await request
    .get('/api/health')
    .then((r) => r.ok())
    .catch(() => false);
  test.skip(
    !res,
    'viewer 对照实例未运行：docker compose -f docker-compose.yaml -f docker-compose.viewer.yaml up -d grafana-viewer'
  );
});

test('匿名 Viewer 访问 /ecs/test（reveal）被 403 拦截', async ({ request }) => {
  const res = await request.post('/api/plugins/local-ecs-app/resources/ecs/test');
  expect(res.status()).toBe(403);
});

test('匿名 Viewer 的 /ecs/ak 响应永不携带完整 AK', async ({ request }) => {
  const res = await request.get('/api/plugins/local-ecs-app/resources/ecs/ak');
  expect(res.status()).toBe(200);
  const text = await res.text();
  expect(text).not.toContain('"full"');
});

test('匿名 Viewer 直达 /a/<id> 被前端守卫重定向回首页', async ({ goto, page }) => {
  await goto('/a/local-ecs-app');
  await page.waitForURL((url) => !url.pathname.startsWith('/a/'), { timeout: 15000 });
  expect(page.url()).not.toContain('/a/local-ecs-app');
});
