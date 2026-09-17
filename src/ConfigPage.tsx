import React, { ChangeEvent, useEffect, useState } from 'react';
import { lastValueFrom } from 'rxjs';
import { css } from '@emotion/css';
import { AppPluginMeta, GrafanaTheme2, PluginConfigPageProps, PluginMeta } from '@grafana/data';
import { DataSourcePicker, getBackendSrv } from '@grafana/runtime';
import { Alert, Button, Field, FieldSet, Input, LoadingPlaceholder, SecretInput, useStyles2 } from '@grafana/ui';
import pluginJson from './plugin.json';
import { queryPrometheus } from './prom';

export type AppSettings = {
  accessKeyId?: string;
  prometheusUid?: string;
  instanceLabel?: string;
};

type PluginSettings = {
  enabled?: boolean;
  pinned?: boolean;
  jsonData?: AppSettings;
  secureJsonFields?: Record<string, boolean>;
};

type Props = Partial<PluginConfigPageProps<AppPluginMeta<AppSettings>>>;

export default function ConfigPage(_props: Props = {}) {
  const styles = useStyles2(getStyles);
  const [ready, setReady] = useState(false);
  const [enabled, setEnabled] = useState(true);
  const [pinned, setPinned] = useState(true);
  const [accessKeyId, setAccessKeyId] = useState('');
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
        const settings = await getBackendSrv().get<PluginSettings>(`/api/plugins/${pluginJson.id}/settings`);
        setEnabled(settings.enabled ?? true);
        setPinned(settings.pinned ?? true);
        setAccessKeyId(settings.jsonData?.accessKeyId || '');
        setPrometheusUid(settings.jsonData?.prometheusUid || '');
        setInstanceLabel(settings.jsonData?.instanceLabel || 'instance');
        setSecretConfigured(Boolean(settings.secureJsonFields?.accessKeySecret));
      } catch (e) {
        setError(e instanceof Error ? e.message : '读取配置失败');
      } finally {
        setReady(true);
      }
    })();
  }, []);

  const onSave = async () => {
    setSaving(true);
    setError(null);
    setMessage(null);
    try {
      await updatePlugin(pluginJson.id, {
        enabled,
        pinned,
        jsonData: {
          accessKeyId: accessKeyId.trim(),
          prometheusUid,
          instanceLabel: instanceLabel.trim() || 'instance',
        },
        secureJsonData: secretConfigured ? undefined : { accessKeySecret },
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
          description="插件会自动枚举这把 AK 可见的全部地域，无需指定 Region"
          className={styles.gap}
        >
          <Input
            width={60}
            value={accessKeyId}
            onChange={(e: ChangeEvent<HTMLInputElement>) => setAccessKeyId(e.target.value)}
          />
        </Field>
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
        <div className={styles.gap}>
          <Button type="submit" disabled={saving || !accessKeyId || (!secretConfigured && !accessKeySecret)}>
            保存
          </Button>
          <Button type="button" variant="secondary" className={styles.btn} onClick={() => void onTest()} disabled={testing}>
            测试连接
          </Button>
        </div>
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
});
