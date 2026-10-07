/*
 * Agentifi — Simplifi dataset extractor
 * =====================================
 *
 * Dumps Simplifi's complete client-side dataset to a single JSON file.
 *
 * HOW TO RUN
 *   1. Log in to https://simplifi.quicken.com in Chrome.
 *   2. Let the app fully load. Visit Transactions, Net Worth, Spending Plan,
 *      Investments and Bills & Income once each so every store is hydrated.
 *      Stores are populated lazily — a page you never opened may be empty.
 *   3. Open DevTools (F12) → Console.
 *   4. Paste this entire file, press Enter.
 *   5. A file named simplifi-export-<datasetId>-<timestamp>.json downloads.
 *   6. Move it to data/simplifi/ in the checkout. data/ is gitignored: the
 *      file is real financial data and never enters the repository, not even
 *      as a trimmed or redacted fixture.
 *   7. Transaction rules are not in IndexedDB. Save them from the Network
 *      panel as data/simplifi/transaction-rules.json; docs/importing.md says
 *      how, and how to import both files.
 *
 * WHY THIS EXISTS
 *   Simplifi has no export API. The web app is offline-capable and keeps the
 *   whole dataset in IndexedDB, so this reads it back out. It depends on a live
 *   subscription — run it well before the subscription lapses.
 *
 * SAFETY
 *   Read-only. Opens IndexedDB in "readonly" transactions and touches nothing
 *   else. It does not call the Simplifi API.
 */

(async () => {
  const openDb = (name) =>
    new Promise((resolve, reject) => {
      const req = indexedDB.open(name);
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error);
    });

  const db = await openDb('localforage');
  const store = db.transaction('keyvaluepairs', 'readonly').objectStore('keyvaluepairs');

  const raw = await new Promise((resolve, reject) => {
    const acc = {};
    const req = store.openCursor();
    req.onerror = () => reject(req.error);
    req.onsuccess = (e) => {
      const cursor = e.target.result;
      if (!cursor) return resolve(acc);
      acc[cursor.key] = cursor.value;
      cursor.continue();
    };
  });

  // Keys look like "<userId>:<datasetId>:<storeName>".
  const SKIP = new Set(['authStore', 'entitlementsStore', 'simplifiCacheMeta']);

  const out = {
    exportedAt: new Date().toISOString(),
    appVersion: (window.__APP_VERSION__ || 'unknown'),
    source: 'indexeddb:localforage/keyvaluepairs',
    datasets: {},
    skipped: [],
  };

  for (const [key, value] of Object.entries(raw)) {
    const parts = String(key).split(':');
    const storeName = parts.pop();
    const datasetId = parts.pop() || 'unknown';

    // authStore holds bearer tokens — never write credentials to disk.
    if (SKIP.has(storeName)) {
      out.skipped.push(storeName);
      continue;
    }

    let parsed;
    try {
      parsed = JSON.parse(value);
    } catch (_) {
      parsed = { __unparsed: true, length: String(value || '').length };
    }

    out.datasets[datasetId] = out.datasets[datasetId] || {};
    // Unwrap the { version, data } envelope; keep version for provenance.
    out.datasets[datasetId][storeName] = {
      version: parsed && parsed.version,
      data: parsed && 'data' in parsed ? parsed.data : parsed,
    };
  }

  // ---- summary to the console so you can sanity-check before trusting it ----
  const summary = [];
  for (const [datasetId, stores] of Object.entries(out.datasets)) {
    summary.push(`dataset ${datasetId}`);
    for (const [name, wrapped] of Object.entries(stores)) {
      const d = wrapped.data;
      let n = '';
      if (d && d.resourcesById) n = `n=${Object.keys(d.resourcesById).length}`;
      else if (Array.isArray(d)) n = `n=${d.length}`;
      summary.push(`  ${name.padEnd(32)} ${n}`);
    }
  }
  console.log(summary.join('\n'));
  console.log('skipped (credentials):', out.skipped.join(', ') || 'none');

  const json = JSON.stringify(out, null, 1);
  console.log(`payload: ${(json.length / 1024 / 1024).toFixed(1)} MB`);

  const datasetId = Object.keys(out.datasets)[0] || 'unknown';
  const stamp = new Date().toISOString().slice(0, 19).replace(/[:T]/g, '');
  const blob = new Blob([json], { type: 'application/json' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  a.href = url;
  a.download = `simplifi-export-${datasetId}-${stamp}.json`;
  document.body.appendChild(a);
  a.click();
  a.remove();
  setTimeout(() => URL.revokeObjectURL(url), 10000);

  console.log('%cDownload started.', 'color:#0c585c;font-weight:bold');
  return `${Object.keys(out.datasets).length} dataset(s), ${json.length} bytes`;
})();
