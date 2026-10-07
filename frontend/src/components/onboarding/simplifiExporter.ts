// The browser-console script that produces the export, bundled so the import
// dialog can hand it over. The Dockerfile copies it into the frontend stage and
// vite.config.ts allows it outside the project root; all three name this path.
import script from '../../../../tools/extractors/extract-simplifi.js?raw'

export const SIMPLIFI_EXPORTER = script
export const SIMPLIFI_EXPORTER_NAME = 'extract-simplifi.js'
