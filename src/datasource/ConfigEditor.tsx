import React from 'react';
import { DataSourcePluginOptionsEditorProps } from '@grafana/data';
import { Alert } from '@grafana/ui';

import { EcsDataSourceOptions } from './types';

type Props = DataSourcePluginOptionsEditorProps<EcsDataSourceOptions>;

export function ConfigEditor(_: Props) {
  return (
    <Alert title="凭证集中管理" severity="info">
      本数据源随「ECS 资产」插件捆绑分发，AccessKey 在 ECS 资产插件的配置页统一管理并自动同步到此处，无需重复填写。
    </Alert>
  );
}
