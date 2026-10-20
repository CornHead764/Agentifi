import { cp, copyFile, mkdir, rm, stat } from 'node:fs/promises'

const root = new URL('../', import.meta.url)
const app = new URL('ios/App/App/', root)
await stat(new URL('dist/index.html', root))
await mkdir(app, { recursive: true })
await rm(new URL('public/', app), { recursive: true, force: true })
await cp(new URL('dist/', root), new URL('public/', app), { recursive: true })
await copyFile(new URL('capacitor.config.json', root), new URL('capacitor.config.json', app))
console.log('Copied shared UI and Capacitor configuration to the iOS app.')
