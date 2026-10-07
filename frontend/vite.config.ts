import react from '@vitejs/plugin-react'
import path from 'node:path'
import { defineConfig, loadEnv } from 'vite'

export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, __dirname, '')
  const backendUrl = env.BACKEND_URL || 'http://localhost:8000'

  return {
    plugins: [react()],
    resolve: {
      alias: {
        '@': path.resolve(__dirname, './src'),
      },
    },
    build: {
      rollupOptions: {
        output: {
          // Vendor code changes on a dependency bump, not on every deploy, so
          // it gets chunks of its own that stay cached across releases.
          manualChunks(id) {
            if (!id.includes('/node_modules/')) return undefined
            if (/\/node_modules\/(recharts|d3-[^/]+|victory-vendor)\//.test(id)) return 'charts'
            if (/\/node_modules\/(react|react-dom|scheduler|react-router|react-router-dom)\//.test(id)) {
              return 'react'
            }
            return 'vendor'
          },
        },
      },
    },
    server: {
      port: 5173,
      strictPort: true,
      // The Simplifi exporter is bundled from outside the project root, for
      // the import dialog to hand over.
      fs: {
        allow: ['.', path.resolve(__dirname, '../tools/extractors/extract-simplifi.js')],
      },
      proxy: {
        // No rewrite: the binary mounts the API at `/api` and serves only
        // `/health` at the root, so stripping the prefix here would ask the
        // server for `/accounts` and get the React app back with a 200.
        '/api': {
          target: backendUrl,
          changeOrigin: true,
        },
      },
    },
  }
})
