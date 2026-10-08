package importer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The only part of the import that touches a database. Mapping sets every
// primary key, so writing is a sequence of inserts whose order IS the foreign
// key dependency graph. A run with errors is never written.

// ErrRefused is returned rather than writing a ledger the report says not to
// trust.
var ErrRefused = errors.New("importer: refusing to write a run with errors")

// Write inserts one mapped export inside a single transaction: a half-written
// ledger is worse than none. Many of these tables have no store helper, so it
// writes its own statements.
func Write(ctx context.Context, db *store.Store, mapped *Mapped) error {
	if !mapped.Report.OK() {
		return fmt.Errorf("%w: %d errors; nothing was written. See the report for the store, "+
			"record and field of each", ErrRefused, len(mapped.Report.Errors))
	}
	if mapped.Space == nil {
		return fmt.Errorf("importer: nothing to write: the export produced no space")
	}
	if db.Pool() == nil {
		return fmt.Errorf("importer: writing needs a pool, not a transaction-scoped store")
	}
	if err := Refusal(ctx, db, mapped); err != nil {
		return err
	}
	return db.InTx(ctx, func(tx *store.Store) error {
		if mapped.IntoExisting {
			// Asked again inside the write transaction: two imports into one
			// empty space would otherwise both find it empty.
			if err := refuseOccupiedSpace(ctx, tx, mapped.SpaceID); err != nil {
				return err
			}
		}
		if err := writeAll(ctx, tx.Conn(), mapped); err != nil {
			return err
		}
		return tx.FileTransferLegs(ctx, mapped.SpaceID)
	})
}

func writeAll(ctx context.Context, tx store.DB, m *Mapped) error {
	space := uuid.UUID(m.SpaceID)
	w := &writer{ctx: ctx, tx: tx, space: space}

	// 1. The tenancy root. Everything below carries space_id. An existing
	// space takes the dataset's name, so the duplicate guard and --rules-only
	// find it as they find a space the import created.
	if m.IntoExisting {
		if _, err := tx.Exec(ctx, `
			UPDATE spaces SET name = $2, primary_currency = $3, timezone = $4, updated_at = now()
			WHERE id = $1`, space, m.Space.Name, m.Space.PrimaryCurrency, m.Space.Timezone); err != nil {
			return fmt.Errorf("importer: writing spaces: %w", err)
		}
	} else {
		w.row("spaces").
			set("id", space).
			set("name", m.Space.Name).
			set("primary_currency", m.Space.PrimaryCurrency).
			set("timezone", m.Space.Timezone).
			set("is_deleted", false).
			exec()
	}

	// 2. Users, before the memberships and alert rules that name one. Email
	// is unique install-wide, so an existing owner is joined, and the id the
	// database hands back replaces the one mapping allocated.
	owners := make(map[uuid.UUID]uuid.UUID, len(m.Users))
	for _, user := range m.Users {
		var existing uuid.UUID
		err := tx.QueryRow(ctx, `
			INSERT INTO users (id, email, is_active, locale, theme)
			VALUES ($1, lower($2), $3, 'en', 'system')
			ON CONFLICT (lower(email)) DO UPDATE SET updated_at = now()
			RETURNING id`, user.ID, strings.TrimSpace(user.Email), user.IsActive).Scan(&existing)
		if err != nil {
			return fmt.Errorf("importer: writing users: %w", err)
		}
		owners[user.ID] = existing
	}
	ownerOf := func(id uuid.UUID) uuid.UUID {
		if found, ok := owners[id]; ok {
			return found
		}
		return id
	}

	// 3. Memberships and institutions: both need the space, neither needs the
	// other.
	for _, membership := range m.Memberships {
		w.row("memberships").
			set("id", membership.ID).
			set("space_id", space).
			set("user_id", ownerOf(membership.UserID)).
			set("role", string(membership.Role)).
			set("accepted_at", membership.AcceptedAt).
			exec()
	}
	for _, institution := range m.Institutions {
		w.row("institutions").
			set("id", institution.ID).
			set("space_id", space).
			set("name", institution.Name).
			set("external_id", dbconv.NullText(institution.ExternalID)).
			set("domain", dbconv.NullText(institution.Domain)).
			set("logo_url", dbconv.NullText(institution.LogoURL)).
			set("is_deleted", institution.IsDeleted).
			exec()
	}

	// 4. Accounts, which name an institution.
	for order, account := range m.Accounts {
		w.row("accounts").
			set("id", account.ID).
			set("space_id", space).
			set("institution_id", dbconv.NullUUID(account.InstitutionID)).
			set("name", account.Name).
			set("description", dbconv.NullText(account.Description)).
			set("notes", dbconv.NullText(account.Notes)).
			set("kind", string(account.Kind)).
			set("type", account.Type).
			set("usage_type", dbconv.NullText(account.UsageType)).
			set("currency", account.Currency).
			set("masked_number", dbconv.NullText(account.MaskedNumber)).
			set("sort_order", order).
			set("provider_balance", dbconv.NullMoney(account.ProviderBalance, account.HasProviderBalance)).
			set("provider_balance_at", account.ProviderBalanceAt).
			set("goal_balance", dbconv.Money(account.GoalBalance)).
			set("excluded_from_reports", account.ExcludedFromReports).
			set("excluded_from_spending_plan", account.ExcludedFromSpendingPlan).
			set("excluded_from_account_bar", account.ExcludedFromAccountBar).
			set("include_in_net_worth", account.IncludeInNetWorth).
			set("exclude_bank_pending", account.ExcludeBankPending).
			set("is_closed", account.IsClosed).
			set("closed_on", dbconv.NullDate(account.ClosedOn)).
			set("is_deleted", account.IsDeleted).
			exec()
	}

	// 5. Categories, parent-first: parent_id references this same table and
	// the database checks it on the insert. An existing space holds none but the
	// untouched default tree (refuseOccupiedSpace), which the export's own
	// categories take the place of.
	if m.IntoExisting {
		if _, err := tx.Exec(ctx, `DELETE FROM categories WHERE space_id = $1`, space); err != nil {
			return fmt.Errorf("importer: clearing the default categories: %w", err)
		}
	}
	for order, category := range parentsFirst(m.Categories) {
		w.row("categories").
			set("id", category.ID).
			set("space_id", space).
			set("parent_id", dbconv.NullUUID(category.ParentID)).
			set("name", category.Name).
			set("kind", string(category.Kind)).
			set("known_category_id", dbconv.NullText(category.KnownCategoryID)).
			set("txf_id", dbconv.NullText(category.TxfID)).
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
			set("space_id", space).
			set("name", tag.Name).
			set("color", dbconv.NullText(tag.Color)).
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
			set("space_id", space).
			set("account_id", series.AccountID).
			set("category_id", dbconv.NullUUID(series.CategoryID)).
			set("kind", string(series.Kind)).
			set("description", series.Description).
			set("display_name", dbconv.NullText(series.DisplayName)).
			set("amount", dbconv.Money(series.Amount)).
			set("currency", series.Currency).
			set("alias", string(series.Alias)).
			set("frequency", dbconv.NullText(string(series.Frequency))).
			set(`"interval"`, series.Interval).
			set("by_month_day", store.NonNil(series.ByMonthDay)).
			set("by_day", store.NonNil(series.ByDay)).
			set("start_on", series.StartOn).
			set("end_on", dbconv.NullDate(series.EndOn)).
			set("next_due_on", dbconv.NullDate(series.NextDueOn)).
			set("override_next_due_on", dbconv.NullDate(series.OverrideNextDueOn)).
			set("override_next_amount", dbconv.NullMoney(series.OverrideNextAmount, series.HasOverrideNextAmount)).
			set("auto_adjust_due_on", series.AutoAdjustDueOn).
			set("reminder_days", series.ReminderDays).
			set("auto_accept_days", series.AutoAcceptDays).
			set("match_criteria", series.MatchCriteria).
			set("match_amount_min", dbconv.NullMoney(series.MatchAmountMin, series.HasMatchAmountMin)).
			set("match_amount_max", dbconv.NullMoney(series.MatchAmountMax, series.HasMatchAmountMax)).
			set("learned_descriptions", store.NonNil(series.LearnedDescriptions)).
			set("template_payee", dbconv.NullText(series.TemplatePayee)).
			set("template_notes", dbconv.NullText(series.TemplateNotes)).
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
			set("space_id", space).
			set("account_id", txn.AccountID).
			set("date", txn.Date).
			set("effective_date", dbconv.NullDate(txn.EffectiveDate)).
			set("amount", dbconv.Money(txn.Amount)).
			set("currency", txn.Currency).
			set("statement_name", txn.StatementName).
			set("payee", txn.Payee).
			set("notes", dbconv.NullText(txn.Notes)).
			set("check_number", dbconv.NullText(txn.CheckNumber)).
			set("category_id", dbconv.NullUUID(txn.CategoryID)).
			set("source", string(txn.Source)).
			set("is_pending", txn.IsPending).
			set("is_deleted", txn.IsDeleted).
			set("is_reviewed", txn.IsReviewed).
			set("excluded_from_reports", txn.ExcludedFromReports).
			set("excluded_from_spending_plan", txn.ExcludedFromSpendingPlan).
			set("is_bill", txn.IsBill).
			set("is_subscription", txn.IsSubscription).
			set("transfer_pair_id", dbconv.NullUUID(txn.TransferPairID)).
			set("user_flag", dbconv.NullText(txn.UserFlag)).
			set("user_flag_note", dbconv.NullText(txn.UserFlagNote)).
			set("series_id", dbconv.NullUUID(txn.SeriesID)).
			set("series_due_on", dbconv.NullDate(txn.SeriesDueOn)).
			set("estimate_status", dbconv.NullText(txn.EstimateStatus)).
			set("accepted_on", dbconv.NullDate(txn.AcceptedOn)).
			set("expires_on", dbconv.NullDate(txn.ExpiresOn)).
			set("rule_id", dbconv.NullUUID(txn.RuleID)).
			set("balance", dbconv.NullMoney(txn.Balance, txn.HasBalance)).
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
				set("space_id", space).
				set("transaction_id", txn.ID).
				set(`"position"`, split.Position).
				set("amount", dbconv.Money(split.Amount)).
				set("category_id", dbconv.NullUUID(split.CategoryID)).
				set("memo", dbconv.NullText(split.Memo)).
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
			set("space_id", space).
			set("content_sha256", store.UnhashedDocumentHash(attachment.ID)).
			set("content_type", attachment.ContentType).
			set("size_bytes", attachment.SizeBytes).
			set("filename", attachment.Filename).
			set("storage_key", attachment.StorageKey).
			set("source", store.DocumentSourceUpload).
			exec()
		w.row("document_links").
			set("document_id", attachment.ID).
			set("space_id", space).
			set("kind", string(store.DocumentLinkTransaction)).
			set("target_id", attachment.TransactionID).
			set("role", store.DocumentRoleAttachment).
			exec()
	}

	// 11. Goals, and the join table that names both a goal and an account.
	for _, goal := range m.Goals {
		w.row("goals").
			set("id", goal.ID).
			set("space_id", space).
			set("account_id", goal.AccountID).
			set("name", goal.Name).
			set("description", dbconv.NullText(goal.Description)).
			set("notes", dbconv.NullText(goal.Notes)).
			set("kind", dbconv.NullText(goal.Kind)).
			set("image_url", dbconv.NullText(goal.ImageURL)).
			set("tag_id", dbconv.NullUUID(goal.TagID)).
			set("target_amount", dbconv.Money(goal.TargetAmount)).
			set("target_on", dbconv.NullDate(goal.TargetOn)).
			set("start_on", dbconv.NullDate(goal.StartOn)).
			set("completed_on", dbconv.NullDate(goal.CompletedOn)).
			set("contribution_amount", dbconv.Money(goal.ContributionAmount)).
			set("contribution_frequency", dbconv.NullText(goal.ContributionFrequency)).
			set("contributed_this_month", dbconv.Money(goal.ContributedThisMonth)).
			set("saved_so_far", dbconv.Money(goal.SavedSoFar)).
			set("spent", dbconv.Money(goal.Spent)).
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
			set("space_id", space).
			set("month", month.Month).
			set("calculated_rollover_amount", dbconv.Money(month.CalculatedRollover))
		for _, name := range []string{"income", "bills", "subscriptions", "transfer", "goals", "planned_spending", "spent"} {
			bucket := month.bucket(name)
			row.set("calculated_"+name+"_amount", dbconv.Money(bucket.Calculated)).
				set(name+"_txn_ids", store.NonNil(bucket.TxnIDs)).
				set("excluded_"+name+"_txn_ids", store.NonNil(bucket.ExcludedTxnIDs)).
				set("overwritten_"+name+"_amount", dbconv.NullMoney(bucket.Overwritten, bucket.HasOverwritten)).
				set("reset_overwritten_"+name, bucket.ResetOverwritten)
		}
		row.set("set_aside", dbconv.Money(month.SetAside)).
			set("total_to_spend_amount", dbconv.Money(month.TotalToSpend)).
			set("left_to_spend_amount", dbconv.Money(month.LeftToSpend)).
			set("projected_other_spending", dbconv.Money(month.ProjectedOtherSpending)).
			set("projection_type", string(month.ProjectionType)).
			set("projection_start_date", dbconv.NullDate(month.ProjectionStartDate)).
			set("projection_end_date", dbconv.NullDate(month.ProjectionEndDate)).
			set("projection_buffer", dbconv.Money(month.ProjectionBuffer)).
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
			set("space_id", space).
			set("spending_plan_month_id", envelope.SpendingPlanMonthID).
			set("filter_id", envelope.FilterID).
			set("recurring_group_id", dbconv.NullUUID(envelope.RecurringGroupID)).
			set("name", envelope.Name).
			set("target_amount", dbconv.Money(envelope.TargetAmount)).
			set("overwritten_target_amount", dbconv.NullMoney(envelope.OverwrittenTargetAmount, envelope.HasOverwrittenTarget)).
			set("calculated_spent_amount", dbconv.Money(envelope.CalculatedSpentAmount)).
			set("rollover_amount", dbconv.Money(envelope.RolloverAmount)).
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
			set("space_id", space).
			set("filter_id", watchlist.FilterID).
			set("name", watchlist.Name).
			set("emoji", dbconv.NullText(watchlist.Emoji)).
			set("target_amount", dbconv.NullMoney(watchlist.TargetAmount, watchlist.HasTarget)).
			set("period", watchlist.Period).
			set("start_date", dbconv.NullDate(watchlist.StartDate)).
			set("end_date", dbconv.NullDate(watchlist.EndDate)).
			set("is_deleted", watchlist.IsDeleted).
			exec()
	}

	// 14. Securities, then the holdings that name one and an account.
	for _, security := range m.Securities {
		w.row("securities").
			set("id", security.ID).
			set("space_id", space).
			set("symbol", security.Symbol).
			set("name", security.Name).
			set("kind", security.Kind).
			set("exchange", dbconv.NullText(security.Exchange)).
			set("currency", security.Currency).
			set("cusip", dbconv.NullText(security.CUSIP)).
			set("isin", dbconv.NullText(security.ISIN)).
			set("last_price", nullRateArg(security.LastPrice, security.HasLastPrice)).
			set("last_price_at", security.LastPriceAt).
			set("prior_close", nullRateArg(security.PriorClose, security.HasPriorClose)).
			set("is_deleted", security.IsDeleted).
			exec()
	}
	for _, holding := range m.Holdings {
		w.row("holdings").
			set("id", holding.ID).
			set("space_id", space).
			set("account_id", holding.AccountID).
			set("security_id", holding.SecurityID).
			set("external_id", dbconv.NullText(holding.ExternalID)).
			set("shares", rateArg(holding.Shares)).
			set("cost_basis", dbconv.NullMoney(holding.CostBasis, holding.HasCostBasis)).
			set("average_cost", nullRateArg(holding.AverageCost, holding.HasAverage)).
			set("is_cost_basis_complete", holding.IsCostBasisComplete).
			set("market_value", dbconv.NullMoney(holding.MarketValue, holding.HasMarketValue)).
			set("as_of", dbconv.NullDate(holding.AsOf)).
			exec()
	}

	// 15. Balance snapshots and alert rules.
	for _, snapshot := range m.BalanceSnapshots {
		w.row("balance_snapshots").
			set("id", snapshot.ID).
			set("space_id", space).
			set("account_id", snapshot.AccountID).
			set("as_of", snapshot.AsOf).
			set("balance", dbconv.Money(snapshot.Balance)).
			set("is_imported", true).
			exec()
	}
	if m.IntoExisting && len(m.AlertRules) > 0 && w.err == nil {
		// The owner may have set notifications in the empty space already;
		// the settings they kept in Simplifi replace them rather than collide.
		for _, owner := range owners {
			if _, err := tx.Exec(ctx, `DELETE FROM alert_rules WHERE space_id = $1 AND user_id = $2`,
				space, owner); err != nil {
				return fmt.Errorf("importer: writing alert_rules: %w", err)
			}
		}
	}
	for _, rule := range m.AlertRules {
		w.row("alert_rules").
			set("id", rule.ID).
			set("space_id", space).
			set("user_id", ownerOf(rule.UserID)).
			set("alert_type", rule.AlertType).
			set("account_id", dbconv.NullUUID(rule.AccountID)).
			set("is_enabled", rule.IsEnabled).
			set("is_paused", rule.IsPaused).
			set("channel_email", rule.ChannelEmail).
			set("channel_push", rule.ChannelPush).
			set("channel_in_app", rule.ChannelInApp).
			set("threshold_amount", dbconv.NullMoney(rule.ThresholdAmount, rule.HasThresholdAmount)).
			set("threshold_count", rule.ThresholdCount).
			set("threshold_pct", dbconv.NullNumeric(rule.ThresholdPct, rule.HasThresholdPct)).
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
	ctx   context.Context
	tx    store.DB
	space uuid.UUID
	err   error
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
		set("space_id", space).
		set("name", dbconv.NullText(filter.Name)).
		set("scope", filter.Scope).
		set("query_text", dbconv.NullText(filter.QueryText)).
		set("is_deleted", filter.IsDeleted).
		exec()
	w.filterItems(space, filter.Items)
}

func (w *writer) filterItems(space uuid.UUID, items []store.FilterItem) {
	for _, item := range items {
		w.row("filter_items").
			set("id", item.ID).
			set("space_id", space).
			set("filter_id", item.FilterID).
			set("field", item.Field).
			set("operator", item.Operator).
			set("group_index", item.GroupIndex).
			set(`"position"`, item.Position).
			set("negated", item.Negated).
			set("value_ids", store.NonNil(item.ValueIDs)).
			set("value_texts", store.NonNil(item.ValueTexts)).
			set("text", dbconv.NullText(item.Text)).
			set("amount_min", dbconv.NullMoney(item.AmountMin, item.HasAmountMin)).
			set("amount_max", dbconv.NullMoney(item.AmountMax, item.HasAmountMax)).
			set("date_from", dbconv.NullDate(item.DateFrom)).
			set("date_to", dbconv.NullDate(item.DateTo)).
			set("date_preset", dbconv.NullText(item.DatePreset)).
			set("state", item.State).
			exec()
	}
}

func (w *writer) rule(space uuid.UUID, rule *Rule) {
	w.row("rules").
		set("id", rule.ID).
		set("space_id", space).
		set("name", rule.Name).
		set("filter_id", rule.FilterID).
		set("priority", rule.Priority).
		set("is_active", rule.IsActive).
		set("is_deleted", rule.IsDeleted).
		set("source_ref", rule.SourceRef).
		set("set_payee", dbconv.NullText(rule.SetPayee)).
		set("set_category_id", dbconv.NullUUID(rule.SetCategoryID)).
		set("add_tag_ids", store.NonNil(rule.AddTagIDs)).
		set("set_notes", dbconv.NullText(rule.SetNotes)).
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
	placeholders := make([]string, len(b.args))
	for i := range b.args {
		placeholders[i] = "$" + strconv.Itoa(i+1)
	}
	sql := "INSERT INTO " + b.table + " (" + strings.Join(b.names, ", ") + ") VALUES (" +
		strings.Join(placeholders, ", ") + ")"
	if _, err := b.w.tx.Exec(b.w.ctx, sql, b.args...); err != nil {
		b.w.err = fmt.Errorf("importer: writing %s: %w", b.table, err)
	}
}

// -- value conversion -----------------------------------------------------

func rateArg(r domain.Rate) dbconv.Number { return dbconv.Numeric(r) }

func nullRateArg(r domain.Rate, present bool) dbconv.Number {
	if !present {
		return dbconv.Number{}
	}
	return rateArg(r)
}

// jsonArg encodes a JSON column. A value that will not encode fails the
// import: writing NULL would drop what the export held without a word.
func (w *writer) jsonArg(column string, value []any) json.RawMessage {
	if len(value) == 0 {
		return nil
	}
	encoded, err := json.Marshal(value)
	if err != nil && w.err == nil {
		w.err = fmt.Errorf("importer: encoding %s: %w", column, err)
	}
	return encoded
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
	var held []string
	for _, one := range occupiedTables {
		var count int
		if err := db.Conn().QueryRow(ctx, `SELECT count(*) FROM `+one.table+` WHERE space_id = $1`,
			uuid.UUID(space)).Scan(&count); err != nil {
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
	err := db.Pool().QueryRow(ctx, `
		SELECT s.name
		FROM spaces s
		JOIN memberships mem ON mem.space_id = s.id
		JOIN users u ON u.id = mem.user_id
		WHERE lower(u.email) = lower($1) AND s.name = $2 AND NOT s.is_deleted AND s.id <> $3
		LIMIT 1`, owner, mapped.Space.Name, uuid.UUID(mapped.SpaceID)).Scan(&existing)
	if err == nil {
		return fmt.Errorf("%w: a space named %q already exists for %s; "+
			"this export has probably been imported before. Delete that space "+
			"first if you meant to start over", ErrDuplicate, existing, owner)
	}
	if !errors.Is(err, sqlitedb.ErrNoRows) {
		return fmt.Errorf("importer: checking for an existing space: %w", err)
	}
	return nil
}
