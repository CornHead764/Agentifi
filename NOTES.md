# Review queue: by day

`index.html` is one self-contained board for the by-day review queue, with phone and desktop views and a Light / Dark / System theme switch on the board's controls. Screenshots are in `shots/`, in both themes, with phones at 390 wide and 2x and desktops at 1440 wide.

Assumptions in place of an interview. This is Operate mode, and the visual world is Rohan's: greyscale, with one accent reserved for "act now". "Today" is Friday, October 31, as in Rohan's household. The non-transaction items are Rohan's items, with one change: the reconnect moves to a savings bank, because the checking account is still syncing in this data.

## The direction

Review is the week's new transactions as a bank statement, grouped by day: each row has a tick and each day has an "Approve" for its sure rows. D's other kinds are pinned above the days in D's order.

## How it meets the five needs

A tap count starts from the Review tab, with the row in view.

| Need | Taps | How |
| --- | --- | --- |
| 1. Made by us, and a path when not recognised | **1** | Approving is the confirmation. "I don't recognise this" is in the row's sheet: **2** taps to the steps, **3** to flag the row and ask the household. |
| 2. Right category, quick to change | **0** to see, **2** to change | The chip on the row shows it. Tap the chip, then a category, which files and approves the row. |
| 3. Split | **3** | When the order or receipt gave a split: the row, then "Approve split". A split by hand is the row, Split, "One part per item" or amounts, then Save: **4 to 6** taps. |
| 4. Supporting data | **0** or **1** | The first items show under the row ("Amazon order: Printer ink, Kids' rain boots…"). Tap the row for the order, the receipt lines or the photo. |
| 5. Approve in one tap | **1** | The tick or a swipe right. A day of sure rows is **1** tap ("Approve 2 of 3"). |

The rules behind those taps:

- **"Approve day" takes only the rows that look right.** A row needs a look when it has no category, its category is a guess (confidence under 0.7), it is flagged as not recognised, it is part of a possible duplicate, or its amount is at least twice the payee's usual. Every other open row looks right. City Transit ($40.00, usually $20.00) shows the unusual-amount case. The button says exactly how many it takes, as in "Approve 2 of 3". The 0.7 threshold is an assumption; it would be a named domain rule.
- **A duplicate's row cannot be approved** until the pair is decided ("Decide above"), and it has no swipe. This follows the rule that hard-to-undo decisions are never one tap.
- **A split cannot be saved until the parts add up** to the charge exactly. Amounts are integer cents.
- **"I don't recognise this" changes nothing in the ledger.** Flagging asks everyone in the household and pins the row at the top of Review. From there it is "It's ours after all" (approves it) or "Disputed with the bank". Excluding a disputed row from reports and the plan is an explicit checkbox that uses the two exclusion flags that already exist.
- **Each row shows its account or card**, short on a phone ("Joint ··3003", "Everyday ··1120") and in full on desktop. That is what the data can say about a purchase, and it helps recognise one. The board says nothing about which person made a purchase, because nothing records that reliably.

## How it fits D, and what it changes

- D's Home card stays, with one chip per kind and its count plus the most urgent item. The chips now scroll Review to their section, which answers Rohan's question 3 with a yes.
- D's time spine survives as order, not as a drawing. Anything on a clock is pinned first (the code, the reconnect). "Money at stake" follows, collapsed on a phone to one line naming what is in it. Below the pins are the days.
- This fixes D's weak spot. In D, seven of the eleven items fell into the band with no deadline. Here the band is the transactions, and a date is how people remember a purchase.
- The owner's earlier "unreviewed is one summary item" becomes row-by-row. Grouping by day is what keeps the volume manageable: last week's open rows sit in one collapsed block ("Approve the 6").
- **The badge counts what needs a person:** act-now items, money-at-stake items and transaction rows that need a look. Rows that look right never count, like ideas. In the invented week that is 2 + 5 + 9 = 16. Home's "New transactions" chip still shows the full figure ("23 new · 9 need a look"), the Home card says "16 need you", and the Review header says the same and names its parts.
- **Day totals, explored.** The "Out" option sums a day's outflows, which is the number the bank's own app shows, so it helps tick a day off against the statement. "Against the plan" shows what each day took from the month's `left_this_month`, leaving out bills already counted in the plan.
  - It is accurate, but it catches overspending, not mistakes. A wrong category usually leaves the day's figure unchanged.
  - The per-day pace (`per_day` in calculations.md §5) is only right for today. A past day's pace was never stored, so comparing Thursday with "the daily pace" would be a wrong number.
  - **Recommendation:** ship "Out" or nothing, and leave the plan comparison off. A better mistake-catcher is a per-row mark for an amount unlike the payee's usual, which would be a new domain function.

## What's new in the backend

What exists (read from `origin/master`):
- `transactions.is_reviewed`, and `POST /transactions/mark-reviewed` for bulk marking.
- The suggestion apply routes on `assistant_actions` (`apply`, `apply-many`, `decline`), including proposed splits, which use the `split_transaction` tool in `lib/transactions/suggestions.ts`.
- `transaction_splits`; `merchant_matches`, `merchant_orders` and `merchant_order_items`, behind `MerchantPanel.tsx`.
- `documents` and `document_links`, behind `AttachmentPanel.tsx`.
- `transactions.user_flag` and `user_flag_note`; the two exclusion flags; `duplicate_candidates`.
- `accounts.masked_number`, which gives the "··3003" on each row.
- The register's review-queue swipe, `REVIEW_QUEUE_SWIPE` in `lib/transactions/gestures.ts` (right approves, left picks a category), which this keeps as it is.
- `reviewIconAction` in `lib/transactions/reviewMode.ts`. A row with a suggestion waiting opens instead of being ticked. Here, ticking applies the shown suggestion, which is what the register's swipe already does.

New:
- `ListReviewItems` to return transaction rows individually, paged by day, alongside the grouped kinds. Today `unreviewed` is one summary item.
- `domain.ReviewDayTotals` for "Out", with a rule in calculations.md. Transfer legs need a decision: both legs of a card payment fall on one day, so the sum must exclude them or double-count.
- `domain.SureToApprove(row)`, the rule behind "Approve day", named and tested, because it decides what a bulk tap applies.
- For "I don't recognise this": a new alert type on `notifications`, delivered through `DeliverToSpace`, which already skips viewers.
- A short display name per account for the row label ("Joint"), or a rule that derives one from the account name. Today only the full name exists.
- No schema change otherwise.

## Risks

- A tidy day invites a tick without looking. "Approve day" leaves the uncertain rows out, but a confident wrong guess still goes through, as it would in the register today.
- A row in a possible duplicate counts once as a row that needs a look and the pair counts once as money at stake, so one decision can move the badge by two.
- The pins push the first day down by about 250px on a phone, even with "Money at stake" collapsed. On a heavy day of sign-ins, transactions start below the fold. That is deliberate (D's kinds must not be buried), but it is a cost.
- Need 1 is implicit. Ticking a row means "ours and right", and nothing asks the question out loud.

## Open questions for the owner

1. Settled: the badge counts act-now, money at stake, and rows that need a look.
2. Is the 0.7 confidence threshold for "Approve day" right, and should a proposed split from an order count as sure?
3. Ship the day "Out" total? I recommend it, and recommend leaving the plan comparison out.
4. Settled: "ask the household" notifies members who can write (owner, admin, member), through the same fan-out as `DeliverToSpace` in `service/alerts.go`. Viewers see the flag and cannot act.

## Dark theme

Every colour in the page is a token on `:root`; nothing outside the token blocks is a literal. The dark values apply under `@media (prefers-color-scheme: dark)` for `:root:not([data-theme="light"])`, and again for `:root[data-theme="dark"]`. The board's Theme switch (Light, Dark, System) sets or clears `data-theme` and remembers the choice in this browser. `#theme=dark` in the URL forces it for a screenshot.

The contrast column gives each text token on the surface it is hardest to read on. Surfaces show the primary text on them instead.

| Token | Used for | Light | Dark | Light contrast | Dark contrast |
| --- | --- | --- | --- | --- | --- |
| `--surface` | App screen and rows | #ffffff | #131416 | surface | ink on it 15.8:1 |
| `--raised` | Pins, Home card, money at stake | #ffffff | #1b1c1f | surface | ink on it 14.6:1 |
| `--sheet` | Bottom sheet | #ffffff | #222327 | surface | ink on it 13.5:1 |
| `--ink` | Primary text, primary fill, focus ring | #17181a | #eceef0 | 17.8:1 on sheet | 13.5:1 on sheet |
| `--t2` | Secondary text | #4b4e54 | #b8bbc1 | 8.3:1 on sheet | 8.2:1 on sheet |
| `--t3` | Muted text | #63676e | #9ca0a7 | 5.7:1 on sheet | 6.0:1 on sheet |
| `--done` | Approved row text | #6b6e75 | #8b8f96 | 4.6:1 on ground | 5.0:1 on ground |
| `--control` | Unticked ring, input borders | #84878e | #7d8189 | 3.2:1 on ground | 4.0:1 on sheet |
| `--urgent` | Tier 1 accent text and fill | #b4410c | #f0965f | 5.0:1 on urgent-bg | 7.2:1 on urgent-bg |
| `--on-urgent` | Text on the urgent button | #ffffff | #1d0e05 | 5.7:1 on urgent | 8.3:1 on urgent |
| `--on-ink` | Text on primary, toast, badge | #ffffff | #131416 | 17.8:1 on ink | 15.8:1 on ink |
| `--off-fg` | Disabled primary button label | #4b4e54 | #b8bbc1 | 7.1:1 on off-bg | 7.2:1 on off-bg |

Other tokens, without a text pair: `--ground` (#f3f3f4 / #202125, hover and selected row), `--chip` (#ebecee / #2b2d31, icon wells and the confidence track; the bar's fill is `--ink`, 15.0:1 and 11.9:1 against it), `--line` and `--line2` (dividers and pill borders), `--field` (#ffffff / #0f1012, inputs), `--rail`, `--bar`, `--urgent-bg` (#fbefe8 / #2c1c13), `--urgent-line`, `--urgent-ico`, `--swipe-l`, `--sticky`, `--scrim`, `--shade`, `--reach`, `--logo`, `--frame-line` and the board's own `--board-*` set.

Decisions:

- **Composed, not inverted.** The dark greys keep the light theme's slight cool cast. Text steps (ink, t2, t3) keep their order and spacing rather than mirroring their values.
- **Elevation by lightness.** Each layer up is lighter: the screen (#131416), then raised cards (#1b1c1f), then the sheet (#222327). Shadows stay but deepen, because a shadow alone barely reads on near-black. Inputs go the other way, darker than the sheet, so they read as wells. The desktop rail sits below the screen, as chrome.
- **One accent, retuned.** The tier-1 orange becomes #f0965f, lighter and a little softer so it holds 7:1 on the dark urgent card. The urgent button flips to dark text on the orange fill. The urgent card's border is dimmed (#7a4426) in dark only, so the accent sits in the time text and the button rather than in a glowing outline. The border is decorative; the tint and text identify the card.
- **Primary actions become light fills.** "Approve split", the selected segment, the toast and the badge use `--ink` as their fill, so in dark they are near-white with dark text. That keeps them the strongest thing on screen, as black is in light.
- **Letter-spacing on prose only.** Light text on dark gets a touch more tracking (0.006em) in sentences, steps and notes. An earlier pass applied it everywhere and clipped the row's account label at 390 ("Travel ··22…"), so rows and chips keep the light theme's spacing.

What the contrast pass changed in light too:

- **Approved rows** were #9a9da3 on white, 2.7:1. They are now #6b6e75, 4.6:1 on the selected-row grey. The filled tick still marks them done.
- **The unticked ring and input borders** were the divider grey #d0d2d6, 1.5:1, which is too faint for the only outline of a control. They now use `--control` at 3.2:1 or better. Dashed "guess" chips use it too. Plain dividers and pill borders stay light, because their text identifies them.
- **The disabled save** in the split editor was the primary fill at 45% opacity, which put its label ("Parts must add up to $212.50") at about 2:1. It now has its own grey fill and label, 7:1 in both themes, and still does nothing until the parts add up.
- **The placeholder logo** in the desktop rail was the ink colour, a bright white square in dark. It now has its own muted token.

The screenshots were checked at 390 and 1440 in both themes. There was no horizontal scroll at 390, no request outside the file and no console errors. The theme switch, the system setting and the forced setting against the opposite system setting all resolve to the right surface. Tick, swipe right, "Approve day", chip-to-category, the split editor with "One part per item", flagging, "It's ours after all", deciding the duplicate and the desktop keys were all exercised in a script.

## Keyboard (desktop)

`c` opens the category picker with focus in its search field. The field is a combobox over a grouped listbox (`aria-activedescendant`), with the suggestion first and the first match highlighted. Up and Down move, Home and End jump, and Enter files the row under the highlighted category and approves it, as a click does. Esc closes any picker, split or "not recognised" panel with nothing changed.

Focus rule: a decision that finishes a row (Enter in the picker, saving a split, "It's ours after all", "Disputed with the bank", flagging) moves focus to the next row below that needs a look; if none is left below, the next open row; if none, it stays. Esc, Back and Cancel return focus to the same row. `x` keeps its own rule, the next row, because it is a pass down the list. Rows use roving focus (`tabindex` 0 on the selected row), so `j` and `k` work right after any of these with no click, and Tab leaves the list. The shortcut bar changes with what is open. A scripted keyboard-only run (`c`, "gro", Down, Enter, then `j`, then `c` and Esc, `s` and Esc) passes with no console errors. `shots/*-desktop-picker-1440.png` shows the picker.
