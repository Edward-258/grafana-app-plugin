import { chromium, expect, test as base, type Browser, type Page } from '@playwright/test';

/*
 * 浏览器跑在常驻 headless-shell 容器里（CDP :9222，见 AGENTS.md），宿主机（WSL）缺 chromium
 * 系统库无法本地 launch。因此地址拆成两侧：
 * - Node 侧（request fixture、reporter）：localhost，由 playwright.config 的 baseURL 提供；
 * - 浏览器侧（page.goto）：docker 网桥地址，经 goto fixture 的 browserBaseURL 提供，
 *   按项目在 playwright.config 里分别配置（主实例 / viewer 对照实例）。
 * 官方模板的 @grafana/plugin-e2e fixtures 强制两侧同源（其 selectors 启动时从 Node 侧拉取
 * Grafana 静态资源），与该拓扑冲突，故此处用纯 @playwright/test，spec 策略仍照模板组织。
 */

type BrowserGoto = {
  browserBaseURL: string;
  goto: (path: string) => ReturnType<Page['goto']>;
};

export const test = base.extend<BrowserGoto & { browser: Browser }>({
  // 覆盖默认 browser fixture：连常驻 CDP 容器而非本地 launch（端点可用 PW_CDP_ENDPOINT 覆盖）；
  // PW_CDP_ENDPOINT=local 时本地 launch——CI（.github/workflows/ci.yml）上 chromium 依赖齐全用这条路径。
  // teardown 的 close() 对 CDP 连接只断连、不杀容器浏览器；对本地 launch 则正常回收。
  browser: [
    async ({}, use) => {
      const endpoint = process.env.PW_CDP_ENDPOINT ?? 'http://127.0.0.1:9222';
      const browser =
        endpoint === 'local' ? await chromium.launch() : await chromium.connectOverCDP(endpoint, { timeout: 30_000 });
      await use(browser);
      await browser.close();
    },
    { scope: 'worker' },
  ],
  browserBaseURL: [process.env.GRAFANA_BROWSER_URL || 'http://172.17.0.1:3000', { option: true }],
  goto: [
    async ({ page, browserBaseURL }, use) => {
      await use((path: string) => page.goto(`${browserBaseURL}${path}`));
    },
    { scope: 'test' },
  ],
});

export { expect };
