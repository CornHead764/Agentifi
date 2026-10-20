import { readFile } from 'node:fs/promises'
import path from 'node:path'

const types = { '.html': 'text/html', '.js': 'text/javascript', '.css': 'text/css', '.svg': 'image/svg+xml', '.woff2': 'font/woff2', '.png': 'image/png', '.webmanifest': 'application/manifest+json' }

export async function serveAssets(page, bundle) {
  await page.route('http://agentifi.test/**', async (route) => {
    const pathname = new URL(route.request().url()).pathname
    if (pathname.startsWith('/api/')) return route.fallback()
    const root = pathname.startsWith('/node_modules/') ? '../../frontend/' : bundle
    const name = path.extname(pathname) ? `.${pathname}` : 'index.html'
    try {
      await route.fulfill({ body: await readFile(new URL(root + name, import.meta.url)), contentType: types[path.extname(name)] ?? 'application/octet-stream' })
    } catch {
      await route.fulfill({ status: 404, body: 'Missing asset' })
    }
  })
}
