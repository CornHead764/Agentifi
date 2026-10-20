# Agentifi mobile

A Capacitor iOS shell around `../frontend`, using the official Swift package.
Setup, server selection, Linux checks and Mac/Xcode instructions are in
[docs/mobile.md](../docs/mobile.md).

```sh
npm run sync     # build the shared UI and copy it into the native project
npm run ios      # sync and open Xcode on macOS
npm test
npm run lint
npm run test:browser
```

Install frontend dependencies first (`cd ../frontend && npm ci`). The Xcode
project resolves native dependencies through Swift Package Manager. This
package has no npm dependencies of its own.
