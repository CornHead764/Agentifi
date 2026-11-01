# Design round 2: Review by day

Thanks for the round, Rohan. This settles the structure and turns it into a spec for the next mockup and M1. It's checked against `master`, where the households are SQLite files and #25's Connect has not landed yet. **Owner** marks what the owner decided; **Recommendation** marks what still waits for the owner.

## The decision

**Owner:** build on D. Home keeps D's card, with one chip per kind and its count, and the Review tab lists new transactions **by day**. Grouping by person is dropped.

- **Why by day.** Transactions are the main chore, and a day is how a bank statement gets checked. It also fixes D's weak spot: the band with no deadline becomes the days themselves.
- **Why not by person.** SimpleFIN reports accounts, not who swiped, and nothing records the purchaser. Each row shows its account and card ending (`accounts.masked_number`) instead.
- **Layout.** Your D agenda becomes a band pinned above the days, in D's spine order: clocks first (codes, sign-ins, reconnects), then due dates. Items with no deadline (duplicates, held balances, orphan transfers) go under the "Money at stake" heading. Ideas are one uncounted line at the bottom.

## What changes in the original model

`unreviewed` is no longer one summary item. `ListReviewItems` returns the rows, grouped by effective date (trap 4) and paged by day. A row's pending suggestion, or its undetermined note, folds into the row as its category chip. The grouping threshold still collapses a big import.

| Row action | Taps | What happens |
| --- | --- | --- |
| Approve | 1 | The tick or a swipe right. It applies the shown suggestion, as the register's swipe does. |
| Category | 2 | The chip, then a category. Picking also approves. |
| Split | 2 from an order or receipt, 4 to 6 by hand | "Approve split" on the proposed split, or Split, then "One part per item" or amounts, then Save. Approve stays off until the parts add up to the cent. |
| Order and receipt lines | 0 or 1 | The first items show under the row. The row opens a sheet on a phone, or the side panel on desktop, with every line and any photo. |
| "I don't recognise this" | 3 | Flag it and ask the household. The row stays pinned until "It's ours after all" (which approves it) or "Disputed with the bank" (which keeps a note). Excluding it from reports and the plan is an explicit checkbox using the two existing flags. |
| Undo | 1 | Every action shows a toast with Undo, and tapping an approved row's tick un-approves it. Undo restores the earlier category, split and reviewed state, including an applied suggestion. A merge and a held balance keep no swipe and no undo. |
| Desktop keys (**Owner**) | 0 clicks | `c` opens the picker with focus in its search field: a combobox over a listbox (`aria-activedescendant`), with the suggestion first and the first match highlighted. Up and Down move, Enter files and approves, and focus goes to the next row below that needs a look (or the next open row). Esc changes nothing and returns focus to the same row. `s` and the "not recognised" panel follow the same rule. Rows have roving focus, so `j` and `k` keep working, and the shortcut bar shows the keys for what's open. |

## "Looks right" and "needs a look"

A person reads these as a verdict on the row, so ground rule 2 applies. **Recommendation:** add `domain.ReviewReason` and `domain.LooksRight`, with this rule in a "Review queue" section of `docs/calculations.md`. A new row needs a look for the first reason that holds. Otherwise it looks right.

| Reason | Rule |
| --- | --- |
| Not recognised | Its not-recognised flag is open |
| Possible duplicate | It is a leg of an open `duplicate_candidates` pair |
| No category | No category and no splits, or the category check left it undetermined (`category_check_note`) |
| A guess | Its category or split comes from a suggestion with confidence under 0.7 |
| Unusual amount | At least twice the median of the last 6 reviewed rows with the same statement name (ground rule 5), given 3 or more such rows. A series row uses `match_amount_min` and `match_amount_max` instead. This is `domain.UsualAmount`, with its own rule. |

"Approve day" approves only the rows that look right and says how many ("Approve 2 of 3"). The server checks the rule again when it writes. **Recommendation:** a swipe right on a row that needs a look approves it as shown, and the toast names the reason ("Dining out, a guess", with Undo). There are exceptions: a row with no category opens the picker, a duplicate leg has no swipe, and a flagged row sits in the pinned band. D-expanded's track record ("none of 58 changed later") is worth adding once `reviewed_via` exists.

## The badge

**Recommendation:** count act-now items, money-at-stake items and rows that need a look. Rows that look right never count, just as ideas never count. Counting every unreviewed row gave 52 in the invented week, so a big week made it meaningless. Here it is 2 + 5 + 9 = 16. Home's chip still shows "23 new · 9 need a look", the card says "16 need you", and the Review header names the parts. All three read `GetReviewSummary`. One wrinkle: a duplicate pair counts under money at stake, and its legs count again as rows.

**Your question 3 (Recommendation):** yes. Home's chips open Review scrolled to that kind or day, not filtered. "New transactions" goes to the first day with a row that needs a look.

## Your other questions

| Question | Answer | Source |
| --- | --- | --- |
| Which structure? | D's Home card plus Review by day, no person | Owner |
| Snooze per household or per person? | Per household. Items are household facts and there's no per-person model. `review_snoozes.user_id` only records who snoozed. | Recommendation |
| Review as its own tab? | Yes, in the tab bar and the rail, with the badge | Recommendation |
| Who does "ask the household" notify? | Members who can write (owner, admin, member), through `DeliverToSpace` in `service/alerts.go`, which already skips viewers | Recommendation |

## Data and backend

| Exists on master | Used for |
| --- | --- |
| `transactions.is_reviewed`; `POST /transactions/mark-reviewed` | Approve |
| `assistant_actions` (`apply`, `apply-many`, `decline`), `category_suggestion_batches`, `assistant_corrections` | Suggestions on the row |
| `transaction_splits`, `SplitEditor.tsx` | Split |
| `merchant_orders`, `merchant_order_items`, `merchant_matches`; `GET /merchants/transactions/{id}`; `POST /merchants/{merchant}/suggest-categories`; `MerchantPanel.tsx` | Orders and Costco receipts |
| `documents`, `document_links`, `AttachmentPanel.tsx` | Receipts and attachments |
| `user_flag`, `user_flag_note`, the two exclusion flags, `duplicate_candidates` | Flags and duplicates |
| `notifications`, `DeliverToSpace` | Asking the household |
| `REVIEW_QUEUE_SWIPE` in `lib/transactions/gestures.ts`, `SuggestionCard.tsx` | Swipe and confidence |

| New | Why |
| --- | --- |
| `transactions.reviewed_via`, `reviewed_by` and `reviewed_at` (one household migration, with `WriteCounterSQL`) | Undo, "who approved this", the track record |
| A not-recognised state (open, ours or disputed, plus who asked and when), separate from the general `user_flag` | The pinned row and its endings |
| A `transaction_not_recognised` alert type, with a per-row `dedupe_key` and `resolved_at` set on the answer | Asking the household |
| Undo for an applied suggestion: apply records the earlier category, splits and reviewed state, and undo restores them while the row is unchanged | No undo exists today |
| Per-line categories from the split proposal (order items carry none), plus `domain.AllocateTax` with a rounding rule. A photo has no lines, so it is split by hand. | "One part per item" |
| A batched supporting-data read for a page of rows | `MerchantPanel` asks one row at a time |
| `ReviewReason`, `LooksRight`, `UsualAmount`, and `ReviewCounts` with `needs_a_look` | Ground rule 2 |
| Approve day as `mark-reviewed` with explicit ids, checked again | A bulk tap never takes a changed row |

**Day totals** show the money out per day by effective date (trap 4), through the same functions as reports: `domain.SpendingIn` over a one-day window, behind `CountsAsIncomeOrExpense`. Transfer legs and excluded rows drop out, so a day can differ from the bank app's figure, and the rule says so. There is no plan comparison: a past day's pace was never stored.

## Milestones

| Milestone | Change |
| --- | --- |
| M1, backend | Gains the row lane: rows by day with their reason, the needs-a-look rule and badge counts, the batched read, Day totals |
| M2, screens | Review by day, with approve, category, split from lines, supporting data, Approve day, Undo with `reviewed_*` and suggestion undo, and the desktop keys. The Home chips scroll Review. |
| M3 | Adds "I don't recognise this" (state, alert, pinned row) to the other kinds, snooze and bulk |
| M4, M5 | Unchanged |
| M6 | The native app renders the same lane |

## The mockup

The board is [`index.html` on this branch](index.html): phone and desktop, light and dark, all clickable. Screenshots to attach, each as `light-` and `dark-`: `phone-home-390.png`, `phone-list-390.png`, `phone-sheet-order-390.png`, `phone-split-390.png`, `phone-notrecognised-390.png`, `desktop-1440.png`, `desktop-picker-1440.png` and `board-390-full.png`.

## Questions for the owner

1. Should the badge be as recommended, and should a duplicate's legs count as well as the pair?
2. Are 0.7 for "a guess" and twice the median of 6 rows for "unusual" the right lines?
3. Should a swipe right on a row that needs a look approve it, or open it?
4. Snooze per household, Review as a tab, and "ask the household" going to everyone who can write: are all three as recommended?
