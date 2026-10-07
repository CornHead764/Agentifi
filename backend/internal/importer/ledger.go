package importer

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Steps 7 to 9: the ledger, the series, and the rules. A transaction's
// stModelId and transactionRuleId name a series and a rule that later steps
// create, so both links are stashed and resolved when their store arrives.

// -- 7. transactions, splits, attachments, transfer pairs -------------

func (m *Mapper) mapTransactions() {
	const storeName = "transactionStore"
	documents, err := m.export.Records(m.datasetID, "documentStore")
	if err != nil {
		m.report.Errorf("documentStore", "", "", "%s", err.Error())
	} else {
		m.documents = documents
	}
	m.report.Read["documentStore"] = len(m.documents)

	m.walk(storeName, kindTransaction, m.mapTransaction)
	m.pairTransfers(storeName)
}

func (m *Mapper) mapTransaction(sourceID string, r *record) {
	const storeName = "transactionStore"
	if r.present("type") || r.present("subtype") {
		m.report.Warnf(KindNoColumn, storeName, sourceID, "type",
			"an investment action (type/subtype) has no column in the ledger")
	}

	statementName := r.text("payee", 500)
	allocations := m.allocations(r)

	categoryID := uuid.Nil
	if len(allocations) == 0 {
		categoryID = m.categoryFor(storeName, sourceID, r)
	}

	balance, hasBalance := r.optMoney("balance")
	accountID := m.resolveRef(r, kindAccount, "accountId")
	// Simplifi leaves investment, loan and asset rows unreviewed forever; ours
	// reads the flag account-wide, so those rows are born reviewed here or
	// they flood the review queue.
	bornReviewed := false
	if account, found := m.accountsByID[accountID]; found {
		bornReviewed = account.Kind.BornReviewed()
	}
	txn := &store.Transaction{
		ID:        m.allocate(kindTransaction, sourceID),
		AccountID: accountID,
		// Deliberately no ExternalID: nothing the import writes has a SimpleFIN
		// id, which is why the relink dedupes on date and amount as well as on
		// the id.
		Date:          r.date("postedOn"),
		Amount:        r.money("amount"),
		Currency:      r.textOr("currency", m.currency, 3),
		StatementName: statementName,
		// Simplifi's names invert ours: their payee is the bank's string,
		// their renamedPayee is what a person reads.
		Payee:       r.textOr("renamedPayee", textutil.Clip(statementName, 255), 255),
		Notes:       r.text("memo", 0),
		CheckNumber: m.checkNumber(r),
		CategoryID:  categoryID,
		Source:      domain.SourceSimplifiImport,

		IsPending:  m.isPending(r),
		IsReviewed: r.flag("isReviewed", false) || bornReviewed,
		IsDeleted:  r.flag("isDeleted", false),

		IsBill:         r.flag("isBill", false),
		IsSubscription: r.flag("isSubscription", false),

		// Independent of each other and of the account's pair.
		ExcludedFromReports:      r.flag("isExcludedFromReports", false),
		ExcludedFromSpendingPlan: r.flag("isExcludedFromF2S", false),

		UserFlag:     m.userFlag(r),
		UserFlagNote: r.text("userFlagNote", 0),

		EstimateStatus: m.estimateStatus(r),
		AcceptedOn:     r.optDate("acceptedOn"),
		ExpiresOn:      r.optDate("expireOn"),

		Balance:    balance,
		HasBalance: hasBalance,
	}
	txn.TagIDs = m.tagsFor(r, "tags")
	if r.failed() {
		return
	}

	m.out.Transactions = append(m.out.Transactions, txn)
	m.txnBySourceID[sourceID] = txn
	m.report.count("transactions", 1)

	m.splits(storeName, sourceID, txn, r, allocations)
	m.attachments(storeName, sourceID, txn, r)
	if r.failed() {
		return
	}

	// Only transfer pairs rows. matchedTxn links an entry to the downloaded
	// row it was merged into, which the merge consumed; pairing on it builds
	// false pairs, which drop both rows from every income and expense total.
	if partner := r.reference("transfer"); partner != "" {
		m.transferLinks = append(m.transferLinks, transferLink{sourceID, partner, "transfer"})
	}

	if stModelID := r.reference("stModelId"); stModelID != "" {
		dueOn := r.optDate("stDueOn")
		m.seriesLinks = append(m.seriesLinks, seriesLink{
			txn:        txn,
			sourceID:   sourceID,
			stModelID:  stModelID,
			occurrence: dueOn,
			hasDueOn:   !dueOn.IsZero(),
		})
	}
	if ruleID := r.reference("transactionRuleId"); ruleID != "" {
		m.ruleLinks = append(m.ruleLinks, ruleLink{
			txn: txn, sourceID: sourceID, ruleID: ruleID,
			renamed: r.present("renamedPayee"), forecast: m.isForecast(r),
		})
	}
}

// isPending reports whether the bank has authorized this charge without
// settling it. Simplifi's scheduled-transaction forecasts also arrive as
// PENDING, told apart only by source; they are estimates (see
// estimateStatus), not pending.
func (m *Mapper) isPending(r *record) bool {
	if m.isForecast(r) {
		return false
	}
	if !r.present("state") {
		return false
	}
	return lookup(r, transactionStates, "state", r.value("state"), "transaction state")
}

// estimateStatus is Simplifi's own value where it set one, and otherwise the
// projected marker for a row it generated from a schedule. Simplifi leaves
// estimateStatus undefined on its forecasts; source is the only marker.
func (m *Mapper) estimateStatus(r *record) string {
	if status := r.text("estimateStatus", 16); status != "" {
		return status
	}
	if m.isForecast(r) {
		return store.ProjectedEstimate
	}
	return ""
}

// isForecast reports whether Simplifi generated this row from a schedule
// rather than recording money that moved. A record with no source is real.
func (m *Mapper) isForecast(r *record) bool {
	if !r.present("source") {
		return false
	}
	return lookup(r, transactionSources, "source", r.value("source"), "transaction source")
}

func (m *Mapper) userFlag(r *record) string {
	if !r.present("userFlag") {
		return ""
	}
	return lookup(r, userFlags, "userFlag", r.value("userFlag"), "flag colour")
}

// seriesIsActive keeps Simplifi's unconfirmed suggestions out of the plan. A
// series with isUserVerified false is a detection nobody accepted; imported
// active it would bill every future month, so it arrives inactive.
func (m *Mapper) seriesIsActive(storeName, sourceID string, r *record) bool {
	if !r.flag("isActive", true) {
		return false
	}
	if r.present("isUserVerified") && !r.flag("isUserVerified", true) {
		m.report.Warnf(KindUnconfirmed, storeName, sourceID, "isUserVerified",
			"a suggestion the user never confirmed; imported inactive so it cannot "+
				"reach the spending plan or a projection")
		return false
	}
	return true
}

// seriesKind reads type, and derives it from the template when the export
// leaves it undefined, as it can on every series.
func (m *Mapper) seriesKind(r *record, template *record, accountID uuid.UUID) domain.SeriesKind {
	if r.present("type") {
		return lookup(r, seriesKinds, "type", r.value("type"), "series kind")
	}
	if kind, _ := template.taggedRef("coa"); kind == coaAccount {
		return domain.SeriesTransfer
	}
	// Money arriving at a credit card is a payment of that card, never income.
	// A card's payment series can carry a BALANCE_ADJUSTMENT coa and a
	// positive amount, which the amount test alone reads as a paycheck.
	if account, found := m.accountsByID[accountID]; found && account.Kind == domain.KindCreditCard {
		if amount, has := template.optMoney("amount"); has && !amount.IsNegative() {
			return domain.SeriesCreditCardPayment
		}
	}
	if template.flag("isSubscription", false) {
		return domain.SeriesSubscription
	}
	if template.flag("isBill", false) {
		return domain.SeriesBill
	}
	if amount, has := template.optMoney("amount"); has && !amount.IsNegative() {
		return domain.SeriesIncome
	}
	return domain.SeriesBill
}

// checkNumber reads the check field, which the export writes as
// {number, memo}; the check's memo has no column of its own.
func (m *Mapper) checkNumber(r *record) string {
	switch typed := r.value("check").(type) {
	case nil:
		return ""
	case string:
		return textutil.Clip(typed, 32)
	case map[string]any:
		number, _ := typed["number"].(string)
		return textutil.Clip(number, 32)
	default:
		r.fail("check", "expected text or {number, memo}, found %s", typeName(typed))
		return ""
	}
}

// coaCategory resolves a coa reference to a category, or to nothing. Only a
// CATEGORY-tagged coa names one; an ACCOUNT-tagged coa marks a transfer leg,
// whose partner arrives through the transfer field.
func (m *Mapper) coaCategory(r *record, field string) uuid.UUID {
	kind, id := r.taggedRef(field)
	if r.failed() || id == "" {
		return uuid.Nil
	}
	switch kind {
	case coaCategory:
		return m.resolveID(r, kindCategory, field, id)
	case coaAccount, coaUncategorized, coaBalanceAdjustment:
		return uuid.Nil
	default:
		r.fail(field, "unknown coa kind: %q", kind)
		return uuid.Nil
	}
}

// categoryFor reads coa first and knownCategoryId, the stable system marker,
// second. Losing the marker turns every credit-card payment into an expense.
func (m *Mapper) categoryFor(storeName, recordID string, r *record) uuid.UUID {
	kind, id := r.taggedRef("coa")
	if r.failed() {
		return uuid.Nil
	}
	switch kind {
	case coaCategory:
		if id != "" {
			return m.resolveID(r, kindCategory, "coa", id)
		}
	case coaAccount, coaUncategorized, coaBalanceAdjustment:
		// coa has answered; a stale knownCategoryId must not override it.
		return uuid.Nil
	case "":
	default:
		r.fail("coa", "unknown coa kind: %q", kind)
		return uuid.Nil
	}

	known := r.text("knownCategoryId", 64)
	if known == "" {
		return uuid.Nil
	}
	found, ok := m.knownCategories[known]
	if !ok {
		// Refusing the row would lose its amount too, so it lands uncategorised.
		m.report.Warnf(KindMissingReference, storeName, recordID, r.prefix+"knownCategoryId",
			"names system category %q, which is not in categoryStore; the row imports uncategorised", known)
		return uuid.Nil
	}
	return found
}

// allocations reads the split lines, whichever of the three shapes split
// arrives in. Simplifi writes `split` as a wrapper carrying `items`, and
// `allocations` as the undefined sentinel; anything outside the three shapes
// is an error rather than a silent drop.
func (m *Mapper) allocations(r *record) []map[string]any {
	raw := r.value("allocations")
	if raw == nil {
		raw = r.value("split")
	}
	if raw == nil {
		return nil
	}
	if flag, isBool := raw.(bool); isBool {
		if !flag {
			return nil
		}
		r.fail("split", "is set but the record carries no allocations")
		return nil
	}
	if wrapper, isObject := raw.(map[string]any); isObject {
		raw = wrapper["allocations"]
		if raw == nil {
			raw = wrapper["items"]
		}
	}
	items, isList := raw.([]any)
	if !isList {
		r.fail("allocations", "expected a list, found %s", typeName(raw))
		return nil
	}
	out := make([]map[string]any, 0, len(items))
	for _, item := range items {
		data, isObject := item.(map[string]any)
		if !isObject {
			r.fail("allocations", "expected an object, found %s", typeName(item))
			return nil
		}
		out = append(out, data)
	}
	return out
}

func (m *Mapper) splits(storeName, recordID string, txn *store.Transaction, r *record, allocations []map[string]any) {
	for position, data := range allocations {
		m.checkNestedFields(storeName, recordID, "allocations.", data, allocationFields)
		item := r.nested("allocations", data)
		split := store.Split{
			ID:            uuid.New(),
			TransactionID: txn.ID,
			Position:      position,
			Amount:        item.money("amount"),
			CategoryID:    m.categoryFor(storeName, recordID, item),
			Memo:          item.text("memo", 0),
		}
		split.TagIDs = m.tagsFor(item, "tags")
		if r.failed() {
			return
		}
		txn.Splits = append(txn.Splits, split)
		m.report.count("transaction_splits", 1)
	}
}

func (m *Mapper) tagsFor(r *record, field string) []uuid.UUID {
	sourceIDs := r.stringList(field)
	if len(sourceIDs) == 0 || r.failed() {
		return nil
	}
	out := make([]uuid.UUID, 0, len(sourceIDs))
	for _, tagID := range sourceIDs {
		tag, found := m.tagsBySourceID[tagID]
		if !found {
			r.fail(field, "names tag %q, which tagStore does not have", tagID)
			return nil
		}
		out = append(out, tag.ID)
	}
	return out
}

func (m *Mapper) attachments(storeName, recordID string, txn *store.Transaction, r *record) {
	raw, present := r.list("attachments")
	if !present || r.failed() {
		return
	}
	for _, entry := range raw {
		document, isInline := entry.(map[string]any)
		if !isInline {
			reference, problem := referenceOf(entry, "attachments")
			if problem != nil {
				r.adopt(problem)
				return
			}
			document = m.documents[reference]
		}
		if document == nil {
			r.fail("attachments", "names document %v, which documentStore does not have", entry)
			return
		}
		m.checkNestedFields(storeName, recordID, "attachments.", document, attachmentFields)
		item := r.nested("attachments", document)

		documentID, problem := referenceOf(entry, "attachments")
		if problem != nil {
			r.adopt(problem)
			return
		}
		if documentID == "" {
			documentID = item.text("id", 0)
		}

		m.report.Warnf(KindNoFile, storeName, recordID, "attachments",
			"the export carries the document record but not the file; storage_key names the "+
				"Simplifi document and no blob exists behind it")

		filename := item.textOr("fileName", item.textOr("filename", item.textOr("name", documentID, 255), 255), 255)
		size, _ := item.optInt("size")
		if size == 0 {
			size, _ = item.optInt("sizeBytes")
		}
		attachment := &Attachment{
			ID:            uuid.New(),
			TransactionID: txn.ID,
			Filename:      filename,
			ContentType:   item.textOr("contentType", item.textOr("mimeType", "application/octet-stream", 128), 128),
			SizeBytes:     size,
			StorageKey:    "simplifi-import:" + documentID,
		}
		if r.failed() {
			return
		}
		m.out.Attachments = append(m.out.Attachments, attachment)
		m.report.count("attachments", 1)
	}
}

// pairTransfers writes one transfer_pair_id on BOTH legs; a token on one leg
// alone excludes it from profit and loss forever.
//
// A leg already carrying a different token means the export chains three or
// more rows; overwriting would strand the first leg, so the first pair stands.
//
// A row that moved no money (store.CountsTowardBalance, as checkHandPair) is
// not a leg: Simplifi links its projected occurrences exactly as it links
// real rows.
func (m *Mapper) pairTransfers(storeName string) {
	pairs := map[string]uuid.UUID{}
	for _, link := range m.transferLinks {
		partner, found := m.txnBySourceID[link.partnerID]
		if !found {
			// The partner was deleted in Simplifi. The leg is kept unpaired
			// rather than refused, which would lose its amount too.
			m.report.Warnf(KindUnpaired, storeName, link.sourceID, link.field,
				"transfer partner %q is not in the export; this leg imports unpaired",
				link.partnerID)
			continue
		}
		// Before the token is allocated, so a refused pair is not counted as
		// one written.
		leg := m.txnBySourceID[link.sourceID]
		if !store.CountsTowardBalance(*leg) || !store.CountsTowardBalance(*partner) {
			m.report.Warnf(KindUnpaired, storeName, link.sourceID, link.field,
				"%s, so it is not half of a transfer; this row imports unpaired",
				unpairableBecause(*leg, *partner))
			continue
		}

		first, second := link.sourceID, link.partnerID
		if second < first {
			first, second = second, first
		}
		key := first + "\x00" + second
		token, seen := pairs[key]
		if !seen {
			token = uuid.New()
			pairs[key] = token
		}
		if existing := m.txnBySourceID[link.sourceID].TransferPairID; existing != uuid.Nil && existing != token {
			m.report.Errorf(storeName, link.sourceID, link.field,
				"a transfer chain in the export: this row is already paired with %s "+
					"and cannot also be paired with %q", existing, link.partnerID)
			continue
		}
		if existing := partner.TransferPairID; existing != uuid.Nil && existing != token {
			m.report.Errorf(storeName, link.partnerID, link.field,
				"a transfer chain in the export: this row is already paired with %s "+
					"and cannot also be paired with %q", existing, link.sourceID)
			continue
		}
		m.txnBySourceID[link.sourceID].TransferPairID = token
		partner.TransferPairID = token
	}
	m.report.count("transfer_pairs", len(pairs))
}

// unpairableBecause names which half of CountsTowardBalance a pair failed, for
// the warning. The flags only choose the wording; the refusal is the rule.
func unpairableBecause(leg, partner store.Transaction) string {
	if leg.IsDeleted || partner.IsDeleted {
		return "a deleted transaction has not moved any money"
	}
	return "a forecast has not moved any money yet"
}

// -- 8. series, and the transactions that claim an occurrence ---------

func (m *Mapper) mapSeries() {
	const storeName = "scheduledTransactionsStore"
	m.walk(storeName, kindSeries, func(sourceID string, r *record) {
		templateData, _ := r.object("transaction")
		if templateData == nil {
			templateData = map[string]any{}
		}
		if r.failed() {
			return
		}
		m.checkNestedFields(storeName, sourceID, "transaction.", templateData, knownFields["transactionStore"])
		template := r.nested("transaction", templateData)

		recurrenceData, _ := r.object("recurrence")
		if recurrenceData == nil {
			recurrenceData = map[string]any{}
		}
		if r.failed() {
			return
		}
		m.checkNestedFields(storeName, sourceID, "recurrence.", recurrenceData, recurrenceFields)
		recurrence := m.recurrence(r.nested("recurrence", recurrenceData))

		startOn := r.optDate("startOn")
		if startOn.IsZero() {
			startOn = template.optDate("postedOn")
		}
		if startOn.IsZero() {
			startOn = r.optDate("dueOn")
		}
		if startOn.IsZero() && !r.failed() {
			r.fail("dueOn", "the series has no start date, due date or template date")
			return
		}

		description := template.textOr("payee", r.textOr("name", template.text("renamedPayee", 500), 500), 500)
		if description == "" && !r.failed() {
			r.fail("transaction.payee", "the series has nothing to match against")
			return
		}

		criteria := "auto"
		if r.present("matchCriteria") {
			criteria = lookup(r, matchCriteria, "matchCriteria", r.value("matchCriteria"), "criteria")
		}
		overrideAmount, hasOverride := r.optMoney("overrideNextAmount")
		matchMin, hasMatchMin := r.optMoney("matchAmountMin")
		matchMax, hasMatchMax := r.optMoney("matchAmountMax")

		var autoAcceptDays *int
		if days, ok := r.optInt("autoAcceptDays"); ok {
			autoAcceptDays = &days
		}

		var templateSplits []any
		if splits, present := template.list("allocations"); present && len(splits) > 0 {
			templateSplits = splits
		}

		accountID := m.resolveRef(template, kindAccount, "accountId")
		series := &Series{
			ID:          m.allocate(kindSeries, sourceID),
			AccountID:   accountID,
			CategoryID:  m.coaCategory(template, "coa"),
			Kind:        m.seriesKind(r, template, accountID),
			Description: description,
			DisplayName: r.textOr("displayName", template.text("renamedPayee", 255), 255),
			Amount:      template.moneyOr("amount", domain.Zero),
			Currency:    template.textOr("currency", m.currency, 3),

			Alias:      recurrence.alias,
			Frequency:  recurrence.frequency,
			Interval:   recurrence.interval,
			ByMonthDay: recurrence.byMonthDay,
			ByDay:      recurrence.byDay,

			StartOn:           startOn,
			EndOn:             r.optDate("endOn"),
			NextDueOn:         r.optDate("dueOn"),
			OverrideNextDueOn: r.optDate("overrideNextDueOn"),

			OverrideNextAmount:    overrideAmount,
			HasOverrideNextAmount: hasOverride,

			AutoAdjustDueOn: r.flag("autoAdjustDueOn", false),
			ReminderDays:    r.intOr("reminderDays", 0),
			AutoAcceptDays:  autoAcceptDays,

			MatchCriteria:     criteria,
			MatchAmountMin:    matchMin,
			HasMatchAmountMin: hasMatchMin,
			MatchAmountMax:    matchMax,
			HasMatchAmountMax: hasMatchMax,

			LearnedDescriptions: r.stringList("learnedDescriptions"),

			TemplatePayee:                    template.text("renamedPayee", 255),
			TemplateNotes:                    template.text("memo", 0),
			TemplateTagIDs:                   m.tagsFor(template, "tags"),
			TemplateExcludedFromReports:      template.flag("isExcludedFromReports", false),
			TemplateExcludedFromSpendingPlan: template.flag("isExcludedFromF2S", false),
			TemplateSplits:                   templateSplits,

			IsActive:  m.seriesIsActive(storeName, sourceID, r),
			IsDeleted: r.flag("isDeleted", false),
		}
		if r.failed() {
			return
		}
		if r.flag("isCompleted", false) {
			m.completedSeries[series.ID] = true
		}
		m.out.Series = append(m.out.Series, series)
		m.report.count("series", 1)
	})
	m.relinkOccurrences()
}

type recurrenceFieldsRead struct {
	alias      domain.RecurrenceAlias
	frequency  domain.Frequency
	interval   int
	byMonthDay []int
	byDay      []string
}

func (m *Mapper) recurrence(r *record) recurrenceFieldsRead {
	out := recurrenceFieldsRead{alias: domain.AliasOneTime, interval: 1}
	if r.present("alias") {
		out.alias = lookup(r, recurrenceAliases, "alias", r.value("alias"), "recurrence alias")
	}
	if r.present("frequency") {
		out.frequency = lookup(r, frequencies, "frequency", r.value("frequency"), "frequency")
	}
	out.interval = r.intOr("interval", 1)
	if out.interval < 1 {
		out.interval = 1
	}
	if days, present := r.list("byMonthDay"); present {
		out.byMonthDay = make([]int, 0, len(days))
		for _, day := range days {
			asNumber, ok := day.(json.Number)
			if !ok {
				r.fail("byMonthDay", "expected whole numbers, found %s", typeName(day))
				return out
			}
			whole, err := asNumber.Int64()
			if err != nil {
				r.fail("byMonthDay", "expected whole numbers, found %s", asNumber.String())
				return out
			}
			out.byMonthDay = append(out.byMonthDay, int(whole))
		}
	}
	out.byDay = r.stringList("byDay")
	// Simplifi exports the rule and not its label, so the label is named from
	// the rule rather than left at the one-time default.
	if !r.present("alias") {
		out.alias = domain.AliasForRule(out.frequency, out.interval, out.byMonthDay)
	}
	return out
}

// relinkOccurrences reads stModelId AND stDueOn, never one without the other:
// keyed on the series alone, a twice-monthly bill's first posting fulfils
// both occurrences.
func (m *Mapper) relinkOccurrences() {
	earliest := map[uuid.UUID]domain.Date{}
	latest := map[uuid.UUID]domain.Date{}
	for _, link := range m.seriesLinks {
		found, ok := m.lookupID(kindSeries, link.stModelID)
		if !ok {
			m.report.Errorf("transactionStore", link.sourceID, "stModelId",
				"claims series %q, which scheduledTransactionsStore lacks", link.stModelID)
			continue
		}
		if !link.hasDueOn {
			m.report.Warnf(KindMissingReference, "transactionStore", link.sourceID, "stDueOn",
				"claims a series with no occurrence date; the slot cannot be matched")
		}
		link.txn.SeriesID = found
		link.txn.SeriesDueOn = link.occurrence
		earliest[found] = domain.EarliestOf(earliest[found], link.occurrence)
		latest[found] = latestOf(latest[found], link.occurrence)
	}
	m.anchorSeriesStarts(earliest)
	m.closeCompletedSeries(latest)
}

// closeCompletedSeries stops a series Simplifi has marked isCompleted, the
// only record that it stopped: recurrence.endOn and isDeleted are almost
// always absent. It closes at its last real occurrence rather than being
// deactivated, so the months it ran still expand; a series nothing filled
// closes at its own last due date.
func (m *Mapper) closeCompletedSeries(latest map[uuid.UUID]domain.Date) {
	for _, series := range m.out.Series {
		if !m.completedSeries[series.ID] {
			continue
		}
		end := latest[series.ID]
		if end.IsZero() {
			end = series.NextDueOn
		}
		if end.IsZero() {
			continue
		}
		if series.EndOn.IsZero() || end.Before(series.EndOn) {
			series.EndOn = end
		}
	}
}

// anchorSeriesStarts backdates each series to its first real occurrence.
// Simplifi stores no series start, only dueOn, the *next* occurrence, and a
// series anchored there expands to nothing in earlier months. The earliest
// occurrence a transaction filled (stModelId and stDueOn) is the real start;
// a series nothing filled keeps the schedule's date.
func (m *Mapper) anchorSeriesStarts(earliest map[uuid.UUID]domain.Date) {
	for _, series := range m.out.Series {
		first, found := earliest[series.ID]
		if !found || first.IsZero() {
			continue
		}
		if series.StartOn.IsZero() || first.Before(series.StartOn) {
			series.StartOn = first
		}
	}
}

func latestOf(current, candidate domain.Date) domain.Date {
	if candidate.IsZero() {
		return current
	}
	if current.IsZero() || current.Before(candidate) {
		return candidate
	}
	return current
}

// -- 9. rename rules --------------------------------------------------

func (m *Mapper) mapRules() {
	const storeName = "renameRuleStore"
	m.walk(storeName, kindRule, func(sourceID string, r *record) {
		filterID := m.ruleFilter(storeName, sourceID, r)
		if r.failed() {
			return
		}
		name := r.textOr("name", r.textOr("renamedPayee", r.textOr("renamePayeeTo", "Rule "+sourceID, 255), 255), 255)
		priority := r.intOr("priority", 0)
		if priority == 0 {
			priority = r.intOr("sortOrder", 0)
		}
		setCategory := m.coaCategory(r, "coa")
		if setCategory == uuid.Nil && !r.failed() {
			setCategory = m.optionalRef(r, kindCategory, "categoryId")
		}
		rule := &Rule{
			ID:        m.allocate(kindRule, sourceID),
			Name:      name,
			FilterID:  filterID,
			Priority:  priority,
			IsActive:  r.flag("isActive", true),
			IsDeleted: r.flag("isDeleted", false),
			SourceRef: sourceRefRenameRule + sourceID,

			SetPayee:      r.textOr("renamedPayee", r.textOr("setPayee", r.text("renamePayeeTo", 255), 255), 255),
			SetCategoryID: setCategory,
			AddTagIDs:     m.tagsFor(r, "tags"),
			SetNotes:      r.textOr("memo", r.text("notes", 0), 0),

			// Null means "leave alone"; false clears the flag.
			SetExcludedFromReports:      r.optFlag("isExcludedFromReports"),
			SetExcludedFromSpendingPlan: r.optFlag("isExcludedFromF2S"),
			SetIsReviewed:               r.optFlag("isReviewed"),
		}
		if r.failed() {
			return
		}
		m.out.Rules = append(m.out.Rules, rule)
		m.report.count("rules", 1)
	})
	m.mapTransactionRules()
	m.relinkRules()
}

// filterFromRenameRule turns Simplifi's rename rule into the one Filter shape.
// It matches the statement wording, not the display payee; a matchCriteria
// other than contains or is-exactly is refused rather than widened.
func (m *Mapper) filterFromRenameRule(sourceID string, r *record, wording string) uuid.UUID {
	operator := domain.OpContains
	if criteria := key(r.text("matchCriteria", 64)); criteria != "" {
		switch criteria {
		case "IF_PAYEE_CONTAINS", "CONTAINS", "PAYEE_CONTAINS":
			operator = domain.OpContains
		case "IF_PAYEE_IS_EXACTLY", "IS_EXACTLY", "PAYEE_IS_EXACTLY", "EXACTLY":
			operator = domain.OpIsExactly
		default:
			r.fail("matchCriteria", "unknown rename match: %q", r.text("matchCriteria", 64))
			return uuid.Nil
		}
	}

	filterID := m.allocate(kindRuleFilter, sourceID)
	filter := &store.Filter{
		ID:    filterID,
		Name:  r.text("name", 255),
		Scope: scopeRule,
		Items: []store.FilterItem{{
			ID:         uuid.New(),
			FilterID:   filterID,
			Position:   0,
			Field:      string(domain.FieldStatementName),
			Operator:   string(operator),
			ValueTexts: []string{wording},
		}},
	}
	m.out.Filters = append(m.out.Filters, filter)
	m.report.count("filters", 1)
	m.report.count("filter_items", len(filter.Items))
	return filterID
}

// ruleFilter reads a rule's conditions as a Filter: a filterId, an inline
// condition list, or rename wording. A rule with none is an error, because a
// filter that matched everything would rewrite the whole register.
func (m *Mapper) ruleFilter(storeName, sourceID string, r *record) uuid.UUID {
	if r.present("filterId") {
		return m.resolveRef(r, kindFilter, "filterId")
	}
	field := "conditions"
	items, present := r.list(field)
	if !present || len(items) == 0 {
		field = "filterItems"
		items, present = r.list(field)
	}
	if r.failed() {
		return uuid.Nil
	}
	if !present || len(items) == 0 {
		// Simplifi's rename rule is not filter-backed: it carries only
		// renamePayeeFrom, renamePayeeTo and matchCriteria.
		if wording := r.text("renamePayeeFrom", 500); wording != "" {
			return m.filterFromRenameRule(sourceID, r, wording)
		}
		r.fail("filterId", "the rule has neither a filter, inline conditions, nor wording to match")
		return uuid.Nil
	}

	filterID := m.allocate(kindRuleFilter, sourceID)
	filter := &store.Filter{
		ID:    filterID,
		Name:  r.text("name", 255),
		Scope: scopeRule,
	}
	filter.Items = m.filterItems(storeName, sourceID, filterID, r, field)
	if r.failed() {
		return uuid.Nil
	}
	m.out.Filters = append(m.out.Filters, filter)
	m.report.count("filters", 1)
	m.report.count("filter_items", len(filter.Items))
	return filterID
}

// Where an imported rule came from, as rules.source_ref records it.
const (
	sourceRefRenameRule      = "simplifi:renameRuleStore:"
	sourceRefTransactionRule = "simplifi:transactionRuleId:"
)

func (m *Mapper) relinkRules() {
	missing := map[string][]ruleLink{}
	for _, link := range m.ruleLinks {
		found, ok := m.lookupID(kindTransactionRule, link.ruleID)
		if !ok {
			found, ok = m.lookupID(kindRule, link.ruleID)
		}
		if !ok {
			missing[link.ruleID] = append(missing[link.ruleID], link)
			continue
		}
		link.txn.RuleID = found
	}
	ruleIDs := make([]string, 0, len(missing))
	for ruleID := range missing {
		ruleIDs = append(ruleIDs, ruleID)
	}
	slices.Sort(ruleIDs)
	for _, ruleID := range ruleIDs {
		m.rebuildRule(ruleID, missing[ruleID])
	}
}

// rebuildRule recovers a Simplifi transaction rule from the rows it touched.
// Simplifi keeps transaction rules on the server, so the export holds only the
// transactionRuleId on each row the rule changed. Its actions are read back
// where every row agrees (reviewed is never inferred: people review by hand),
// and its conditions are the rows' statement names, matched exactly. A rule
// whose rows agree on nothing arrives switched off rather than vanishing.
func (m *Mapper) rebuildRule(ruleID string, links []ruleLink) {
	const storeName = "transactionStore"
	evidence := make([]ruleLink, 0, len(links))
	for _, link := range links {
		if !link.txn.IsDeleted && !link.forecast {
			evidence = append(evidence, link)
		}
	}
	if len(evidence) == 0 {
		evidence = links
	}

	var names []string
	folded := map[string]bool{}
	for _, link := range evidence {
		name := strings.TrimSpace(link.txn.StatementName)
		if name == "" || folded[domain.FoldName(name)] {
			continue
		}
		folded[domain.FoldName(name)] = true
		names = append(names, name)
	}
	slices.Sort(names)
	if len(names) == 0 {
		m.report.Warnf(KindRuleRebuilt, storeName, links[0].sourceID, "transactionRuleId",
			"rule %q is not in the export and none of its %d rows has a statement name to match on; "+
				"the rows keep what it did but the rule is not rebuilt", ruleID, len(links))
		return
	}

	rule := &Rule{
		ID:        m.allocate(kindTransactionRule, ruleID),
		SourceRef: sourceRefTransactionRule + ruleID,
		IsActive:  true,
		SetPayee:  unanimousPayee(evidence),

		SetCategoryID:               unanimousCategory(evidence),
		AddTagIDs:                   sharedTags(evidence),
		SetExcludedFromReports:      unanimousFlag(evidence, func(t *store.Transaction) bool { return t.ExcludedFromReports }),
		SetExcludedFromSpendingPlan: unanimousFlag(evidence, func(t *store.Transaction) bool { return t.ExcludedFromSpendingPlan }),
	}
	label := rule.SetPayee
	if label == "" {
		label = names[0]
	}
	acts := rule.SetPayee != "" || rule.SetCategoryID != uuid.Nil || len(rule.AddTagIDs) > 0 ||
		rule.SetExcludedFromReports != nil || rule.SetExcludedFromSpendingPlan != nil
	if acts {
		rule.Name = textutil.Clip(label+" (rebuilt from Simplifi)", 255)
	} else {
		rule.IsActive = false
		rule.Name = textutil.Clip(label+" (rebuilt from Simplifi, choose its actions)", 255)
	}

	filterID := m.allocate(kindRuleFilter, sourceRefTransactionRule+ruleID)
	filter := &store.Filter{
		ID:    filterID,
		Name:  rule.Name,
		Scope: scopeRule,
		Items: []store.FilterItem{{
			ID:         uuid.New(),
			FilterID:   filterID,
			Field:      string(domain.FieldStatementName),
			Operator:   string(domain.OpIsExactly),
			ValueTexts: names,
		}},
	}
	rule.FilterID = filterID
	m.out.Filters = append(m.out.Filters, filter)
	m.report.count("filters", 1)
	m.report.count("filter_items", 1)
	m.out.Rules = append(m.out.Rules, rule)
	m.report.count("rules", 1)
	for _, link := range links {
		link.txn.RuleID = rule.ID
	}

	did := "its rows agree on no action, so it arrives switched off"
	if acts {
		did = "it does what every one of them agrees on"
	}
	missing := "Simplifi's transaction rules are not in the export; pass --transaction-rules to import them"
	if m.report.Read[transactionRulesStore] > 0 {
		missing = "the rules file does not hold this rule, so Simplifi has since deleted it"
	}
	m.report.Warnf(KindRuleRebuilt, storeName, links[0].sourceID, "transactionRuleId",
		"%s; rule %q was rebuilt as %q from the %d rows it touched (%d statement names), and %s",
		missing, ruleID, rule.Name, len(evidence), len(names), did)
}

func unanimousPayee(links []ruleLink) string {
	payee := ""
	for _, link := range links {
		if !link.renamed || link.txn.Payee == "" || (payee != "" && link.txn.Payee != payee) {
			return ""
		}
		payee = link.txn.Payee
	}
	return payee
}

// unanimousCategory reads the unsplit rows only: a split row's parent carries
// no category, and its lines were divided by hand.
func unanimousCategory(links []ruleLink) uuid.UUID {
	category := uuid.Nil
	for _, link := range links {
		if len(link.txn.Splits) > 0 {
			continue
		}
		if link.txn.CategoryID == uuid.Nil || (category != uuid.Nil && link.txn.CategoryID != category) {
			return uuid.Nil
		}
		category = link.txn.CategoryID
	}
	return category
}

func sharedTags(links []ruleLink) []uuid.UUID {
	shared := slices.Clone(links[0].txn.TagIDs)
	for _, link := range links[1:] {
		shared = slices.DeleteFunc(shared, func(tag uuid.UUID) bool {
			return !slices.Contains(link.txn.TagIDs, tag)
		})
	}
	return shared
}

// unanimousFlag sets a flag only when every row has it. A rule that cleared
// one is indistinguishable from rows that never had it, so false is never
// inferred.
func unanimousFlag(links []ruleLink, read func(*store.Transaction) bool) *bool {
	for _, link := range links {
		if !read(link.txn) {
			return nil
		}
	}
	set := true
	return &set
}
