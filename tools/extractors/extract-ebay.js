/*
 * Agentifi — eBay purchase history extractor
 * ==========================================
 *
 * Saves your eBay purchase history, as far back as eBay's purchases page
 * shows (about three years), as one JSON file Agentifi imports under
 * Settings → Merchants → eBay → Import orders.
 *
 * HOW TO RUN
 *   1. Sign in at https://www.ebay.com in your own browser.
 *   2. Open My eBay → Purchases (https://www.ebay.com/mye/myebay/purchase).
 *   3. Open the developer tools (F12) → Console.
 *   4. Paste this entire file and press Enter. It reads one page of purchases
 *      every couple of seconds and says how far it has got.
 *   5. A file named ebay-purchases-<date>.json downloads. Import it in
 *      Agentifi. The file is your purchase history: keep it out of shared
 *      folders and delete it once imported.
 *
 * WHY THIS EXISTS
 *   eBay's API, which Agentifi's daily update reads, answers for the last
 *   ninety days only. The purchases page in your own signed-in browser goes
 *   further back.
 *
 * WHAT IT READS
 *   The purchases list only: each seller order's number, date, total and
 *   status, which checkout it was paid in, and each item's number, title and
 *   quantity. That is enough to match your bank rows; tax, shipping and the
 *   payment method are on each order's page, which this does not open.
 *   Sellers' names are not kept.
 *
 * SAFETY
 *   Read-only, and only the list your purchases page itself asks eBay for. It
 *   sends nothing anywhere but eBay, and the file never leaves your computer
 *   until you import it.
 */

(async () => {
  const PAGE_LIMIT = 150;
  const PAUSE_MS = [1500, 3000];
  const ORDER_NUMBER = /\b(\d{2}-\d{5}-\d{5})\b/;

  if (!/(^|\.)ebay\.com$/.test(location.hostname) || !location.pathname.startsWith('/mye/myebay')) {
    console.error('Run this on your My eBay purchases page: https://www.ebay.com/mye/myebay/purchase');
    return;
  }

  const pause = () =>
    new Promise((done) => setTimeout(done, PAUSE_MS[0] + Math.random() * (PAUSE_MS[1] - PAUSE_MS[0])));

  // Every string under a value, depth first, in the page's own order.
  const strings = (value, out = []) => {
    if (typeof value === 'string') out.push(value);
    else if (Array.isArray(value)) value.forEach((one) => strings(one, out));
    else if (value && typeof value === 'object') Object.values(value).forEach((one) => strings(one, out));
    return out;
  };
  // The words a block shows, a line per text span.
  const words = (value, out = []) => {
    if (Array.isArray(value)) value.forEach((one) => words(one, out));
    else if (value && typeof value === 'object') {
      if (Array.isArray(value.textSpans)) out.push(value.textSpans.map((span) => span.text || '').join(''));
      else Object.values(value).forEach((one) => words(one, out));
    }
    return out;
  };

  const MONTHS = { jan: 1, feb: 2, mar: 3, apr: 4, may: 5, jun: 6, jul: 7, aug: 8, sep: 9, oct: 10, nov: 11, dec: 12 };
  const isoDay = (text) => {
    const found = /([A-Za-z]{3})[a-z]*\.? (\d{1,2}), (\d{4})/.exec(text || '');
    if (!found || !MONTHS[found[1].toLowerCase()]) return '';
    const pad = (n) => String(n).padStart(2, '0');
    return `${found[3]}-${pad(MONTHS[found[1].toLowerCase()])}-${pad(found[2])}`;
  };
  const money = (text) => {
    const found = /\$\s*([\d,]+\.\d{2})/.exec(text || '');
    return found ? found[1].replace(/,/g, '') : '';
  };

  // One purchase card: its seller order, the checkout it was paid in, and its
  // items. The card's lines read "Order date:", "Oct 09, 2026", "Order total:"…
  const readCard = (card) => {
    const lines = words(card.secondaryMessage || card);
    const after = (label) => {
      const at = lines.findIndex((line) => line.trim().toLowerCase().startsWith(label));
      if (at < 0) return '';
      const rest = lines[at].slice(lines[at].indexOf(':') + 1).trim();
      return rest || (lines[at + 1] || '');
    };
    const number = (ORDER_NUMBER.exec(after('order number')) || ORDER_NUMBER.exec(lines.join(' ')) || [])[1];
    const date = isoDay(after('order date'));
    if (!number || !date) return null;
    const checkout =
      strings(card)
        .map((one) => (/purchaseOrderId=([\d-]+)/.exec(one) || [])[1])
        .find(Boolean) || number;
    const status = words(card.primaryMessage || card.heading || [])[0] || '';
    const items = [];
    for (const item of card.itemCards || []) {
      const id = String(item.listingId || (/\/itm\/(\d{9,15})/.exec(strings(item).join(' ')) || [])[1] || '');
      if (!id) continue;
      const title = words(item.title || [])[0] || '';
      const quantity = Number(
        (/(?:qty|quantity)\s*:?\s*(\d+)/i.exec(words(item.aspectValuesList || []).join(' ')) || [])[1] || 1,
      );
      items.push({ item_id: id, title: title.trim(), quantity });
    }
    return { number, date, checkout, total: money(after('order total')), status: status.trim(), items };
  };

  const listUrl = (page) =>
    `/mye/myebay/ajax/v2/purchase/mp/get?page=${page}&modules=ALL_TRANSACTIONS&moduleId=122164` +
    '&pg=purchase&mp=purchase-module-v2&type=v2';

  const orders = new Map();
  for (let page = 1; page <= PAGE_LIMIT; page++) {
    const answer = await fetch(listUrl(page), { credentials: 'include', headers: { accept: 'application/json' } });
    if (!answer.ok) {
      console.error(`eBay answered page ${page} with ${answer.status}; stopping here and saving what was read.`);
      break;
    }
    const body = await answer.json();
    const cards = (((body.modules || {}).RIVER || [])[0] || {}).data?.items || [];
    if (cards.length === 0) break;
    let fresh = 0;
    for (const card of cards) {
      const one = readCard(card);
      if (one && !orders.has(one.number)) {
        orders.set(one.number, one);
        fresh++;
      }
    }
    console.log(`Page ${page}: ${fresh} orders (${orders.size} so far)`);
    if (fresh === 0) break;
    await pause();
  }

  // The orders paid together are one checkout, filed under its lowest seller
  // order number, as Agentifi's daily update files them.
  const checkouts = new Map();
  for (const one of orders.values()) {
    const list = checkouts.get(one.checkout) || [];
    list.push(one);
    checkouts.set(one.checkout, list);
  }
  const file = {
    source: 'agentifi-ebay-extract',
    extracted_at: new Date().toISOString(),
    account_hint: '',
    checkouts: [...checkouts.values()].map((sellers) => {
      const number = sellers.map((one) => one.number).sort()[0];
      const statuses = [...new Set(sellers.map((one) => one.status).filter(Boolean))];
      return {
        order_id: number,
        seller_orders: sellers.map((one) => ({ order_id: one.number, total: one.total })),
        date: sellers.map((one) => one.date).sort()[0],
        currency: 'USD',
        status: statuses.join(', '),
        url: `https://order.ebay.com/ord/show?orderId=${encodeURIComponent(number)}`,
        items: sellers.flatMap((one) =>
          one.items.map((item) => ({ ...item, url: `https://www.ebay.com/itm/${item.item_id}` })),
        ),
      };
    }),
  };

  if (file.checkouts.length === 0) {
    console.error('No purchases were read. eBay may have changed its purchases page; nothing was saved.');
    return;
  }
  const blob = new Blob([JSON.stringify(file, null, 2)], { type: 'application/json' });
  const link = document.createElement('a');
  link.href = URL.createObjectURL(blob);
  link.download = `ebay-purchases-${new Date().toISOString().slice(0, 10)}.json`;
  document.body.appendChild(link);
  link.click();
  link.remove();
  console.log(`Saved ${orders.size} orders in ${file.checkouts.length} checkouts to ${link.download}.`);
})();
