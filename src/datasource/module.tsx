import { DataSourcePlugin } from '@grafana/data';

import { ConfigEditor } from './ConfigEditor';
import { DataSource } from './datasource';
import { QueryEditor } from './QueryEditor';
import { EcsDataSourceOptions, EcsQuery } from './types';

export const plugin = new DataSourcePlugin<DataSource, EcsQuery, EcsDataSourceOptions>(DataSource)
  .setConfigEditor(ConfigEditor)
  .setQueryEditor(QueryEditor);
