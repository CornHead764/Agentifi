import '../../frontend/src/styles/index.css'
import './mobile.css'
import { checkServer, normalizeServer, savedServer, saveServer } from './server.mjs'

const dialog = document.getElementById('server-settings')
const input = document.getElementById('server-url')
const error = document.getElementById('server-error')
const connect = document.getElementById('server-connect')
const cancel = document.getElementById('server-cancel')
let server = null
let busy = false

try {
  server = savedServer(localStorage)
} catch {
  showError('Allow this app to store its server address on this device, then try again.')
}

function showError(message) {
  error.textContent = message
  error.hidden = false
}

function openSettings() {
  if (dialog.open) return
  input.value = server ?? ''
  cancel.hidden = !server
  document.getElementById('server-change-note').hidden = !server
  dialog.showModal()
}

async function startApp() {
  window.agentifiApiBase = `${server}/api`
  try {
    // api.ts and session.ts must first evaluate after the server has been chosen.
    await import('../../frontend/src/main.tsx')
  } catch {
    showError('The app could not start. Close and reopen it, or choose another server.')
    openSettings()
  }
}

window.addEventListener('agentifi:server-settings', openSettings)
cancel.addEventListener('click', () => { if (!busy) dialog.close() })
dialog.addEventListener('cancel', (event) => {
  if (!server || busy) event.preventDefault()
})

document.getElementById('server-form').addEventListener('submit', async (event) => {
  event.preventDefault()
  if (busy) return
  busy = true
  connect.disabled = true
  cancel.disabled = true
  connect.textContent = 'Connecting…'
  error.hidden = true
  try {
    const next = normalizeServer(input.value)
    await checkServer(next)
    saveServer(localStorage, next)
    if (server) {
      // A full document reload discards in-flight requests and React Query caches.
      location.replace('/')
      return
    }
    server = next
    dialog.close()
    await startApp()
  } catch (failure) {
    showError(`${failure.message} Check the address, Wi-Fi connection and iOS Local Network permission.`)
  } finally {
    busy = false
    connect.disabled = false
    cancel.disabled = false
    connect.textContent = 'Connect'
  }
})

if (server) void startApp()
else openSettings()
