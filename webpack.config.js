const path = require('path');
const CopyWebpackPlugin = require('copy-webpack-plugin');
const pkg = require('./package.json');

const pluginId = pkg.name; // 插件 ID 唯一源头：package.json name（gen-permissions.js 同源）
const pluginIdDs = `${pluginId}-ds`; // 捆绑数据源插件 ID（src/datasource/plugin.json 同源）

const externals = [
  { 'amd-module': 'module' },
  'lodash',
  'jquery',
  'moment',
  'react',
  'react/jsx-runtime',
  'react/jsx-dev-runtime',
  'react-dom',
  'rxjs',
  'i18next',
  'react-router',
  '@emotion/css',
  '@emotion/react',
  /^@grafana\/ui/i,
  /^@grafana\/runtime/i,
  /^@grafana\/data/i,
];

const moduleRule = {
  test: /\.[tj]sx?$/,
  exclude: /node_modules/,
  use: {
    loader: 'swc-loader',
    options: {
      jsc: {
        target: 'es2015',
        parser: { syntax: 'typescript', tsx: true },
        transform: { react: { runtime: 'classic' } },
      },
    },
  },
};

const template = (content) =>
  content
    .toString()
    .replace(/%VERSION%/g, pkg.version)
    .replace(/%TODAY%/g, new Date().toISOString().slice(0, 10));

module.exports = (env = {}) => {
  const production = Boolean(env.production);
  const common = {
    mode: production ? 'production' : 'development',
    devtool: production ? 'source-map' : 'eval-source-map',
    externals,
    module: { rules: [moduleRule] },
    resolve: { extensions: ['.js', '.jsx', '.ts', '.tsx'] },
  };
  return [
    {
      ...common,
      context: path.join(__dirname, 'src'),
      entry: { module: './module.tsx' },
      output: {
        // datasource/ 是捆绑数据源的产物（另一个 webpack 配置写入），
        // clean 时必须保留，否则多配置并行 emit 会互相删除
        clean: { keep: /gpx_ecs|^datasource\// },
        filename: '[name].js',
        library: { type: 'amd' },
        path: path.join(__dirname, 'dist'),
        publicPath: `public/plugins/${pluginId}/`,
        uniqueName: pluginId,
      },
      plugins: [
        new CopyWebpackPlugin({
          patterns: [
            { from: 'img', to: 'img' },
            {
              from: 'plugin.json',
              to: 'plugin.json',
              transform: (content) => template(content).replace(/%PLUGIN_ID%/g, pluginId),
            },
            { from: path.join(__dirname, 'README.md'), to: 'README.md', noErrorOnMissing: true },
          ],
        }),
      ],
      watchOptions: {
        poll: 3000,
        ignored: /node_modules/,
      },
    },
    {
      ...common,
      context: path.join(__dirname, 'src/datasource'),
      entry: { module: './module.tsx' },
      output: {
        clean: true,
        filename: '[name].js',
        library: { type: 'amd' },
        path: path.join(__dirname, 'dist/datasource'),
        publicPath: `public/plugins/${pluginIdDs}/`,
        uniqueName: pluginIdDs,
      },
      plugins: [
        new CopyWebpackPlugin({
          patterns: [
            { from: path.join(__dirname, 'src/img'), to: 'img' },
            {
              from: 'plugin.json',
              to: 'plugin.json',
              transform: (content) => template(content).replace(/%PLUGIN_ID_DS%/g, pluginIdDs),
            },
          ],
        }),
      ],
    },
  ];
};
