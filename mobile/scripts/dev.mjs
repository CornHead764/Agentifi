import { createServer } from '../../frontend/node_modules/vite/dist/node/index.js'

const server = await createServer({ configFile: new URL('../vite.config.mjs', import.meta.url).pathname })
await server.listen()
server.printUrls()
