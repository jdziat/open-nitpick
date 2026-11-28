import path from 'node:path'
import tailwindcss from '@tailwindcss/vite'
import react from '@vitejs/plugin-react'
import { defineConfig } from 'vitest/config'

// The server serves every built file from /ui/assets/ under the name Vite gives
// it, so each name carries a hash of its content and the server may cache it for
// good. internal/server/uibuild copies the files into the directory the Go
// binary embeds and writes the manifest that says which is which.
export default defineConfig({
  base: '/ui/assets/',
  plugins: [react(), tailwindcss()],
  resolve: { alias: { '@': path.resolve(import.meta.dirname, './src') } },
  test: { environment: 'jsdom', setupFiles: ['./src/test/setup.ts'], css: false },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
    cssCodeSplit: false,
    assetsInlineLimit: 0, // no data: URIs, which the page's policy does not allow
    modulePreload: { polyfill: false },
    rollupOptions: {
      input: { app: path.resolve(import.meta.dirname, 'src/main.tsx'), theme: path.resolve(import.meta.dirname, 'src/theme.ts') },
      output: {
        entryFileNames: '[name].[hash].js',
        chunkFileNames: 'chunk.[name].[hash].js',
        assetFileNames: (info) => (info.names.some((n) => n.endsWith('.css')) ? 'app.[hash][extname]' : 'asset.[name].[hash][extname]'),
      },
    },
  },
})
