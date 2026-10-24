package importer

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/decimaltext"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The only part of the import that touches a database. Mapping sets every
// primary key, so writing is a sequence of inserts whose order IS the foreign
// key dependency graph. A run with errors is never written.

// ErrRefused is returned rather than writing a ledger the report says not to
// trust.
var ErrRefused = errors.New("importer: refusing to write a run with errors")

// Write atomically inserts the ledger into its household file. New households
// remain unpublished until the ledger commits; users may survive a failed import.
// Memberships and server metadata publish in a separate server transaction.
// A publication failure retains the committed ledger and reports its recovery ID.
func Write(ctx context.Context, db *store.Store, mapped *Mapped) error {
	if !mapped.Report.OK() {
		return fmt.Errorf("%w: %d errors; nothing was written. See the report for the store, "+
			"record and field of each", ErrRefused, len(mapped.Report.Errors))
	}
	if mapped.Space == nil {
		return fmt.Errorf("importer: nothing to write: the export produced no space")
	}
	if err := Refusal(ctx, db, mapped); err != nil {
		return err
	}
	var err error
	owners := map[uuid.UUID]uuid.UUID{}
	err = db.InTx(ctx, func(tx *store.Store) error {
		for _, user := range mapped.Users {
			existing, err := tx.GetUserByEmail(ctx, strings.TrimSpace(user.Email))
			if err == nil {
				owners[user.ID] = existing.ID
				continue
			}
			if !errors.Is(err, store.ErrNotFound) {
				return err
			}
			copied := *user
			copied.Email = strings.TrimSpace(user.Email)
			if err := tx.CreateUser(ctx, &copied); err != nil {
				return err
			}
			owners[user.ID] = copied.ID
		}

		return nil
	})
	if err != nil {
		return err
	}
	if !mapped.IntoExisting {
		pending := *mapped.Space
		pending.IsDeleted = true
		if err := db.CreateSpace(ctx, &pending); err != nil {
			return err
		}
	}
	h, err := db.ForHousehold(ctx, mapped.SpaceID)
	if err != nil {
		return err
	}
	err = h.InTx(ctx, func(tx *store.Store) error {
		if mapped.IntoExisting {
			if err := refuseOccupiedSpace(ctx, tx, mapped.SpaceID); err != nil {
				return err
			}
		}
		household, err := tx.HouseholdDB(ctx, mapped.SpaceID)
		if err != nil {
			return err
		}
		if _, err := household.Write().ExecContext(ctx, "UPDATE household_metadata SET primary_currency=? WHERE id=1", mapped.Space.PrimaryCurrency); err != nil {
			return err
		}
		if err := writeAll(ctx, household.Write(), household.Now(), mapped, owners); err != nil {
			return err
		}
		return tx.FileTransferLegs(ctx, mapped.SpaceID)
	})
	if err != nil {
		if !mapped.IntoExisting {
			return errors.Join(err, db.DeleteSpace(context.WithoutCancel(ctx), mapped.SpaceID))
		}
		return err
	}
	// Publish membership and server metadata only after the complete ledger commits.
	// A publication failure leaves the complete household file available for recovery.
	err = db.InTx(ctx, func(tx *store.Store) error {
		for _, membership := range mapped.Memberships {
			copied := *membership
			copied.UserID = owners[membership.UserID]
			if mapped.IntoExisting {
				if _, err := tx.GetMembership(ctx, mapped.SpaceID, copied.UserID); err == nil {
					continue
				} else if !errors.Is(err, store.ErrNotFound) {
					return err
				}
			}
			if err := tx.CreateMembership(ctx, mapped.SpaceID, &copied); err != nil {
				return err
			}
		}
		current, err := tx.GetSpace(ctx, mapped.SpaceID)
		if err != nil {
			return err
		}
		current.Name = mapped.Space.Name
		current.PrimaryCurrency = mapped.Space.PrimaryCurrency
		current.Timezone = mapped.Space.Timezone
		current.IsDeleted = false
		return tx.UpdateSpace(ctx, &current)
	})
	if err != nil {
		return fmt.Errorf("importer: ledger committed in household %s but publication failed; recover this household rather than reimporting: %w", mapped.SpaceID, err)
	}
	return nil
}

type importDB interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func writeAll(ctx context.Context, tx importDB, now int64, m *Mapped, owners map[uuid.UUID]uuid.UUID) error {
	space := uuid.UUID(m.SpaceID)
	w := &writer{ctx: ctx, tx: tx, space: space, now: now, currency: m.Space.PrimaryCurrency, accounts: map[uuid.UUID]string{}, transactions: map[uuid.UUID]string{}, securities: map[uuid.UUID]string{}}
	for _, security := range m.Securities {
		w.securities[security.ID] = security.Currency
	}
	for _, a := range m.Accounts {
		w.accounts[a.ID] = a.Currency
	}
	for _, t := range m.Transactions {
		w.transactions[t.ID] = t.Currency
	}
	ownerOf := func(id uuid.UUID) uuid.UUID {
		if owner, ok := owners[id]; ok {
			return owner
		}
		return id
	}
	for _, institution := range m.Institutions {
		w.row("institutions").set("id", institution.ID).set("name", institution.Name).set("external_id", nullText(institution.ExternalID)).set("domain", nullText(institution.Domain)).set("logo_url", nullText(institution.LogoURL)).set("is_deleted", institution.IsDeleted).exec()
	}

	// 4. Accounts, which name an institution.
	for order, account := range m.Accounts {
		w.row("accounts").
			set("id", account.ID).
			set("institution_id", nullUUID(account.InstitutionID)).
			set("name", account.Name).
			set("description", nullText(account.Description)).
			set("notes", nullText(account.Notes)).
			set("kind", string(account.Kind)).
			set("type", account.Type).
			set("usage_type", nullText(account.UsageType)).
			set("currency", account.Currency).
			set("masked_number", nullText(account.MaskedNumber)).
			set("sort_order", order).
			set("provider_balance", optionalMoney(account.ProviderBalance, account.HasProviderBalance)).
			set("provider_balance_at", account.ProviderBalanceAt).
			set("goal_balance", moneyArg(account.GoalBalance)).
			set("excluded_from_reports", account.ExcludedFromReports).
			set("excluded_from_spending_plan", account.ExcludedFromSpendingPlan).
			set("excluded_from_account_bar", account.ExcludedFromAccountBar).
			set("include_in_net_worth", account.IncludeInNetWorth).
			set("exclude_bank_pending", account.ExcludeBankPending).
			set("is_closed", account.IsClosed).
			set("closed_on", nullDate(account.ClosedOn)).
			set("is_deleted", account.IsDeleted).
			exec()
	}

	// 5. Categories, parent-first: parent_id references this same table and
	// SQLite checks it on the insert. An existing space holds none but the
	// untouched default tree (refuseOccupiedSpace), which the export's own
	// categories take the place of.
	if m.IntoExisting {
		if _, err := tx.ExecContext(ctx, `DELETE FROM categories`); err != nil {
			return fmt.Errorf("importer: clearing the default categories: %w", err)
		}
	}
	for order, category := range parentsFirst(m.Categories) {
		w.row("categories").
			set("id", category.ID).
			set("parent_id", nullUUID(category.ParentID)).
			set("name", category.Name).
			set("kind", string(category.Kind)).
			set("known_category_id", nullText(category.KnownCategoryID)).
			set("txf_id", nullText(category.TxfID)).
			set("txf_ids", store.NonNil(category.TxfIDs)).
			set("is_user_assignable", category.IsUserAssignable).
			set("is_editable", category.IsEditable).
			set("excluded_from_reports", category.ExcludedFromReports).
			set("excluded_from_spending_plan", category.ExcludedFromSpendingPlan).
			set("excluded_from_category_list", category.ExcludedFromCategoryList).
			set("sort_order", order).
			set("is_deleted", category.IsDeleted).
			exec()
	}

	// 6. Tags.
	for _, tag := range m.Tags {
		w.row("tags").
			set("id", tag.ID).
			set("name", tag.Name).
			set("color", nullText(tag.Color)).
			set("is_deleted", tag.IsDeleted).
			exec()
	}

	// 7. Filters and their clauses, before the envelopes, watchlists and rules
	// whose foreign keys are ON DELETE RESTRICT against them.
	for _, filter := range m.Filters {
		w.filter(space, filter)
	}

	// 8. Series and 9. rules, both before transactions: a posted row names the
	// series occurrence it fulfils and the rule that touched it.
	for _, series := range m.Series {
		w.row("series").
			set("id", series.ID).
			set("account_id", series.AccountID).
			set("category_id", nullUUID(series.CategoryID)).
			set("kind", string(series.Kind)).
			set("description", series.Description).
			set("display_name", nullText(series.DisplayName)).
			set("amount", moneyArg(series.Amount)).
			set("currency", series.Currency).
			set("alias", string(series.Alias)).
			set("frequency", nullText(string(series.Frequency))).
			set(`"interval"`, series.Interval).
			set("by_month_day", store.NonNil(series.ByMonthDay)).
			set("by_day", store.NonNil(series.ByDay)).
			set("start_on", series.StartOn.String()).
			set("end_on", nullDate(series.EndOn)).
			set("next_due_on", nullDate(series.NextDueOn)).
			set("override_next_due_on", nullDate(series.OverrideNextDueOn)).
			set("override_next_amount", optionalMoney(series.OverrideNextAmount, series.HasOverrideNextAmount)).
			set("auto_adjust_due_on", series.AutoAdjustDueOn).
			set("reminder_days", series.ReminderDays).
			set("auto_accept_days", series.AutoAcceptDays).
			set("match_criteria", series.MatchCriteria).
			set("match_amount_min", optionalMoney(series.MatchAmountMin, series.HasMatchAmountMin)).
			set("match_amount_max", optionalMoney(series.MatchAmountMax, series.HasMatchAmountMax)).
			set("learned_descriptions", store.NonNil(series.LearnedDescriptions)).
			set("template_payee", nullText(series.TemplatePayee)).
			set("template_notes", nullText(series.TemplateNotes)).
			set("template_tag_ids", store.NonNil(series.TemplateTagIDs)).
			set("template_excluded_from_reports", series.TemplateExcludedFromReports).
			set("template_excluded_from_spending_plan", series.TemplateExcludedFromSpendingPlan).
			set("template_splits", w.jsonArg("recurring_series.template_splits", series.TemplateSplits)).
			set("is_active", series.IsActive).
			set("is_deleted", series.IsDeleted).
			exec()
	}
	for _, rule := range m.Rules {
		w.rule(space, rule)
	}

	// 10. Transactions, then their splits, tags and attachments.
	for _, txn := range m.Transactions {
		w.row("transactions").
			set("id", txn.ID).
			set("account_id", txn.AccountID).
			set("date", txn.Date.String()).
			set("effective_date", nullDate(txn.EffectiveDate)).
			set("amount", moneyArg(txn.Amount)).
			set("currency", txn.Currency).
			set("statement_name", txn.StatementName).
			set("payee", txn.Payee).
			set("notes", nullText(txn.Notes)).
			set("check_number", nullText(txn.CheckNumber)).
			set("category_id", nullUUID(txn.CategoryID)).
			set("source", string(txn.Source)).
			set("is_pending", txn.IsPending).
			set("is_deleted", txn.IsDeleted).
			set("is_reviewed", txn.IsReviewed).
			set("excluded_from_reports", txn.ExcludedFromReports).
			set("excluded_from_spending_plan", txn.ExcludedFromSpendingPlan).
			set("is_bill", txn.IsBill).
			set("is_subscription", txn.IsSubscription).
			set("transfer_pair_id", nullUUID(txn.TransferPairID)).
			set("user_flag", nullText(txn.UserFlag)).
			set("user_flag_note", nullText(txn.UserFlagNote)).
			set("series_id", nullUUID(txn.SeriesID)).
			set("series_due_on", nullDate(txn.SeriesDueOn)).
			set("estimate_status", nullText(txn.EstimateStatus)).
			set("accepted_on", nullDate(txn.AcceptedOn)).
			set("expires_on", nullDate(txn.ExpiresOn)).
			set("rule_id", nullUUID(txn.RuleID)).
			set("balance", optionalMoney(txn.Balance, txn.HasBalance)).
			exec()
	}
	for _, txn := range m.Transactions {
		for _, tagID := range txn.TagIDs {
			w.row("transaction_tags").
				set("transaction_id", txn.ID).
				set("tag_id", tagID).
				exec()
		}
		for _, split := range txn.Splits {
			w.row("transaction_splits").
				set("id", split.ID).
				set("transaction_id", txn.ID).
				set(`"position"`, split.Position).
				set("amount", moneyArg(split.Amount)).
				set("category_id", nullUUID(split.CategoryID)).
				set("memo", nullText(split.Memo)).
				exec()
			for _, tagID := range split.TagIDs {
				w.row("split_tags").
					set("split_id", split.ID).
					set("tag_id", tagID).
					exec()
			}
		}
	}
	// The export carries no bytes and so no hash; the placeholder is unique
	// per document and never equal to a digest, so the dedupe cannot match it.
	for _, attachment := range m.Attachments {
		w.row("documents").
			set("id", attachment.ID).
			set("content_sha256", store.UnhashedDocumentHash(attachment.ID)).
			set("content_type", attachment.ContentType).
			set("size_bytes", attachment.SizeBytes).
			set("filename", attachment.Filename).
			set("storage_key", attachment.StorageKey).
			set("source", store.DocumentSourceUpload).
			exec()
		w.row("document_links").
			set("document_id", attachment.ID).
			set("kind", string(store.DocumentLinkTransaction)).
			set("target_id", attachment.TransactionID).
			set("role", store.DocumentRoleAttachment).
			exec()
	}

	// 11. Goals, and the join table that names both a goal and an account.
	for _, goal := range m.Goals {
		w.row("goals").
			set("id", goal.ID).
			set("account_id", goal.AccountID).
			set("name", goal.Name).
			set("description", nullText(goal.Description)).
			set("notes", nullText(goal.Notes)).
			set("kind", nullText(goal.Kind)).
			set("image_url", nullText(goal.ImageURL)).
			set("tag_id", nullUUID(goal.TagID)).
			set("target_amount", moneyArg(goal.TargetAmount)).
			set("target_on", nullDate(goal.TargetOn)).
			set("start_on", nullDate(goal.StartOn)).
			set("completed_on", nullDate(goal.CompletedOn)).
			set("contribution_amount", moneyArg(goal.ContributionAmount)).
			set("contribution_frequency", nullText(goal.ContributionFrequency)).
			set("contributed_this_month", moneyArg(goal.ContributedThisMonth)).
			set("saved_so_far", moneyArg(goal.SavedSoFar)).
			set("spent", moneyArg(goal.Spent)).
			set("txn_ids", store.NonNil(goal.TxnIDs)).
			set("withdrawal_txn_ids", store.NonNil(goal.WithdrawalTxnIDs)).
			set("is_taken_from_plan", goal.IsTakenFromPlan).
			set("is_deleted", goal.IsDeleted).
			exec()
		for _, accountID := range goal.FundingAccountIDs {
			w.row("goal_funding_accounts").
				set("goal_id", goal.ID).
				set("account_id", accountID).
				exec()
		}
	}

	// 12. Spending plan months, then the envelopes that name one.
	for _, month := range m.Months {
		row := w.row("spending_plan_months").
			set("id", month.ID).
			set("month", month.Month.String()).
			set("calculated_rollover_amount", moneyArg(month.CalculatedRollover))
		for _, name := range []string{"income", "bills", "subscriptions", "transfer", "goals", "planned_spending", "spent"} {
			bucket := month.bucket(name)
			row.set("calculated_"+name+"_amount", moneyArg(bucket.Calculated)).
				set(name+"_txn_ids", store.NonNil(bucket.TxnIDs)).
				set("excluded_"+name+"_txn_ids", store.NonNil(bucket.ExcludedTxnIDs)).
				set("overwritten_"+name+"_amount", optionalMoney(bucket.Overwritten, bucket.HasOverwritten)).
				set("reset_overwritten_"+name, bucket.ResetOverwritten)
		}
		row.set("set_aside", moneyArg(month.SetAside)).
			set("total_to_spend_amount", moneyArg(month.TotalToSpend)).
			set("left_to_spend_amount", moneyArg(month.LeftToSpend)).
			set("projected_other_spending", moneyArg(month.ProjectedOtherSpending)).
			set("projection_type", string(month.ProjectionType)).
			set("projection_start_date", nullDate(month.ProjectionStartDate)).
			set("projection_end_date", nullDate(month.ProjectionEndDate)).
			set("projection_buffer", moneyArg(month.ProjectionBuffer)).
			set("projection_window_months", month.ProjectionWindowMonths).
			set("is_closed_out", month.IsClosedOut).
			set("show_closed_out", month.ShowClosedOut).
			set("new_month_viewed", month.NewMonthViewed).
			set("hide_excluded_from_ui", month.HideExcludedFromUI).
			exec()
	}
	for _, envelope := range m.Envelopes {
		w.row("envelopes").
			set("id", envelope.ID).
			set("spending_plan_month_id", envelope.SpendingPlanMonthID).
			set("filter_id", envelope.FilterID).
			set("recurring_group_id", nullUUID(envelope.RecurringGroupID)).
			set("name", envelope.Name).
			set("target_amount", moneyArg(envelope.TargetAmount)).
			set("overwritten_target_amount", optionalMoney(envelope.OverwrittenTargetAmount, envelope.HasOverwrittenTarget)).
			set("calculated_spent_amount", moneyArg(envelope.CalculatedSpentAmount)).
			set("rollover_amount", moneyArg(envelope.RolloverAmount)).
			set("auto_release_rollover", envelope.AutoReleaseRollover).
			set("recurring", envelope.Recurring).
			set("txn_ids", store.NonNil(envelope.TxnIDs)).
			set("excluded_txn_ids", store.NonNil(envelope.ExcludedTxnIDs)).
			set("hide_excluded_from_ui", envelope.HideExcludedFromUI).
			exec()
	}

	// 13. Watchlists.
	for _, watchlist := range m.Watchlists {
		w.row("watchlists").
			set("id", watchlist.ID).
			set("filter_id", watchlist.FilterID).
			set("name", watchlist.Name).
			set("emoji", nullText(watchlist.Emoji)).
			set("target_amount", optionalMoney(watchlist.TargetAmount, watchlist.HasTarget)).
			set("period", watchlist.Period).
			set("start_date", nullDate(watchlist.StartDate)).
			set("end_date", nullDate(watchlist.EndDate)).
			set("is_deleted", watchlist.IsDeleted).
			exec()
	}

	// 14. Securities, then the holdings that name one and an account.
	for _, security := range m.Securities {
		w.row("securities").
			set("id", security.ID).
			set("symbol", security.Symbol).
			set("name", security.Name).
			set("kind", security.Kind).
			set("exchange", nullText(security.Exchange)).
			set("currency", security.Currency).
			set("cusip", nullText(security.CUSIP)).
			set("isin", nullText(security.ISIN)).
			set("last_price", nullRateArg(security.LastPrice, security.HasLastPrice)).
			set("last_price_at", security.LastPriceAt).
			set("prior_close", nullRateArg(security.PriorClose, security.HasPriorClose)).
			set("is_deleted", security.IsDeleted).
			exec()
	}
	for _, holding := range m.Holdings {
		w.row("holdings").
			set("id", holding.ID).
			set("account_id", holding.AccountID).
			set("security_id", holding.SecurityID).
			set("external_id", nullText(holding.ExternalID)).
			set("shares", rateArg(holding.Shares)).
			set("cost_basis", optionalMoney(holding.CostBasis, holding.HasCostBasis)).
			set("average_cost", nullRateArg(holding.AverageCost, holding.HasAverage)).
			set("is_cost_basis_complete", holding.IsCostBasisComplete).
			set("market_value", optionalMoney(holding.MarketValue, holding.HasMarketValue)).
			set("as_of", nullDate(holding.AsOf)).
			exec()
	}

	// 15. Balance snapshots and alert rules.
	for _, snapshot := range m.BalanceSnapshots {
		w.row("balance_snapshots").
			set("id", snapshot.ID).
			set("account_id", snapshot.AccountID).
			set("as_of", snapshot.AsOf.String()).
			set("balance", moneyArg(snapshot.Balance)).
			set("is_imported", true).
			exec()
	}
	if m.IntoExisting && len(m.AlertRules) > 0 && w.err == nil {
		// The owner may have set notifications in the empty space already;
		// the settings they kept in Simplifi replace them rather than collide.
		for _, owner := range owners {
			if _, err := tx.ExecContext(ctx, `DELETE FROM alert_rules WHERE user_id = ?`, owner); err != nil {
				return fmt.Errorf("importer: writing alert_rules: %w", err)
			}
		}
	}
	for _, rule := range m.AlertRules {
		w.row("alert_rules").
			set("id", rule.ID).
			set("user_id", ownerOf(rule.UserID)).
			set("alert_type", rule.AlertType).
			set("account_id", nullUUID(rule.AccountID)).
			set("is_enabled", rule.IsEnabled).
			set("is_paused", rule.IsPaused).
			set("channel_email", rule.ChannelEmail).
			set("channel_push", rule.ChannelPush).
			set("channel_in_app", rule.ChannelInApp).
			set("threshold_amount", optionalMoney(rule.ThresholdAmount, rule.HasThresholdAmount)).
			set("threshold_count", rule.ThresholdCount).
			set("threshold_pct", optionalPercent(rule.ThresholdPct, rule.HasThresholdPct)).
			exec()
	}

	return w.err
}

// parentsFirst orders categories so that no row is inserted before its parent;
// categories.parent_id is checked on the insert.
func parentsFirst(categories []*store.Category) []*store.Category {
	byID := make(map[uuid.UUID]*store.Category, len(categories))
	for _, category := range categories {
		byID[category.ID] = category
	}
	out := make([]*store.Category, 0, len(categories))
	placed := make(map[uuid.UUID]bool, len(categories))

	var place func(*store.Category)
	place = func(category *store.Category) {
		if placed[category.ID] {
			return
		}
		// Marked before the parent is followed, so a cycle terminates; the
		// database then refuses it.
		placed[category.ID] = true
		if parent, found := byID[category.ParentID]; found {
			place(parent)
		}
		out = append(out, category)
	}
	for _, category := range categories {
		place(category)
	}
	return out
}

// writer keeps the first failure, with its table and row, and skips the rest.
type writer struct {
	ctx          context.Context
	tx           importDB
	now          int64
	currency     string
	accounts     map[uuid.UUID]string
	transactions map[uuid.UUID]string
	securities   map[uuid.UUID]string
	space        uuid.UUID
	err          error
}

type rowBuilder struct {
	w     *writer
	table string
	names []string
	args  []any
}

func (w *writer) row(table string) *rowBuilder {
	return &rowBuilder{w: w, table: table}
}

// filter writes a filter and its clauses, which the full import and the
// rules-only import both do.
func (w *writer) filter(space uuid.UUID, filter *store.Filter) {
	w.row("filters").
		set("id", filter.ID).
		set("name", nullText(filter.Name)).
		set("scope", filter.Scope).
		set("query_text", nullText(filter.QueryText)).
		set("is_deleted", filter.IsDeleted).
		exec()
	w.filterItems(space, filter.Items)
}

func (w *writer) filterItems(space uuid.UUID, items []store.FilterItem) {
	for _, item := range items {
		w.row("filter_items").
			set("id", item.ID).
			set("filter_id", item.FilterID).
			set("field", item.Field).
			set("operator", item.Operator).
			set("group_index", item.GroupIndex).
			set(`"position"`, item.Position).
			set("negated", item.Negated).
			set("value_ids", store.NonNil(item.ValueIDs)).
			set("value_texts", store.NonNil(item.ValueTexts)).
			set("text", nullText(item.Text)).
			set("amount_min", optionalMoney(item.AmountMin, item.HasAmountMin)).
			set("amount_max", optionalMoney(item.AmountMax, item.HasAmountMax)).
			set("date_from", nullDate(item.DateFrom)).
			set("date_to", nullDate(item.DateTo)).
			set("date_preset", nullText(item.DatePreset)).
			set("state", item.State).
			exec()
	}
}

func (w *writer) rule(space uuid.UUID, rule *Rule) {
	w.row("rules").
		set("id", rule.ID).
		set("name", rule.Name).
		set("filter_id", rule.FilterID).
		set("priority", rule.Priority).
		set("is_active", rule.IsActive).
		set("is_deleted", rule.IsDeleted).
		set("source_ref", rule.SourceRef).
		set("set_payee", nullText(rule.SetPayee)).
		set("set_category_id", nullUUID(rule.SetCategoryID)).
		set("add_tag_ids", store.NonNil(rule.AddTagIDs)).
		set("set_notes", nullText(rule.SetNotes)).
		set("set_excluded_from_reports", rule.SetExcludedFromReports).
		set("set_excluded_from_spending_plan", rule.SetExcludedFromSpendingPlan).
		set("set_is_reviewed", rule.SetIsReviewed).
		exec()
}

func (b *rowBuilder) set(column string, value any) *rowBuilder {
	b.names = append(b.names, column)
	b.args = append(b.args, value)
	return b
}

func (b *rowBuilder) exec() {
	if b.w.err != nil {
		return
	}
	code := b.w.currency
	for i, name := range b.names {
		if name == "currency" {
			code = b.args[i].(string)
		}
		if b.table == "balance_snapshots" && name == "account_id" {
			code = b.w.accounts[b.args[i].(uuid.UUID)]
		}
		if b.table == "transaction_splits" && name == "transaction_id" {
			code = b.w.transactions[b.args[i].(uuid.UUID)]
		}
	}
	if b.table == "holdings" {
		for i, name := range b.names {
			if name == "security_id" {
				code = b.w.securities[b.args[i].(uuid.UUID)]
			}
		}
	}
	for i, v := range b.args {
		converted, err := importValue(v, code)
		if err != nil {
			b.w.err = fmt.Errorf("importer: writing %s.%s: %w", b.table, b.names[i], err)
			return
		}
		b.args[i] = converted
	}
	if b.table == "transactions" {
		for i, name := range b.names {
			if name == "statement_name" || name == "payee" {
				b.names = append(b.names, name+"_folded")
				b.args = append(b.args, domain.FoldName(b.args[i].(string)))
			}
		}
	}
	// Join tables without timestamps retain their schema shape.
	switch b.table {
	case "transaction_tags", "split_tags", "goal_funding_accounts":
	case "document_links":
		b.names = append(b.names, "created_at")
		b.args = append(b.args, b.w.now)
	default:
		b.names = append(b.names, "created_at", "updated_at")
		b.args = append(b.args, b.w.now, b.w.now)
	}
	placeholders := make([]string, len(b.args))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	query := "INSERT INTO " + b.table + " (" + strings.Join(b.names, ",") + ") VALUES (" + strings.Join(placeholders, ",") + ")"
	if _, err := b.w.tx.ExecContext(b.w.ctx, query, b.args...); err != nil {
		b.w.err = fmt.Errorf("importer: writing %s: %w", b.table, err)
	}
}

type importMoney struct {
	value   decimal.Decimal
	present bool
}

func moneyArg(v domain.Money) importMoney              { return importMoney{v.Decimal(), true} }
func optionalMoney(v domain.Money, p bool) importMoney { return importMoney{v.Decimal(), p} }
func optionalPercent(v domain.Rate, p bool) any {
	if !p {
		return nil
	}
	return decimaltext.Format(v)
}
func nullText(v string) any {
	if v == "" {
		return nil
	}
	return v
}
func nullUUID(v uuid.UUID) any {
	if v == uuid.Nil {
		return nil
	}
	return v
}
func nullDate(v domain.Date) any {
	if v.IsZero() {
		return nil
	}
	return v.String()
}
func rateArg(v domain.Rate) string { return decimaltext.Format(v) }
func nullRateArg(v domain.Rate, p bool) any {
	if !p {
		return nil
	}
	return decimaltext.Format(v)
}
func importValue(v any, code string) (any, error) {
	switch x := v.(type) {
	case importMoney:
		if !x.present {
			return nil, nil
		}
		m, err := minorunits.FromDecimal(x.value, code)
		return m.Amount, err
	case time.Time:
		return x.UnixMilli(), nil
	case *time.Time:
		if x == nil {
			return nil, nil
		}
		return x.UnixMilli(), nil
	}
	if v != nil && reflect.TypeOf(v).Kind() == reflect.Slice {
		data, err := json.Marshal(v)
		return string(data), err
	}
	return v, nil
}

// jsonArg encodes a JSON column. A value that will not encode fails the
// import: writing NULL would drop what the export held without a word.
func (w *writer) jsonArg(column string, value []any) any {
	if len(value) == 0 {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil && w.err == nil {
		w.err = fmt.Errorf("importer: encoding %s: %w", column, err)
	}
	return string(encoded)
}

// ErrDuplicate is a write refused for what the database already holds: the
// export imported before, or a space that is not empty.
var ErrDuplicate = errors.New("importer: refusing to import beside existing data")

// Refusal is why Write would refuse this run, given what the database holds,
// or nil. It opens no transaction, so a preview can ask it.
func Refusal(ctx context.Context, db *store.Store, mapped *Mapped) error {
	if err := refuseDuplicateImport(ctx, db, mapped); err != nil {
		return err
	}
	if mapped.IntoExisting {
		return refuseOccupiedSpace(ctx, db, mapped.SpaceID)
	}
	return nil
}

// occupiedTables are what a space must not already hold for an import to fill
// it: the imported rows would sit beside them, and nothing could tell the two
// ledgers apart afterwards.
var occupiedTables = []struct{ table, noun string }{
	{"accounts", "accounts"},
	{"transactions", "transactions"},
	{"categories", "categories"},
	{"tags", "tags"},
	{"rules", "rules"},
	{"series", "bills and income"},
	{"goals", "savings goals"},
	{"watchlists", "watchlists"},
	{"spending_plan_months", "spending plan months"},
	{"holdings", "holdings"},
	{"institutions", "institutions"},
}

// refuseOccupiedSpace rejects filling a space that holds anything the import
// writes, deleted rows included. The default category tree a new space starts
// with does not count while it is untouched: the import writes the export's
// categories in its place.
func refuseOccupiedSpace(ctx context.Context, db *store.Store, space store.SpaceID) error {
	household, err := db.HouseholdDB(ctx, space)
	if err != nil {
		return err
	}
	var held []string
	for _, one := range occupiedTables {
		var count int
		if err := household.Read().QueryRowContext(ctx, `SELECT count(*) FROM `+one.table).Scan(&count); err != nil {
			return fmt.Errorf("importer: checking the space is empty: %w", err)
		}
		if count > 0 && one.table == "categories" {
			untouched, err := db.OnlyUntouchedDefaults(ctx, space, domain.DefaultCategories)
			if err != nil {
				return fmt.Errorf("importer: checking the space is empty: %w", err)
			}
			if untouched {
				continue
			}
		}
		if count > 0 {
			held = append(held, fmt.Sprintf("%d %s", count, one.noun))
		}
	}
	if len(held) == 0 {
		return nil
	}
	return fmt.Errorf("%w: this space already holds %s. A Simplifi import fills an empty "+
		"space; start a new space and import into that", ErrDuplicate, strings.Join(held, ", "))
}

// refuseDuplicateImport rejects a run that would create a second copy of the
// same dataset. Nothing ties a space to its export, so "the owner already has a
// space with this name" is the best available fingerprint. The space a run
// fills is not a copy of itself.
func refuseDuplicateImport(ctx context.Context, db *store.Store, mapped *Mapped) error {
	owner := ""
	for _, user := range mapped.Users {
		if user.Email != "" {
			owner = user.Email
			break
		}
	}
	if owner == "" || mapped.Space.Name == "" {
		return nil
	}
	var existing string
	server, err := db.ServerDB()
	if err != nil {
		return err
	}
	err = server.Read().QueryRowContext(ctx, `
		SELECT s.name
		FROM households s
		JOIN memberships mem ON mem.household_id = s.id
		JOIN users u ON u.id = mem.user_id
		WHERE lower(u.email) = lower(?) AND s.name = ? AND NOT s.is_deleted AND s.id <> ?
		LIMIT 1`, owner, mapped.Space.Name, uuid.UUID(mapped.SpaceID)).Scan(&existing)
	if err == nil {
		return fmt.Errorf("%w: a space named %q already exists for %s; "+
			"this export has probably been imported before. Delete that space "+
			"first if you meant to start over", ErrDuplicate, existing, owner)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("importer: checking for an existing space: %w", err)
	}
	return nil
}
