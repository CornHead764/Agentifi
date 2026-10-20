import { build } from '../../frontend/node_modules/vite/dist/node/index.js'

await build({ configFile: new URL('../vite.config.mjs', import.meta.url).pathname })
