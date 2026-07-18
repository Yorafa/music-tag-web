# React + TypeScript + Vite

本模板提供一个最小可运行的 React + Vite + HMR 配置，并附带若干 ESLint 规则。

当前提供两种官方插件：

- [@vitejs/plugin-react](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react)（使用 [Oxc](https://oxc.rs)）
- [@vitejs/plugin-react-swc](https://github.com/vitejs/vite-plugin-react/blob/main/packages/plugin-react-swc)（使用 [SWC](https://swc.rs/)）

## React Compiler

由于 React Compiler 对开发与构建性能影响较大，本模板默认**未启用**。如需开启，请参考 [官方文档](https://react.dev/learn/react-compiler/installation)。

## 扩展 ESLint 配置

如果是开发生产应用，推荐扩展配置以启用 type-aware lint 规则：

```js
export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      // Other configs...

      // 移除 tseslint.configs.recommended，并替换为下面这行
      tseslint.configs.recommendedTypeChecked,
      // 或者，如果你想更严格，可用这条
      tseslint.configs.strictTypeChecked,
      // 加上这条可以得到风格相关的 lint 规则
      tseslint.configs.stylisticTypeChecked,

      // Other configs...
    ],
    languageOptions: {
      parserOptions: {
        project: ['./tsconfig.node.json', './tsconfig.app.json'],
        tsconfigRootDir: import.meta.dirname,
      },
      // other options...
    },
  },
])
```

也可以安装 [eslint-plugin-react-x](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-x) 与 [eslint-plugin-react-dom](https://github.com/Rel1cx/eslint-react/tree/main/packages/plugins/eslint-plugin-react-dom) 来获得 React 专属 lint 规则：

```js
// eslint.config.js
import reactX from 'eslint-plugin-react-x'
import reactDom from 'eslint-plugin-react-dom'

export default defineConfig([
  globalIgnores(['dist']),
  {
    files: ['**/*.{ts,tsx}'],
    extends: [
      // Other configs...
      // 启用 React 相关 lint 规则
      reactX.configs['recommended-typescript'],
      // 启用 React DOM 相关 lint 规则
      reactDom.configs.recommended,
    ],
    languageOptions: {
      parserOptions: {
        project: ['./tsconfig.node.json', './tsconfig.app.json'],
        tsconfigRootDir: import.meta.dirname,
      },
      // other options...
    },
  },
])
```
