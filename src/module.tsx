import React from 'react';
import { AppPlugin, PluginExtensionPoints } from '@grafana/data';
import App from './App';
import ConfigPage, { AppSettings } from './ConfigPage';
import { EcsModalBody } from './EcsInfo';

export const plugin = new AppPlugin<AppSettings>()
  .setRootPage(App)
  .addConfigPage({
    title: '配置',
    icon: 'cog',
    body: ConfigPage,
    id: 'configuration',
  })
  .addLink({
    title: 'ECS 资产信息',
    description: '查看当前主机的 ECS ID 与规格',
    targets: [PluginExtensionPoints.DashboardPanelMenu],
    onClick: (_event, helpers) => {
      helpers.openModal({
        title: 'ECS 资产信息',
        width: 480,
        body: ({ onDismiss }) => <EcsModalBody onDismiss={onDismiss} />,
      });
    },
  });
