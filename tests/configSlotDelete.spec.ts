import { expect, test } from './fixtures';

/*
 * 删除 AK 插槽回归（2026-10-06 实测踩坑）：@grafana/ui ConfirmModal 的确认按钮
 * type="submit" 且自带内层 <form>，若嵌在配置页 <form> 内，确认删除的 submit 会
 * 冒泡成隐式保存——setSlots 异步使 onSave 读到旧状态，保存出等价配置后
 * window.location.reload()，删除"看起来没生效"。修复 = ConfirmModal 移出 form。
 * 双断言：①确认后进入待删除态且页面不刷新（window 标记存活）；②保存后真正移除。
 * 用例注入假插槽（假 AK 值，删除链路只读写设置不发真实请求），结束无论成败都
 * 把插槽表恢复原状，不污染实例上的真实配置。
 */

const PLUGIN = 'local-ecs-app';
const LABEL = 'e2e-删除回归';

test('删除 AK 插槽：确认后进入待删除态且不隐式刷新，保存后真正移除', async ({ goto, page, request }) => {
  const slot = `e2e-${Date.now().toString(36)}`;

  // 基线：以 /ecs/ak 的有效插槽表为准（覆盖 legacy 未迁移态），追加假插槽后写回
  const akRes = await request.get(`/api/plugins/${PLUGIN}/resources/ecs/ak`);
  expect(akRes.ok()).toBeTruthy();
  const akInfo = await akRes.json();
  const baseline: Array<{ slot: string; label: string }> = (akInfo.pairs || []).map(
    (p: { slot: string; label: string }) => ({ slot: p.slot, label: p.label })
  );
  const restore = async () => {
    const cur = await (await request.get(`/api/plugins/${PLUGIN}/resources/ecs/ak`)).json();
    const roster: Array<{ slot: string; label: string }> = (cur.pairs || [])
      .filter((p: { slot: string }) => p.slot !== slot)
      .map((p: { slot: string; label: string }) => ({ slot: p.slot, label: p.label }));
    if (roster.length === baseline.length) {
      return; // 假插槽已不在（删除成功或从未写入），不动配置、避免无谓的插件重启
    }
    const settings = await (await request.get(`/api/plugins/${PLUGIN}/settings`)).json();
    await request.post(`/api/plugins/${PLUGIN}/settings`, {
      data: {
        enabled: settings.enabled ?? true,
        pinned: settings.pinned ?? true,
        jsonData: { ...settings.jsonData, akList: roster },
        secureJsonData: { [`ak:${slot}:id`]: '', [`ak:${slot}:secret`]: '' },
      },
    });
  };

  try {
    const settings = await (await request.get(`/api/plugins/${PLUGIN}/settings`)).json();
    await request.post(`/api/plugins/${PLUGIN}/settings`, {
      data: {
        // enabled 缺省会被 Grafana 视为 false →「Cannot disable auto-enabled plugin」400
        enabled: settings.enabled ?? true,
        pinned: settings.pinned ?? true,
        jsonData: { ...settings.jsonData, akList: [...baseline, { slot, label: LABEL }] },
        secureJsonData: { [`ak:${slot}:id`]: 'LTAI5tE2EFAKE0000delete', [`ak:${slot}:secret`]: 'e2e-fake-secret' },
      },
    });

    await goto('/a/local-ecs-app?tab=config');
    const box = page.getByTestId(`ak-slot-${slot}`);
    await expect(box).toBeVisible({ timeout: 20_000 });

    // 确认删除只应改前端状态：弹窗确认后进入待删除态，页面不得刷新（标记存活）
    await page.evaluate(() => {
      (window as unknown as Record<string, unknown>).__e2eNoReload = Date.now();
    });
    await box.getByLabel('删除该 AK/SK').click();
    await page.getByRole('button', { name: '删除', exact: true }).click();
    await expect(page.getByText(`将删除「${LABEL}」——保存后生效`)).toBeVisible();
    await expect(page.getByRole('button', { name: '撤销' })).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__e2eNoReload)).toBeTruthy();

    // 保存后 reload，假插槽从页面与后端插槽表同时消失
    await page.getByRole('button', { name: '保存', exact: true }).click();
    await expect(box).toHaveCount(0, { timeout: 30_000 });
    await expect
      .poll(
        async () => {
          const res = await request.get(`/api/plugins/${PLUGIN}/resources/ecs/ak`);
          if (!res.ok()) {
            return ['<restarting>'];
          }
          const info = await res.json();
          return (info.pairs || []).map((p: { slot: string }) => p.slot);
        },
        { timeout: 45_000 }
      )
      .not.toContain(slot);
  } finally {
    await restore();
  }
});
