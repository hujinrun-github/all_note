import { fileURLToPath } from 'node:url'
import { dirname, resolve } from 'node:path'

const root = dirname(fileURLToPath(import.meta.url))
const nodeModules = resolve(root, '../../frontend/node_modules')

export default {
  root,
  base: './',
  resolve: {
    alias: {
      '@tiptap/core': resolve(nodeModules, '@tiptap/core'),
      '@tiptap/starter-kit': resolve(nodeModules, '@tiptap/starter-kit'),
      '@tiptap/extension-placeholder': resolve(
        nodeModules,
        '@tiptap/extension-placeholder'
      ),
      'tiptap-markdown': resolve(nodeModules, 'tiptap-markdown'),
    },
  },
  build: {
    outDir: resolve(root, '../FlowSpaceMac/Resources/RichEditor'),
    emptyOutDir: true,
    assetsDir: 'assets',
    rollupOptions: { input: resolve(root, 'index.html') },
  },
}
