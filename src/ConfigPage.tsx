import React, { ChangeEvent, useEffect, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { css } from '@emotion/css';
import { AppPluginMeta, GrafanaTheme2, PluginConfigPageProps, PluginMeta } from '@grafana/data';
import { DataSourcePicker, getBackendSrv, hasPermission } from '@grafana/runtime';
import {
  Alert,
  Button,
  ConfirmModal,
  Field,
  FieldSet,
  IconButton,
  Input,
  LoadingPlaceholder,
  SecretInput,
  useStyles2,
} from '@grafana/ui';
import pluginJson from './plugin.json';
import { ACTION_REVEAL, ACTION_WRITE } from './permissions.gen';
import { queryPrometheus } from './prom';

export type AppSettings = {
  accessKeyId?: string; // 最老格式残留（jsonData 明文），迁入插槽后消失
  prometheusUid?: string;
  instanceLabel?: string;
  // AK 插槽表：slot 为前端生成的随机 uuid（后端键名 ak:<slot>:* 的主键），
  // label 用户可读。任何位置都不放 AK 明文（红线4）。
  akList?: Array<{ slot: string; label: string }>;
};

// 捆绑告警数据源：ID 与 webpack（pkg.name + "-ds"）、Go 侧（handler.PluginID + "-ds"）同源
const dsPluginType = `${pluginJson.id}-ds`;
// 与后端 guardrails.maxAKPairs 对齐
const MAX_AK_PAIRS = 50;
// 后端 credentialsFrom 里 legacy 单对插槽的固定主键
const LEGACY_SLOT = 'legacy';

type PluginSettings = {
  enabled?: boolean;
  pinned?: boolean;
  jsonData?: AppSettings;
  secureJsonFields?: Record<string, boolean>;
};

// /ecs/ak 响应：pairs 为全部插槽（含配置不全的）；full 仅 reveal 者按对下发
type AKPair = {
  slot: string;
  label: string;
  masked: string;
  akConfigured: boolean;
  secretConfigured: boolean;
  legacy?: boolean;
  full?: string;
};

type AKInfo = {
  configured?: boolean;
  canReveal?: boolean;
  pairs?: AKPair[];
};

// 插槽编辑态：pendingID/pendingSecret 为待保存值，只发被修改的键（红线4）
type SlotState = {
  slot: string;
  label: string;
  masked: string;
  full: string;
  akConfigured: boolean;
  secretConfigured: boolean;
  legacy: boolean;
  isNew: boolean;
  replacing: boolean;
  secretRevoked: boolean;
  pendingID: string;
  pendingSecret: string;
  showFull: boolean;
  removing: boolean;
};

type TestRow = { ak: string; akLabel?: string; count: number; regions: number; error?: string };

type Props = Partial<PluginConfigPageProps<AppPluginMeta<AppSettings>>>;

const toSlot = (p: AKPair): SlotState => ({
  slot: p.slot,
  label: p.label || '',
  masked: p.masked || '',
  full: p.full || '',
  akConfigured: p.akConfigured,
  secretConfigured: p.secretConfigured,
  legacy: Boolean(p.legacy),
  isNew: false,
  replacing: false,
  secretRevoked: false,
  pendingID: '',
  pendingSecret: '',
  showFull: false,
  removing: false,
});

// 校验：ID 在（已配置且未更换）或（填了新值）二者居其一即成立
const slotIDOK = (s: SlotState) => (s.akConfigured && !s.replacing ? true : s.pendingID.trim() !== '');
// Secret 在「从未配置 / 更换 / 已撤销 / 撤销过」时必须重填，否则可选
const slotNeedsSecret = (s: SlotState) => s.isNew || s.replacing || s.secretRevoked || !s.secretConfigured;
const slotSecretOK = (s: SlotState) => (slotNeedsSecret(s) ? s.pendingSecret !== '' : true);
const slotValid = (s: SlotState) => slotIDOK(s) && slotSecretOK(s);

export default function ConfigPage(_props: Props = {}) {
  const styles = useStyles2(getStyles);
  const canWrite = hasPermission(ACTION_WRITE) === true;
  const canReveal = hasPermission(ACTION_REVEAL) === true;
  const [ready, setReady] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [pinned, setPinned] = useState(true);
  const [slots, setSlots] = useState<SlotState[]>([]);
  const [prometheusUid, setPrometheusUid] = useState('');
  const [instanceLabel, setInstanceLabel] = useState('instance');
  // 捆绑告警数据源（凭证同步目标）：uid 为 null 表示实例不存在（provisioning 未生效）
  const [dsUid, setDsUid] = useState<string | null>(null);
  const [dsCredsReady, setDsCredsReady] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);

  useEffect(() => {
    (async () => {
      try {
        const [settings, akInfo] = await Promise.all([
          getBackendSrv().get<PluginSettings>(`/api/plugins/${pluginJson.id}/settings`),
          getBackendSrv().get<AKInfo>(`/api/plugins/${pluginJson.id}/resources/ecs/ak`),
        ]);
        setEnabled(settings.enabled ?? true);
        setPinned(settings.pinned ?? true);
        setPrometheusUid(settings.jsonData?.prometheusUid || '');
        setInstanceLabel(settings.jsonData?.instanceLabel || 'instance');
        setSlots((akInfo.pairs || []).map(toSlot));
        if (canWrite) {
          // 凭证同步目标探测：数据源列表仅 Admin 可读（与配置页写入门槛一致）
          const all =
            await getBackendSrv().get<Array<{ uid: string; type: string; secureJsonFields?: Record<string, boolean> }>>(
              '/api/datasources'
            );
          const ds = all.find((d) => d.type === dsPluginType);
          if (ds) {
            setDsUid(ds.uid);
            const fields = ds.secureJsonFields || {};
            setDsCredsReady(Object.keys(fields).some((k) => k.startsWith('ak:')) || Boolean(fields.accessKeySecret));
          }
        }
      } catch (e) {
        setError(e instanceof Error ? e.message : '读取配置失败');
      } finally {
        setReady(true);
      }
    })();
  }, [canWrite]);

  const kept = slots.filter((s) => !s.removing);
  const hasLegacy = kept.some((s) => s.legacy && !s.isNew);
  const allValid = kept.length > 0 && kept.every(slotValid);
  const atLimit = slots.length >= MAX_AK_PAIRS;

  const patch = (slot: string, changes: Partial<SlotState>) =>
    setSlots((prev) => prev.map((s) => (s.slot === slot ? { ...s, ...changes } : s)));

  const addSlot = () => {
    if (atLimit) {
      return;
    }
    setSlots((prev) => [
      ...prev,
      {
        slot: crypto.randomUUID(),
        label: '',
        masked: '',
        full: '',
        akConfigured: false,
        secretConfigured: false,
        legacy: false,
        isNew: true,
        replacing: false,
        secretRevoked: false,
        pendingID: '',
        pendingSecret: '',
        showFull: false,
        removing: false,
      },
    ]);
  };

  const requestDelete = (slot: string) => {
    const target = slots.find((s) => s.slot === slot);
    if (target?.isNew) {
      setSlots((prev) => prev.filter((s) => s.slot !== slot)); // 新插槽没有后端状态，直接移除
      return;
    }
    setConfirmDelete(slot);
  };

  const onSave = async () => {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      const secure: Record<string, string> = {};
      const akList: Array<{ slot: string; label: string }> = [];
      for (const s of slots) {
        if (s.removing) {
          // 已保存的插槽清键（空串覆盖），让同步把 ds 侧也清掉
          secure[`ak:${s.slot}:id`] = '';
          secure[`ak:${s.slot}:secret`] = '';
          continue;
        }
        akList.push({ slot: s.slot, label: s.label.trim() });
        if (s.pendingID.trim()) {
          secure[`ak:${s.slot}:id`] = s.pendingID.trim();
        }
        if (s.pendingSecret) {
          secure[`ak:${s.slot}:secret`] = s.pendingSecret;
        }
        // legacy 插槽的新键已配齐：旧键清空，ak:legacy:* 接管（迁移完成）
        if (s.slot === LEGACY_SLOT && secure[`ak:${s.slot}:id`] && secure[`ak:${s.slot}:secret`]) {
          secure.accessKeyId = '';
          secure.accessKeySecret = '';
        }
      }
      await updatePlugin(pluginJson.id, {
        enabled,
        pinned,
        jsonData: {
          prometheusUid,
          instanceLabel: instanceLabel.trim() || 'instance',
          akList,
        },
        secureJsonData: Object.keys(secure).length > 0 ? secure : undefined,
      });
      // 告警数据源的凭证由后端自动同步（保存会重启插件进程、触发同步），
      // 前端不搬运 Secret——本进程外的已存密文前端本就读不到。
      setMessage('已保存。Grafana 会重启插件后端进程并同步告警数据源。');
      window.location.reload();
    } catch (e) {
      setError(e instanceof Error ? e.message : '保存失败');
    } finally {
      setSaving(false);
    }
  };

  const onTest = async () => {
    setTesting(true);
    setError(null);
    setMessage(null);
    try {
      const parts: string[] = [];
      if (prometheusUid) {
        const prom = await queryPrometheus(prometheusUid, 'up');
        const n = prom.data?.result?.length ?? 0;
        if (prom.error) {
          throw new Error(`Prometheus: ${prom.error}`);
        }
        parts.push(`Prometheus 可见 ${n} 条 up 序列`);
      } else {
        parts.push('未选择 Prometheus 数据源');
      }
      const res = await getBackendSrv().post<{ ok: boolean; results?: TestRow[]; error?: string }>(
        `/api/plugins/${pluginJson.id}/resources/ecs/test`,
        {}
      );
      if (!res.results || res.results.length === 0) {
        throw new Error(res.error || '阿里云测试失败');
      }
      for (const row of res.results) {
        const name = row.akLabel || row.ak;
        parts.push(
          row.error ? `「${name}」失败：${row.error}` : `「${name}」${row.count} 台 ECS / ${row.regions} 地域`
        );
      }
      setMessage(parts.join('；'));
    } catch (e) {
      setError(e instanceof Error ? e.message : '测试失败，请先保存配置');
    } finally {
      setTesting(false);
    }
  };

  if (!ready) {
    return <LoadingPlaceholder text="读取配置..." />;
  }

  const renderSlot = (s: SlotState) => {
    const editingID = s.isNew || s.replacing || !s.akConfigured;
    const secretConfiguredNow = s.secretConfigured && !s.secretRevoked && !s.replacing;
    const displayName = s.akConfigured && s.showFull && s.full ? s.full : s.masked || '未配置';
    if (s.removing) {
      return (
        <div key={s.slot} className={`${styles.slotBox} ${styles.removing}`}>
          <div className={styles.slotRow}>
            <span>将删除「{s.label || s.masked}」——保存后生效，告警数据源会同步清除该凭证。</span>
            <div className={styles.slotActions}>
              <Button size="sm" variant="secondary" onClick={() => patch(s.slot, { removing: false })}>
                撤销
              </Button>
            </div>
          </div>
        </div>
      );
    }
    return (
      <div key={s.slot} className={styles.slotBox}>
        <div className={styles.slotRow}>
          <Input
            width={20}
            value={s.label}
            placeholder="标签（如：主账号）"
            aria-label="AK 标签"
            readOnly={!canWrite}
            onChange={(e: ChangeEvent<HTMLInputElement>) => patch(s.slot, { label: e.target.value })}
          />
          {editingID ? (
            <Input
              width={38}
              value={s.pendingID}
              placeholder={s.akConfigured ? '输入新的 AccessKey ID' : '输入 AccessKey ID'}
              onChange={(e: ChangeEvent<HTMLInputElement>) => patch(s.slot, { pendingID: e.target.value })}
            />
          ) : (
            <>
              <Input width={38} value={displayName} readOnly />
              {canReveal && s.full && (
                <IconButton
                  name={s.showFull ? 'eye-slash' : 'eye'}
                  tooltip={s.showFull ? '隐藏完整 AccessKey ID' : '查看完整 AccessKey ID'}
                  aria-label={s.showFull ? '隐藏完整 AccessKey ID' : '查看完整 AccessKey ID'}
                  type="button"
                  onClick={() => patch(s.slot, { showFull: !s.showFull })}
                />
              )}
              {canWrite && (
                <Button
                  size="sm"
                  variant="secondary"
                  type="button"
                  onClick={() => patch(s.slot, { replacing: true, pendingID: '', showFull: false })}
                >
                  更换
                </Button>
              )}
            </>
          )}
          {canWrite && (
            <div className={styles.slotActions}>
              {s.replacing && (
                <Button
                  size="sm"
                  variant="secondary"
                  type="button"
                  onClick={() => patch(s.slot, { replacing: false, pendingID: '' })}
                >
                  取消更换
                </Button>
              )}
              <IconButton
                name="trash-alt"
                tooltip="删除该 AK/SK"
                aria-label="删除该 AK/SK"
                type="button"
                onClick={() => requestDelete(s.slot)}
              />
            </div>
          )}
        </div>
        {canWrite && (
          <div className={styles.slotRow}>
            <SecretInput
              width={60}
              value={s.pendingSecret}
              isConfigured={secretConfiguredNow}
              placeholder={
                s.replacing
                  ? '更换 AK ID 后必须重输 Secret'
                  : secretConfiguredNow
                    ? '已配置（加密存储）'
                    : '输入 AccessKey Secret'
              }
              onChange={(e: ChangeEvent<HTMLInputElement>) => patch(s.slot, { pendingSecret: e.target.value })}
              onReset={() => patch(s.slot, { secretRevoked: true, pendingSecret: '' })}
            />
          </div>
        )}
        {!canWrite && (
          <div className={styles.slotRow}>
            <Input
              width={60}
              value={s.secretConfigured ? '已配置（加密存储）' : '未配置'}
              readOnly
              aria-label="AccessKey Secret 状态"
            />
          </div>
        )}
      </div>
    );
  };

  return (
    <form
      onSubmit={(e) => {
        e.preventDefault();
        void onSave();
      }}
    >
      <FieldSet label="Prometheus（对齐 Dashboard）">
        <Field
          label="Prometheus 数据源"
          description="用已有监控数据告诉插件「这张 dashboard 是哪台机器」。scrape target 不必填 IP。"
        >
          <DataSourcePicker
            type="prometheus"
            noDefault
            current={prometheusUid || null}
            onChange={(ds) => setPrometheusUid(ds.uid)}
            onClear={() => setPrometheusUid('')}
            width={60}
          />
        </Field>
        <Field
          label="实例 label"
          description="默认 instance。与 Node Exporter Full 上的 Instance 变量一致即可。"
          className={styles.gap}
        >
          <Input
            width={60}
            value={instanceLabel}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setInstanceLabel(e.target.value)}
          />
        </Field>
      </FieldSet>

      <FieldSet label="阿里云 ECS（只读，查 ID 与规格）" className={styles.gap}>
        <p className={styles.hint}>
          每对 AK/SK 一个插槽，查找时并发扫全部账号；AK ID 加密存储，Viewer 仅见脱敏值，展开需要 Editor 及以上。
        </p>
        {slots.map(renderSlot)}
        {canWrite && (
          <div className={styles.slotRow}>
            <Button
              type="button"
              variant="secondary"
              icon="plus"
              onClick={addSlot}
              disabled={atLimit}
              tooltip={atLimit ? `最多 ${MAX_AK_PAIRS} 对` : '新增一对 AK/SK 插槽'}
            >
              添加 AK/SK
            </Button>
            {atLimit && <span className={styles.muted}>已达 {MAX_AK_PAIRS} 对上限</span>}
          </div>
        )}
        {hasLegacy && canWrite && (
          <Alert title="旧格式凭证待迁移" severity="info">
            这对凭证仍走旧格式键读取（一切照常工作）。在对应插槽补填 Secret 并保存后，即迁移到加密插槽存储。
          </Alert>
        )}
        {canWrite && dsUid && !dsCredsReady && (
          <Alert title="告警数据源凭证待同步" severity="info">
            捆绑数据源「ECS 资产（告警）」尚未取得凭证，保存后插件后端会自动同步；若长期未生效请检查插件日志。
          </Alert>
        )}
        {canWrite && !dsUid && (
          <Alert title="告警数据源未实例化" severity="info">
            捆绑数据源「ECS 资产（告警）」（{dsPluginType}）尚未创建，告警功能不可用。通常由 provisioning/datasources
            自动预置，也可在「数据源 → 新建」手动选择本插件的数据源。
          </Alert>
        )}
        {message && (
          <Alert title="成功" severity="success">
            {message}
          </Alert>
        )}
        {error && (
          <Alert title="失败" severity="error">
            {error}
          </Alert>
        )}
        {canWrite && (
          <div className={styles.gap}>
            <Button type="submit" disabled={saving || !allValid}>
              保存
            </Button>
            {canReveal && (
              <Button
                type="button"
                variant="secondary"
                className={styles.btn}
                onClick={() => void onTest()}
                disabled={testing}
              >
                测试连接
              </Button>
            )}
          </div>
        )}
      </FieldSet>

      <ConfirmModal
        isOpen={confirmDelete !== null}
        title="删除该 AK/SK？"
        body="保存后生效：该账号将从资产对齐与告警查询中移除，告警数据源会同步清除其凭证。"
        confirmText="删除"
        icon="trash-alt"
        onConfirm={() => {
          patch(confirmDelete as string, { removing: true });
          setConfirmDelete(null);
        }}
        onDismiss={() => setConfirmDelete(null)}
      />
    </form>
  );
}

const updatePlugin = async (pluginId: string, data: Partial<PluginMeta<AppSettings>>) => {
  const response = await getBackendSrv().fetch({
    url: `/api/plugins/${pluginId}/settings`,
    method: 'POST',
    data,
  });
  return lastValueFrom(response);
};

const getStyles = (theme: GrafanaTheme2) => ({
  gap: css({ marginTop: theme.spacing(2) }),
  btn: css({ marginLeft: theme.spacing(1) }),
  muted: css({
    color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize,
    marginLeft: theme.spacing(2),
  }),
  hint: css({
    color: theme.colors.text.secondary,
    fontSize: theme.typography.bodySmall.fontSize,
    margin: `0 0 ${theme.spacing(1.5)}`,
  }),
  slotBox: css({
    border: `1px solid ${theme.colors.border.weak}`,
    borderRadius: theme.shape.radius.default,
    padding: theme.spacing(1.5),
    marginBottom: theme.spacing(1),
    display: 'grid',
    gap: theme.spacing(1),
  }),
  slotRow: css({
    display: 'flex',
    alignItems: 'center',
    gap: theme.spacing(1),
  }),
  slotActions: css({
    display: 'flex',
    alignItems: 'center',
    gap: theme.spacing(0.5),
    marginLeft: 'auto',
  }),
  removing: css({ opacity: 0.5 }),
});
