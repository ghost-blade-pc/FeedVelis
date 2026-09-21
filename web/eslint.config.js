import js from '@eslint/js'
import pluginVue from 'eslint-plugin-vue'
import globals from 'globals'
import tseslint from 'typescript-eslint'

export default [
  { ignores: ['dist/**'] },
  js.configs.recommended,
  ...tseslint.configs.recommended,
  ...pluginVue.configs['flat/essential'],
  {
    files: ['src/**/*.ts', 'src/**/*.vue'],
    languageOptions: {
      parserOptions: { parser: tseslint.parser, ecmaVersion: 'latest', sourceType: 'module' },
      globals: globals.browser,
    },
    // content_html 只来自后端白名单清洗器；这里的 v-html 是受控阅读/预览契约。
    rules: { 'vue/no-v-html': 'off' },
  },
  {
    files: ['src/**/*.test.ts'],
    languageOptions: { globals: globals.vitest },
  },
]
