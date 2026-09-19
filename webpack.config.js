const path = require('path');
const CopyWebpackPlugin = require('copy-webpack-plugin');
const pkg = require('./package.json');

const pluginId = pkg.name; // 插件 ID 唯一源头：package.json name（gen-permissions.js 同源）

module.exports = (env = {}) => {
  const production = Boolean(env.production);
  return {
    mode: production ? 'production' : 'development',
    devtool: production ? 'source-map' : 'eval-source-map',
    context: path.join(__dirname, 'src'),
    entry: { module: './module.tsx' },
    externals: [
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
    ],
    output: {
      clean: { keep: /gpx_ecs/ },
      filename: '[name].js',
      library: { type: 'amd' },
      path: path.join(__dirname, 'dist'),
      publicPath: `public/plugins/${pluginId}/`,
      uniqueName: pluginId,
    },
    module: {
      rules: [
        {
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
        },
      ],
    },
    resolve: {
      extensions: ['.js', '.jsx', '.ts', '.tsx'],
    },
    plugins: [
      new CopyWebpackPlugin({
        patterns: [
          { from: 'img', to: 'img' },
          {
            from: 'plugin.json',
            to: 'plugin.json',
            transform: (content) =>
              content
                .toString()
                .replace(/%VERSION%/g, pkg.version)
                .replace(/%TODAY%/g, new Date().toISOString().slice(0, 10))
                .replace(/%PLUGIN_ID%/g, pluginId),
          },
          { from: path.join(__dirname, 'README.md'), to: 'README.md', noErrorOnMissing: true },
        ],
      }),
    ],
    watchOptions: {
      poll: 3000,
      ignored: /node_modules/,
    },
  };
};
