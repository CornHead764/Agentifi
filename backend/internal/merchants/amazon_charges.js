// The charges on Amazon's Transactions page, in document order: a date
// heading, then the rows under it, each with an amount, a payment method and
// an "Order #". A charge is "-$12.34" and a refund "+$12.34", the signs the
// bank sees. A row with no card is a gift card's when it says so.
//
// The pull embeds this file.
((root, orderNumber, amount) => {
  const out = [];
  let currentDate = '';
  const nodes = root.querySelectorAll(
    '.apx-transaction-date-container, .apx-transactions-line-item-component-container, ' +
      '[class*="transaction-date"], [class*="transactions-line-item"]'
  );
  for (const el of nodes) {
    const cls = el.className || '';
    const text = el.innerText.replace(/\s+/g, ' ');
    if (/date/.test(cls)) {
      currentDate = text.trim();
      continue;
    }
    const order = text.match(orderNumber);
    const money = text.match(amount);
    if (!order || !money) continue;
    const negative = money[1] === '-' || money[1] === '−';
    let instrument = (text.match(/([A-Za-z ]+?)\s*(?:\*+|ending in|••••)\s*(\d{4})/) || [])
      .slice(1)
      .join(' ••••')
      .trim();
    if (!instrument && /gift\s*card|gift\s*balance|promotional/i.test(text)) instrument = 'Amazon Gift Card';
    out.push({
      order_id: order[1],
      date_text: currentDate,
      amount: (negative ? '-' : '') + money[2].replace(/,/g, ''),
      instrument,
    });
  }
  return out;
})
