import globals from 'globals'
import pluginVue from 'eslint-plugin-vue'
import tseslint from 'typescript-eslint'
import vueParser from 'vue-eslint-parser'

// The initial gate is deliberately narrow: obvious bug rules plus the two
// rules that expose the existing warning baseline. `npm run lint` caps
// warnings at the committed baseline so new violations fail without making
// the whole legacy tree an immediate error.
export default tseslint.config(
  { ignores: ['dist/**'] },
  tseslint.configs.base,
  pluginVue.configs['flat/base'],
  {
    files: ['**/*.ts'],
    languageOptions: {
      parser: tseslint.parser,
      globals: { ...globals.browser, ...globals.es2021 },
    },
    rules: {
      'no-unused-vars': 'off',
      '@typescript-eslint/no-unused-vars': ['warn', {
        argsIgnorePattern: '^_',
        varsIgnorePattern: '^_',
      }],
      // TypeScript resolves type names (RequestInit, BufferSource, ...), so the
      // JS-only no-undef rule would only produce false positives.
      'no-undef': 'off',
      '@typescript-eslint/no-explicit-any': 'warn',
      'no-dupe-keys': 'error',
      'no-unreachable': 'error',
      'no-debugger': 'error',
    },
  },
  {
    files: ['**/*.vue'],
    languageOptions: {
      parser: vueParser,
      parserOptions: {
        parser: tseslint.parser,
        ecmaVersion: 'latest',
        sourceType: 'module',
        extraFileExtensions: ['.vue'],
      },
      globals: {
        ...globals.browser,
        ...globals.es2021,
        defineProps: 'readonly',
        defineEmits: 'readonly',
        defineExpose: 'readonly',
        withDefaults: 'readonly',
        ScrollBehavior: 'readonly',
      },
    },
    rules: {
      // eslint-plugin-vue v10 removed script-setup-uses-vars. Script bindings
      // used only in the template would all be false positives, so the TS
      // rule stays on .ts files and the template rule below covers <template>.
      'no-unused-vars': 'off',
      '@typescript-eslint/no-unused-vars': 'off',
      'no-undef': 'warn',
      '@typescript-eslint/no-explicit-any': 'warn',
      'no-dupe-keys': 'error',
      'no-unreachable': 'error',
      'no-debugger': 'error',
      'vue/no-unused-vars': 'warn',
      'vue/no-unused-components': 'warn',
    },
  },
)
