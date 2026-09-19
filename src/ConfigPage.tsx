import React, { ChangeEvent, useEffect, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { css } from '@emotion/css';
import { AppPluginMeta, GrafanaTheme2, PluginConfigPageProps, PluginMeta } from '@grafana/data';
import { DataSourcePicker, getBackendSrv, hasPermission } from '@grafana/runtime';
import { Alert, Button, Field, FieldSet, IconButton, Input, LoadingPlaceholder, SecretInput, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';
import { queryPrometheus } from './prom';

export type AppSettings = {
  accessKeyId?: string; // 旧格式残留（jsonData），Admin 保存一次后迁移到 secureJsonData
  prometheusUid?: string;
  instanceLabel?: string;
};

type PluginSettings = {
  enabled?: boolean;
  pinned?: boolean;
  jsonData?: AppSettings;
  secureJsonFields?: Record<string, boolean>;
};

// /ecs/ak 响应：full/canReveal 仅对持有 ecs:reveal（Editor/Admin）的调用者返回
type AKInfo = {
  configured?: boolean;
  masked?: string;
  legacy?: boolean;
  canReveal?: boolean;
  full?: string;
};

type Props = Partial<PluginConfigPageProps<AppPluginMeta<AppSettings>>>;

export default function ConfigPage(_props: Props = {}) {
  const styles = useStyles2(getStyles);
  const canWrite = hasPermission('local-ecs-app.ecs:write') === true;
  const canReveal = hasPermission('local-ecs-app.ecs:reveal') === true;
  const [ready, setReady] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [pinned, setPinned] = useState(true);
  const [ak, setAk] = useState<AKInfo>({});
  const [akLegacyValue, setAkLegacyValue] = useState('');
  const [akSecureConfigured, setAkSecureConfigured] = useState(false);
  const [showFullAK, setShowFullAK] = useState(false);
  const [replacingAK, setReplacingAK] = useState(false);
  const [newAccessKeyId, setNewAccessKeyId] = useState('');
  const [accessKeySecret, setAccessKeySecret] = useState('');
  const [secretConfigured, setSecretConfigured] = useState(false);
  const [prometheusUid, setPrometheusUid] = useState('');
  const [instanceLabel, setInstanceLabel] = useState('instance');
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [message, setMessage] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);

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
        setAkLegacyValue(settings.jsonData?.accessKeyId || '');
        setSecretConfigured(Boolean(settings.secureJsonFields?.accessKeySecret));
        setAkSecureConfigured(Boolean(settings.secureJsonFields?.accessKeyId));
        setAk(akInfo);
      } catch (e) {
        setError(e instanceof Error ? e.message : '读取配置失败');
      } finally {
        setReady(true);
      }
    })();
  }, []);

  const typedAK = newAccessKeyId.trim();
  const akConfigured = Boolean(ak.configured);
  const editingAK = canWrite && (replacingAK || !akConfigured);
  const displayAK = showFullAK && ak.full ? ak.full : ak.masked || '未配置';

  const onSave = async () => {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      const secure: Record<string, string> = {};
      if (typedAK) {
        secure.accessKeyId = typedAK;
      } else if (!akSecureConfigured && akLegacyValue) {
        // 旧格式（jsonData）迁移：保存时原值转入加密存储
        secure.accessKeyId = akLegacyValue;
      }
      if (!secretConfigured) {
        secure.accessKeySecret = accessKeySecret;
      }
      await updatePlugin(pluginJson.id, {
        enabled,
        pinned,
        jsonData: {
          prometheusUid,
          instanceLabel: instanceLabel.trim() || 'instance',
        },
        secureJsonData: Object.keys(secure).length > 0 ? secure : undefined,
      });
      setMessage('已保存。Grafana 会重启插件后端进程。');
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
      const res = await getBackendSrv().post<{ ok: boolean; count?: number; regions?: number; error?: string }>(
        `/api/plugins/${pluginJson.id}/resources/ecs/test`,
        {}
      );
      if (!res.ok) {
        throw new Error(res.error || '阿里云测试失败');
      }
      parts.push(`阿里云全部地域共 ${res.count ?? 0} 台 ECS，覆盖 ${res.regions ?? 0} 个地域`);
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
          <Input width={60} value={instanceLabel} onChange={(e: ChangeEvent<HTMLInputElement>) => setInstanceLabel(e.target.value)} />
        </Field>
      </FieldSet>

      <FieldSet label="阿里云 ECS（只读，查 ID 与规格）" className={styles.gap}>
        <Field
          label="AccessKey ID"
          description="已加密存储，Viewer 仅见脱敏值；展开完整值需要 Editor 及以上。"
          className={styles.gap}
        >
          {editingAK ? (
            <div className={styles.akRow}>
              <Input
                width={38}
                value={newAccessKeyId}
                onChange={(e: ChangeEvent<HTMLInputElement>) => setNewAccessKeyId(e.target.value)}
                placeholder={akConfigured ? '输入新的 AccessKey ID' : '输入 AccessKey ID'}
              />
              {akConfigured && (
                <Button
                  size="sm"
                  variant="secondary"
                  type="button"
                  onClick={() => {
                    setReplacingAK(false);
                    setNewAccessKeyId('');
                  }}
                >
                  取消
                </Button>
              )}
            </div>
          ) : (
            <div className={styles.akRow}>
              <Input width={38} value={displayAK} readOnly />
              {ak.full && (
                <IconButton
                  name={showFullAK ? 'eye-slash' : 'eye'}
                  tooltip={showFullAK ? '隐藏完整 AccessKey ID' : '查看完整 AccessKey ID'}
                  aria-label={showFullAK ? '隐藏完整 AccessKey ID' : '查看完整 AccessKey ID'}
                  type="button"
                  onClick={() => setShowFullAK((v) => !v)}
                />
              )}
              {canWrite && (
                <Button
                  size="sm"
                  variant="secondary"
                  type="button"
                  onClick={() => {
                    setReplacingAK(true);
                    setShowFullAK(false);
                  }}
                >
                  更换
                </Button>
              )}
            </div>
          )}
        </Field>
        {canWrite ? (
          <Field label="AccessKey Secret" description="保存后不会回显明文">
            <SecretInput
              width={60}
              value={accessKeySecret}
              isConfigured={secretConfigured}
              onChange={(e: ChangeEvent<HTMLInputElement>) => setAccessKeySecret(e.target.value)}
              onReset={() => {
                setAccessKeySecret('');
                setSecretConfigured(false);
              }}
            />
          </Field>
        ) : (
          <Field label="AccessKey Secret" description="仅 Admin 可修改">
            <Input width={60} value={secretConfigured ? '已配置（加密存储）' : '未配置'} readOnly />
          </Field>
        )}
        {ak.legacy && canWrite && (
          <Alert title="AccessKey ID 仍以旧格式保存" severity="info">
            点击「保存」即可把它迁移到加密存储（secureJsonData）。
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
            <Button
              type="submit"
              disabled={saving || !(akConfigured || typedAK) || (!secretConfigured && !accessKeySecret)}
            >
              保存
            </Button>
            {canReveal && (
              <Button type="button" variant="secondary" className={styles.btn} onClick={() => void onTest()} disabled={testing}>
                测试连接
              </Button>
            )}
          </div>
        )}
      </FieldSet>
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
  akRow: css({
    display: 'flex',
    alignItems: 'center',
    gap: theme.spacing(1),
  }),
});
