import { spawnSync } from 'node:child_process'

if (process.platform !== 'darwin') {
  console.error('Opening the iOS project requires macOS and Xcode. See docs/mobile.md.')
  process.exitCode = 1
} else {
  const result = spawnSync('open', [new URL('../ios/App/App.xcodeproj', import.meta.url).pathname], { stdio: 'inherit' })
  process.exitCode = result.status ?? 1
}
