/*
 * 依官方 create-plugin 模板（templates/common/.config/playwright.config.ts）适配，差异点：
 * - baseURL（Node 侧：request fixture）用 localhost；浏览器侧地址见 tests/fixtures.ts，
 *   按项目以 browserBaseURL 注入。两侧地址都可用环境变量覆盖。
 * - 本项目 dev 实例均为匿名认证（:3000 匿名 Admin / :3001 匿名 Viewer，见 docker-compose*.yaml），
 *   无需模板的 auth 登录项目与 storageState，也天然不涉及任何凭证入库。
 * - 浏览器经 CDP 连容器（fixtures.ts 覆盖说明在 tests/fixtures.ts 头注），trace 关闭
 *   （CDP 连接下 tracing 不可用；失败截图与 error-context 仍有效）。
 * - viewer 项目跑 RBAC 拦截回归，指向 :3001 对照实例。
 * - config-writes 项目放改写插件配置的用例（保存会让 app 实例重建、凭证同步进告警数据源），
 *   依赖 chromium 项目跑完再跑，避免与只读用例并发时读到中间态（ds 健康检查撞上假 AK 等）。
 *   单独调试这类用例时加 --no-deps 跳过前置项目。
 */

import { defineConfig, devices } from '@playwright/test';

export default defineConfig({
  testDir: './tests',
  globalSetup: './tests/global-setup.ts',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 2 : 1,
  // 本地所有 worker 共享一个 CDP 浏览器进程：worker 过多会把进程内存压崩
  //（Page crashed），Grafana 重启后并发首访还可能撞上插件模块加载失败
  //（"App not found"，global-setup.ts 预热消掉大半）。CI 每 worker 独立
  // launch 拓扑更稳，保持默认 workers 与 retries=2。
  workers: process.env.CI ? undefined : 4,
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
      testIgnore: [/rbac\.spec\.ts/, /configSlotDelete\.spec\.ts/],
    },
    {
      name: 'config-writes',
      testMatch: /configSlotDelete\.spec\.ts/,
      dependencies: ['chromium'],
      use: {
        ...devices['Desktop Chrome'],
        browserBaseURL: process.env.GRAFANA_BROWSER_URL || 'http://172.17.0.1:3000',
      },
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
