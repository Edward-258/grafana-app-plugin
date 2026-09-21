/*
 * 依官方 create-plugin 模板（templates/common/.config/playwright.config.ts）适配，差异点：
 * - baseURL（Node 侧：request fixture）用 localhost；浏览器侧地址见 tests/fixtures.ts，
 *   按项目以 browserBaseURL 注入。两侧地址都可用环境变量覆盖。
 * - 本项目 dev 实例均为匿名认证（:3000 匿名 Admin / :3001 匿名 Viewer，见 docker-compose*.yaml），
 *   无需模板的 auth 登录项目与 storageState，也天然不涉及任何凭证入库。
 * - 浏览器经 CDP 连容器（fixtures.ts 覆盖说明在 tests/fixtures.ts 头注），trace 关闭
 *   （CDP 连接下 tracing 不可用；失败截图与 error-context 仍有效）。
 * - viewer 项目跑 RBAC 拦截回归，指向 :3001 对照实例。
 */

import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 0,
  reporter: [['html', { open: 'never' }]],
  use: {
    baseURL: process.env.GRAFANA_URL || 'http://localhost:3000',
    viewport: { width: 1600, height: 1000 },
    trace: 'off',
  },
  projects: [
    {
      name: 'chromium',
      use: {
        ...devices['Desktop Chrome'],
        browserBaseURL: process.env.GRAFANA_BROWSER_URL || 'http://172.17.0.1:3000',
      },
      testIgnore: /rbac\.spec\.ts/,
    },
    {
      name: 'viewer',
      testMatch: /rbac\.spec\.ts/,
      use: {
        ...devices['Desktop Chrome'],
        baseURL: process.env.GRAFANA_VIEWER_URL || 'http://localhost:3001',
        browserBaseURL: process.env.GRAFANA_VIEWER_BROWSER_URL || 'http://172.17.0.1:3001',
      },
    },
  ],
});
