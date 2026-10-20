import path from 'node:path'
import { fileURLToPath } from 'node:url'
import frontendConfig from '../frontend/vite.config.ts'

const root = path.dirname(fileURLToPath(import.meta.url))

export default async (env) => {
  const shared = await frontendConfig(env)
  return {
    ...shared,
    root,
    publicDir: path.join(root, '../frontend/public'),
    resolve: shared.resolve,
    server: {
      host: '127.0.0.1',
      port: 5177,
      strictPort: true,
      fs: { allow: [path.join(root, '..')] },
    },
    build: { ...shared.build, outDir: path.join(root, 'dist'), emptyOutDir: true },
  }
}
