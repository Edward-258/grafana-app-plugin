import { expect, test } from './fixtures';

/*
 * 删除 AK 插槽回归（2026-10-06 实测踩坑）：@grafana/ui ConfirmModal 的确认按钮
 * type="submit" 且自带内层 <form>，若嵌在配置页 <form> 内，确认删除的 submit 会
 * 冒泡成隐式保存——setSlots 异步使 onSave 读到旧状态，保存出等价配置后
 * window.location.reload()，删除"看起来没生效"。修复 = ConfirmModal 移出 form。
 * 双断言：①确认后进入待删除态且页面不刷新（window 标记存活）；②保存后真正移除。
 * 注入两个假插槽、删除其一：CI 实例无真实 AK，若只注入一个，删除后 kept=0 会让
 * 保存按钮被 allValid 禁用（UI 不允许零插槽），用例必然卡死——两个保证 kept≥1。
 * 假 AK 值永不发起真实请求（删除链路只读写设置）；结束无论成败都把插槽表恢复
 * 原状，不污染实例上的真实配置。
 */

const PLUGIN = 'local-ecs-app';
const LABEL = 'e2e-删除回归';
const LABEL_KEEP = 'e2e-删除回归-陪跑';

test('删除 AK 插槽：确认后进入待删除态且不隐式刷新，保存后真正移除', async ({ goto, page, request }) => {
  const stamp = Date.now().toString(36);
  const slot = `e2e-${stamp}`;
  const keep = `e2e-keep-${stamp}`;

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
      .filter((p: { slot: string }) => p.slot !== slot && p.slot !== keep)
      .map((p: { slot: string; label: string }) => ({ slot: p.slot, label: p.label }));
    if (roster.length === baseline.length) {
      return; // 假插槽已全清（或从未写入），不动配置、避免无谓的插件重启
    }
    const settings = await (await request.get(`/api/plugins/${PLUGIN}/settings`)).json();
    await request.post(`/api/plugins/${PLUGIN}/settings`, {
      data: {
        enabled: settings.enabled ?? true,
        pinned: settings.pinned ?? true,
        jsonData: { ...settings.jsonData, akList: roster },
        secureJsonData: {
          [`ak:${slot}:id`]: '',
          [`ak:${slot}:secret`]: '',
          [`ak:${keep}:id`]: '',
          [`ak:${keep}:secret`]: '',
        },
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
        jsonData: {
          ...settings.jsonData,
          akList: [...baseline, { slot, label: LABEL }, { slot: keep, label: LABEL_KEEP }],
        },
        secureJsonData: {
          [`ak:${slot}:id`]: 'LTAI5tE2EFAKE0000delete',
          [`ak:${slot}:secret`]: 'e2e-fake-secret',
          [`ak:${keep}:id`]: 'LTAI5tE2E2FAKE0000keep',
          [`ak:${keep}:secret`]: 'e2e-fake-secret',
        },
      },
    });

    await goto('/a/local-ecs-app?tab=config');
    const box = page.getByTestId(`ak-slot-${slot}`);
    await expect(box).toBeVisible({ timeout: 20_000 });
    await expect(page.getByTestId(`ak-slot-${keep}`)).toBeVisible();

    // 确认删除只应改前端状态：弹窗确认后进入待删除态，页面不得刷新（标记存活）
    await page.evaluate(() => {
      (window as unknown as Record<string, unknown>).__e2eNoReload = Date.now();
    });
    await box.getByLabel('删除该 AK/SK').click();
    await page.getByRole('button', { name: '删除', exact: true }).click();
    await expect(page.getByText(`将删除「${LABEL}」——保存后生效`)).toBeVisible();
    await expect(page.getByRole('button', { name: '撤销' })).toBeVisible();
    expect(await page.evaluate(() => (window as unknown as Record<string, unknown>).__e2eNoReload)).toBeTruthy();

    // 保存后 reload：目标插槽从页面与后端插槽表消失，陪跑插槽保留（kept≥1 的存证）
    await page.getByRole('button', { name: '保存', exact: true }).click();
    await expect(box).toHaveCount(0, { timeout: 30_000 });
    const rosterSlots = async () => {
      const res = await request.get(`/api/plugins/${PLUGIN}/resources/ecs/ak`);
      if (!res.ok()) {
        return ['<restarting>'];
      }
      const info = await res.json();
      return (info.pairs || []).map((p: { slot: string }) => p.slot);
    };
    await expect.poll(rosterSlots, { timeout: 45_000 }).not.toContain(slot);
    await expect
      .poll(rosterSlots, { timeout: 10_000 })
      .toEqual(expect.arrayContaining([...baseline.map((b) => b.slot), keep]));
  } finally {
    await restore();
  }
});
