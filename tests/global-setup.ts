import { chromium } from '@playwright/test';

/*
 * Warm-up：Grafana 重启后，首个到达 /a/<id> 的导航可能抢在插件前端模块注册
 * 完成之前，渲染 "App not found" 页（本地 6 worker 并发首访时尤其容易触发，
 * 见 fixtures.ts 的 CDP 拓扑）。这里在全部测试前串行预热两个实例的 app
 * 路由，竞态就消掉了。实例不在（viewer 对照实例按需起停）则静默跳过。
 */

export default async function globalSetup() {
  const endpoint = process.env.PW_CDP_ENDPOINT ?? 'http://127.0.0.1:9222';
  const targets = [
    process.env.GRAFANA_BROWSER_URL || 'http://172.17.0.1:3000',
    process.env.GRAFANA_VIEWER_BROWSER_URL || 'http://172.17.0.1:3001',
  ];
  let browser;
  try {
    browser =
      endpoint === 'local' ? await chromium.launch() : await chromium.connectOverCDP(endpoint, { timeout: 30_000 });
  } catch {
    return; // 浏览器容器不在：交给各测试自身的 skip/fail 逻辑表达
  }
  const page = await browser.newPage();
  for (const base of targets) {
    for (let i = 0; i < 6; i++) {
      try {
        await page.goto(`${base}/a/local-ecs-app`, { timeout: 15_000 });
      } catch {
        break; // 实例不可达：不预热
      }
      if (
        !(await page
          .getByText('App not found')
          .isVisible()
          .catch(() => false))
      ) {
        break;
      }
      await page.waitForTimeout(2_000);
    }
  }
  await page.close();
  await browser.close();
}
