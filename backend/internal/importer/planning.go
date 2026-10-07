package importer

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Steps 10 to 15: goals, the spending plan, watchlists, investments, alerts.

// bucketName pairs our column prefix with Simplifi's field-name stem.
type bucketName struct {
	ours     string
	simplifi string
}

// lowerSimplifi is the stem as Simplifi writes the txnIds field: incomeTxnIds,
// not IncomeTxnIds.
func (b bucketName) lowerSimplifi() string {
	return strings.ToLower(b.simplifi[:1]) + b.simplifi[1:]
}

// buckets are the five whose Simplifi field names follow the five-field
// pattern exactly. Planned spending and Other Spend do not, and are handled by
// name in readBuckets.
var buckets = []bucketName{
	{"income", "Income"},
	{"bills", "Bills"},
	{"subscriptions", "Subscriptions"},
	{"transfer", "Transfer"},
	{"goals", "Goals"},
}

// -- 10. goals --------------------------------------------------------

func (m *Mapper) mapGoals() {
	const storeName = "goalStore"
	// For Simplifi's rows the sign records which way a row moves the goal:
	// after dissolveGoalContainers a contribution is the leg that left the
	// real account. Rows added in this application are asked about instead.
	amounts := make(map[uuid.UUID]domain.Money, len(m.out.Transactions))
	for _, txn := range m.out.Transactions {
		amounts[txn.ID] = txn.Amount
	}
	m.walk(storeName, kindGoal, func(sourceID string, r *record) {
		if r.present("imageId") {
			m.report.Warnf(KindNoFile, storeName, sourceID, "imageId",
				"names a Simplifi-hosted image the export does not carry; image_url is null")
		}
		goal := &Goal{
			ID:          m.allocate(kindGoal, sourceID),
			AccountID:   m.resolveRef(r, kindAccount, "accountId"),
			Name:        r.requiredText("name", 255),
			Description: r.text("description", 500),
			Notes:       r.text("notes", 0),
			Kind:        r.text("type", 32),
			TagID:       m.optionalRef(r, kindTag, "tagId"),

			TargetAmount: r.moneyOr("targetAmount", domain.Zero),
			TargetOn:     r.optDate("targetOn"),
			StartOn:      r.optDate("startOn"),
			CompletedOn:  r.optDate("completedOn"),

			ContributionAmount:    r.moneyOr("contributionAmount", domain.Zero),
			ContributionFrequency: r.text("contributionFrequency", 32),
			ContributedThisMonth:  r.moneyOr("contributedThisMonth", domain.Zero),
			SavedSoFar:            r.moneyOr("savedSoFar", domain.Zero),
			Spent:                 r.moneyOr("spent", domain.Zero),

			TxnIDs:          m.txnIDs(storeName, sourceID, r, "txnIds"),
			IsTakenFromPlan: r.flag("isTakenFromF2S", true),
			IsDeleted:       r.flag("isDeleted", false),
		}
		for _, txnID := range goal.TxnIDs {
			if amounts[txnID].IsPositive() {
				goal.WithdrawalTxnIDs = append(goal.WithdrawalTxnIDs, txnID)
			}
		}
		initial := m.optionalRef(r, kindAccount, "initialAccount")
		if initial != uuid.Nil && initial != goal.AccountID {
			goal.FundingAccountIDs = []uuid.UUID{initial}
		}
		if r.failed() {
			return
		}
		m.out.Goals = append(m.out.Goals, goal)
		m.report.count("goals", 1)
	})
}

// -- 11. the spending plan, one row per month -------------------------

func (m *Mapper) mapSpendingPlan() {
	const storeName = "freeToSpendStore"
	m.walk(storeName, kindMonth, func(sourceID string, r *record) {
		projection := domain.ProjectionRunRate
		if r.present("projectionType") {
			projection = lookup(r, projectionTypes, "projectionType", r.value("projectionType"), "projection")
		}
		month := &SpendingPlanMonth{
			ID:                 m.allocate(kindMonth, sourceID),
			Month:              r.monthStart("date"),
			CalculatedRollover: r.moneyOr("calculatedRolloverAmount", domain.Zero),

			SetAside:               r.moneyOr("setAside", domain.Zero),
			TotalToSpend:           r.moneyOr("totalToSpendAmount", domain.Zero),
			LeftToSpend:            r.moneyOr("leftToSpendAmount", domain.Zero),
			ProjectedOtherSpending: r.moneyOr("projectedOtherSpending", domain.Zero),

			ProjectionType:      projection,
			ProjectionStartDate: r.optDate("projectionStartDate"),
			ProjectionEndDate:   r.optDate("projectionEndDate"),
			ProjectionBuffer:    r.moneyOr("projectionBuffer", domain.Zero),
			// Simplifi stores the projection type but not the window an
			// average reads over.
			ProjectionWindowMonths: 3,

			IsClosedOut:        r.flag("isClosedOut", false),
			ShowClosedOut:      r.flag("showClosedOut", false),
			NewMonthViewed:     r.flag("newMonthViewed", false),
			HideExcludedFromUI: r.flag("isOtherSpendingHideExcludedFromUi", false),
		}
		m.readBuckets(storeName, sourceID, r, month)
		if r.failed() {
			return
		}
		m.out.Months = append(m.out.Months, month)
		m.report.count("spending_plan_months", 1)
		m.envelopes(storeName, sourceID, r, month)
	})
}

// readBuckets copies Simplifi's own answers rather than recomputing them, so
// our engine's figures can be diffed against them month by month.
func (m *Mapper) readBuckets(storeName, recordID string, r *record, month *SpendingPlanMonth) {
	for _, bucket := range buckets {
		into := month.bucket(bucket.ours)
		into.Calculated = r.moneyOr("calculated"+bucket.simplifi+"Amount", domain.Zero)
		into.Overwritten, into.HasOverwritten = r.optMoney("overwritten" + bucket.simplifi + "Amount")
		into.ResetOverwritten = r.flag("resetOverwritten"+bucket.simplifi+"Amount", false)

		if bucket.ours == "goals" {
			// goalsIds names goals, not transactions, so it never goes into
			// the transaction-id column.
			if len(r.stringList("goalsIds")) > 0 || len(r.stringList("excludedGoalsIds")) > 0 {
				m.report.Warnf(KindSpendingPlan, storeName, recordID, "goalsIds",
					"names goals, not transactions; the audit trail is left empty because "+
						"goals_txn_ids holds contribution transactions (data-model.md §9)")
			}
			into.TxnIDs = nil
			into.ExcludedTxnIDs = nil
			continue
		}
		into.TxnIDs = m.txnIDs(storeName, recordID, r, bucket.lowerSimplifi()+"TxnIds")
		into.ExcludedTxnIDs = m.txnIDs(storeName, recordID, r, "excluded"+bucket.simplifi+"TxnIds")
	}

	month.PlannedSpending.Calculated = r.moneyOr("calculatedPlannedSpendingAmount", domain.Zero)
	// Simplifi keeps no audit trail, exclusion list or override for planned
	// spending, and no override for Other Spend.
	month.PlannedSpending.TxnIDs = nil
	month.PlannedSpending.ExcludedTxnIDs = nil
	month.PlannedSpending.HasOverwritten = false
	month.PlannedSpending.ResetOverwritten = false

	month.Spent.Calculated = r.moneyOr("spentAmount", domain.Zero)
	month.Spent.TxnIDs = m.txnIDs(storeName, recordID, r, "spentTxnIds")
	month.Spent.ExcludedTxnIDs = m.txnIDs(storeName, recordID, r, "excludedSpentTxnIds")
	month.Spent.HasOverwritten = false
	month.Spent.ResetOverwritten = false

	m.report.Warnf(KindSpendingPlan, storeName, recordID, "calculatedPlannedSpendingAmount",
		"Simplifi stores no txnIds, excluded or overwritten fields for planned spending "+
			"or Other Spend's override; those four columns are left at their defaults")
}

func (m *Mapper) envelopes(storeName, recordID string, r *record, month *SpendingPlanMonth) {
	items, present := r.list("plannedSpendingItems")
	if !present || r.failed() {
		return
	}
	for _, entry := range items {
		data, isObject := entry.(map[string]any)
		if !isObject {
			r.fail("plannedSpendingItems", "expected an object, found %s", typeName(entry))
			return
		}
		m.checkNestedFields(storeName, recordID, "plannedSpendingItems.", data, plannedSpendingItemFields)
		item := r.nested("plannedSpendingItems", data)

		itemID := item.requiredText("id", 0)
		if r.failed() {
			return
		}
		if m.envelopeIDsSeen[itemID] {
			m.envelopeIDsRepeated = true
		}
		m.envelopeIDsSeen[itemID] = true

		recurring := item.flag("recurring", false)
		if !item.present("rolloverAmount") {
			m.report.Warnf(KindSpendingPlan, storeName, recordID, "plannedSpendingItems.rolloverAmount",
				"Simplifi's envelope carries no rollover figure; ours starts at zero")
		}
		overwritten, hasOverwritten := item.optMoney("overwrittenTargetAmount")

		envelope := &Envelope{
			ID:                  uuid.New(),
			SpendingPlanMonthID: month.ID,
			FilterID:            m.resolveRef(item, kindFilter, "filterId"),
			Name:                item.requiredText("name", 255),

			TargetAmount:            item.moneyOr("targetAmount", domain.Zero),
			OverwrittenTargetAmount: overwritten,
			HasOverwrittenTarget:    hasOverwritten,
			// Negated: Simplifi stores this in the ledger's sign, and our
			// envelope figures are magnitudes (domain.EnvelopeSpent).
			CalculatedSpentAmount: item.moneyOr("calculatedSpentAmount", domain.Zero).Neg(),
			RolloverAmount:        item.moneyOr("rolloverAmount", domain.Zero),
			AutoReleaseRollover:   item.flag("isAutoRelease", false),
			Recurring:             recurring,

			TxnIDs:             m.txnIDs(storeName, recordID, item, "txnIds"),
			ExcludedTxnIDs:     m.txnIDs(storeName, recordID, item, "excludedTxnIds"),
			HideExcludedFromUI: item.flag("isHideExcludedFromUi", false),
		}
		if recurring {
			// Simplifi's item id is the only candidate for envelope identity
			// across months; envelopeIDsRepeated records whether it is stable.
			envelope.RecurringGroupID = m.allocate(kindEnvelopeGroup, itemID)
		}
		if r.failed() {
			return
		}
		m.out.Envelopes = append(m.out.Envelopes, envelope)
		m.report.count("envelopes", 1)
	}
}

// -- 12. watchlists ---------------------------------------------------

func (m *Mapper) mapWatchlists() {
	m.walk("spendingWatchListStore", kindWatchlist, func(sourceID string, r *record) {
		period := "month"
		if r.present("period") {
			period = lookup(r, watchlistPeriods, "period", r.value("period"), "period")
		}
		target, hasTarget := r.optMoney("targetAmount")
		watchlist := &Watchlist{
			ID:           m.allocate(kindWatchlist, sourceID),
			FilterID:     m.resolveRef(r, kindFilter, "filterId"),
			Name:         r.requiredText("name", 255),
			Emoji:        r.emoji("emoji", 16),
			TargetAmount: target,
			HasTarget:    hasTarget,
			Period:       period,
			StartDate:    r.optDate("startDate"),
			EndDate:      r.optDate("endDate"),
			IsDeleted:    r.flag("isDeleted", false),
		}
		if r.failed() {
			return
		}
		m.out.Watchlists = append(m.out.Watchlists, watchlist)
		m.report.count("watchlists", 1)
	})
}

// -- 13. securities and holdings --------------------------------------

// symbolFromName recovers a fund's ticker from a name like
// "Example 500 Index Fund (ABCDX)". Symbol is unique per space, so funds with
// no symbol field would otherwise collide on the empty string.
func symbolFromName(name string) string {
	trimmed := strings.TrimSpace(name)
	if !strings.HasSuffix(trimmed, ")") {
		return ""
	}
	open := strings.LastIndex(trimmed, "(")
	if open < 0 {
		return ""
	}
	inner := trimmed[open+1 : len(trimmed)-1]
	if len(inner) < 3 || len(inner) > 8 || strings.ContainsAny(inner, " ,") {
		return ""
	}
	if inner != strings.ToUpper(inner) {
		return ""
	}
	return inner
}

// holdingSecurity resolves the security a position is in. The reference is
// usually the brokerage's own id (a CUSIP or fund number), only sometimes the
// securitiesStore record id; symbol is the last resort.
func (m *Mapper) holdingSecurity(
	r *record, securities map[string]*Security, byBrokerID, bySymbol map[string]string,
) uuid.UUID {
	reference := r.reference("securityId")
	if r.failed() {
		return uuid.Nil
	}
	if security, found := securities[reference]; found && reference != "" {
		return security.ID
	}
	if sourceID, found := byBrokerID[reference]; found {
		return securities[sourceID].ID
	}
	symbol := r.textOr("symbol", r.text("ticker", 32), 32)
	if sourceID, found := bySymbol[strings.ToUpper(symbol)]; found && symbol != "" {
		return securities[sourceID].ID
	}
	if name := r.text("securityName", 255); name != "" {
		if sourceID, found := bySymbol["NAME:"+strings.ToUpper(name)]; found {
			return securities[sourceID].ID
		}
	}
	if reference == "" {
		// A cash sweep, already in the account's balance; counting it as a
		// position would double it.
		return uuid.Nil
	}
	r.fail("securityId", "references security %q, which securitiesStore lacks under any id, "+
		"symbol or name", reference)
	return uuid.Nil
}

func (m *Mapper) mapInvestments() {
	bySymbol := map[string]string{}
	byBrokerID := map[string]string{}
	securities := map[string]*Security{}

	// One instrument can hold several securitiesStore rows, one per brokerage
	// that reported it; the later row is aliased onto the first. Rows with no
	// symbol key on the name instead, because dropping them would drop the
	// positions that reference them.
	m.walk("securitiesStore", kindSecurity, func(sourceID string, r *record) {
		symbol := r.textOr("symbol", r.text("ticker", 32), 32)
		name := r.textOr("name", symbol, 255)
		if r.failed() {
			return
		}
		if symbol == "" {
			symbol = symbolFromName(name)
		}
		if symbol == "" {
			// A fund can carry neither; the brokerage's id keeps it distinct.
			symbol = r.textOr("securityId", sourceID, 32)
		}
		identity := strings.ToUpper(symbol)
		if identity == "" {
			identity = "NAME:" + strings.ToUpper(name)
		}
		if identity == "NAME:" {
			// Neither a symbol nor a name: kept under its own id so its
			// holdings survive.
			identity = "ID:" + sourceID
		}
		if first, seen := bySymbol[identity]; seen {
			securities[sourceID] = securities[first]
			m.aliasID(kindSecurity, sourceID, securities[first].ID)
			m.report.count("securities_aliased", 1)
			return
		}
		bySymbol[identity] = sourceID
		// Holdings name a security by the brokerage's id, so it is registered
		// as an alias of the record id.
		if brokerID := r.text("securityId", 64); brokerID != "" {
			byBrokerID[brokerID] = sourceID
		}

		security := &Security{
			ID:        m.allocate(kindSecurity, sourceID),
			Symbol:    symbol,
			Name:      name,
			Kind:      strings.ToLower(r.textOr("type", "equity", 32)),
			Exchange:  r.text("exchange", 32),
			Currency:  r.textOr("currency", m.currency, 3),
			CUSIP:     r.text("cusip", 16),
			ISIN:      r.text("isin", 16),
			IsDeleted: r.flag("isDeleted", false),
		}
		if r.failed() {
			return
		}
		securities[sourceID] = security
		m.out.Securities = append(m.out.Securities, security)
		m.report.count("securities", 1)
	})

	m.walk("investmentQuotesV2Store", "", func(sourceID string, r *record) {
		securityID := r.reference("securityId")
		if r.failed() {
			return
		}
		if securityID == "" {
			securityID = sourceID
		}
		security, found := securities[securityID]
		if !found {
			// investmentQuotesV2Store is keyed by ticker, not by security id.
			symbol := r.textOr("symbol", r.text("ticker", 32), 32)
			if symbol == "" {
				symbol = sourceID
			}
			if bySymbolID, seen := bySymbol[strings.ToUpper(symbol)]; seen {
				security, found = securities[bySymbolID]
			}
		}
		if !found {
			r.fail("securityId", "quotes security %q, which securitiesStore lacks", securityID)
			return
		}
		price, hasPrice := r.optRate("lastPrice")
		if !hasPrice {
			price, hasPrice = r.optRate("price")
		}
		prior, hasPrior := r.optRate("priorClose")
		at := r.optTime("asOf")
		if r.failed() {
			return
		}
		security.LastPrice, security.HasLastPrice = price, hasPrice
		security.PriorClose, security.HasPriorClose = prior, hasPrior
		security.LastPriceAt = at
	})

	type position struct {
		account  uuid.UUID
		security uuid.UUID
	}
	seen := map[position]bool{}

	m.walk("investmentHoldingsV2Store", "", func(sourceID string, r *record) {
		accountID := m.resolveRef(r, kindAccount, "accountId")
		securityID := m.holdingSecurity(r, securities, byBrokerID, bySymbol)
		if r.failed() {
			return
		}
		if securityID == uuid.Nil {
			return
		}
		if seen[position{accountID, securityID}] {
			r.fail("securityId", "a second holding of the same security in the same account")
			return
		}
		seen[position{accountID, securityID}] = true

		shares, hasShares := r.optRate("shares")
		if !hasShares {
			shares, hasShares = r.optRate("quantity")
		}
		if !hasShares {
			if !r.failed() {
				r.fail("shares", "a holding with no share count is not a position")
			}
			return
		}
		costBasis, hasCostBasis := r.optMoney("costBasis")
		averageCost, hasAverage := r.optRate("averageCost")
		marketValue, hasMarketValue := r.optMoney("marketValue")
		asOf := r.optDate("asOf")
		if asOf.IsZero() {
			asOf = r.optDate("asOfOn")
		}

		holding := &Holding{
			ID:         uuid.New(),
			AccountID:  accountID,
			SecurityID: securityID,
			ExternalID: sourceID,
			Shares:     shares,

			CostBasis:    costBasis,
			HasCostBasis: hasCostBasis,
			AverageCost:  averageCost,
			HasAverage:   hasAverage,
			// An unknown basis is a state, not a zero: a portfolio that renders
			// market_value - 0 reads as a spectacular return.
			IsCostBasisComplete: r.flag("isCostBasisComplete", hasCostBasis),

			MarketValue:    marketValue,
			HasMarketValue: hasMarketValue,
			AsOf:           asOf,
		}
		if r.failed() {
			return
		}
		m.out.Holdings = append(m.out.Holdings, holding)
		m.report.count("holdings", 1)
	})
}

// -- 14. balance history ----------------------------------------------

// The export writes one record per balance,
// {accountId, balanceType, balanceAmount, balanceOn}, with balanceType ONLINE
// (settled) or CURRENT (pending included). Most days carry both, and CURRENT
// wins the day: it is the figure Simplifi shows. The nested
// {balances|points|history} shape is also read; the invented test fixtures
// use it.
func (m *Mapper) mapBalanceHistory() {
	const storeName = "accountsBalancesStore"
	type day struct {
		account uuid.UUID
		on      string
	}
	type snapshot struct {
		balance domain.Money
		current bool
	}
	picked := map[day]snapshot{}
	order := []day{}

	keep := func(accountID uuid.UUID, asOf domain.Date, balance domain.Money, current bool) {
		at := day{accountID, asOf.String()}
		existing, found := picked[at]
		if !found {
			picked[at] = snapshot{balance, current}
			order = append(order, at)
			return
		}
		if current && !existing.current {
			picked[at] = snapshot{balance, current}
		}
	}

	m.walk(storeName, "", func(sourceID string, r *record) {
		var accountID uuid.UUID
		if r.present("accountId") {
			accountID = m.resolveRef(r, kindAccount, "accountId")
		} else {
			accountID = m.resolveID(r, kindAccount, "accountId", sourceID)
		}
		if r.failed() {
			return
		}

		// The flat shape: this record is itself one balance.
		if r.present("balanceAmount") || r.present("balanceOn") {
			asOf := r.optDate("balanceOn")
			if asOf.IsZero() {
				asOf = r.optDate("balanceAt")
			}
			if r.failed() {
				return
			}
			if asOf.IsZero() {
				r.fail("balanceOn", "a balance with no date")
				return
			}
			balance, has := r.optMoney("balanceAmount")
			if r.failed() {
				return
			}
			if !has {
				r.fail("balanceAmount", "a balance with no amount")
				return
			}
			keep(accountID, asOf, balance, key(r.text("balanceType", 32)) == "CURRENT")
			return
		}

		points, present := r.list("balances")
		field := "balances"
		if !present {
			points, present = r.list("points")
			field = "points"
		}
		if !present {
			points, present = r.list("history")
			field = "history"
		}
		if r.failed() {
			return
		}
		if !present {
			r.fail("balances", "the record carries no balance series")
			return
		}

		for _, entry := range points {
			data, isObject := entry.(map[string]any)
			if !isObject {
				r.fail(field, "expected an object, found %s", typeName(entry))
				return
			}
			m.checkNestedFields(storeName, sourceID, field+".", data, balancePointFields)
			point := r.nested(field, data)

			asOf := point.optDate("date")
			if asOf.IsZero() {
				asOf = point.optDate("on")
			}
			if asOf.IsZero() {
				asOf = point.optDate("asOf")
			}
			if r.failed() {
				return
			}
			if asOf.IsZero() {
				point.fail("date", "a balance point with no date")
				return
			}

			balance, has := point.optMoney("normalizedBalance")
			if !has {
				balance, has = point.optMoney("balance")
			}
			if !has {
				balance = point.moneyOr("amount", domain.Zero)
			}
			if r.failed() {
				return
			}
			keep(accountID, asOf, balance, false)
		}
	})

	newest := map[uuid.UUID]domain.Date{}
	for _, at := range order {
		asOf, ok := parseDate(at.on)
		if !ok {
			continue
		}
		m.out.BalanceSnapshots = append(m.out.BalanceSnapshots, &BalanceSnapshot{
			ID:        uuid.New(),
			AccountID: at.account,
			AsOf:      asOf,
			Balance:   picked[at].balance,
		})
		m.report.count("balance_snapshots", 1)
		if last, found := newest[at.account]; !found || last.Before(asOf) {
			newest[at.account] = asOf
			m.setAccountBalance(at.account, picked[at].balance, asOf)
		}
	}
}

// setAccountBalance moves an account onto its newest real balance.
// accountsStore.normalizedBalance is a cache that can be stale or missing, so
// it is only the fallback for an account the balance store never covered.
func (m *Mapper) setAccountBalance(accountID uuid.UUID, balance domain.Money, asOf domain.Date) {
	account, found := m.accountsByID[accountID]
	if !found {
		return
	}
	at := asOf.Time()
	account.ProviderBalance = balance
	account.HasProviderBalance = true
	account.ProviderBalanceAt = &at
}

// -- 15. alert rules --------------------------------------------------

func (m *Mapper) mapAlertRules() {
	const storeName = "alertRulesStore"
	type scope struct {
		alertType string
		account   uuid.UUID
	}
	seen := map[scope]bool{}

	m.walk(storeName, "", func(sourceID string, r *record) {
		raw := r.value("alertType")
		field := "alertType"
		if raw == nil {
			raw = r.value("type")
			field = "type"
		}
		if text, isText := raw.(string); isText {
			if why, declined := declinedAlertTypes[key(text)]; declined {
				m.report.Warnf(KindAlert, storeName, sourceID, field, "%q is not in our catalog: %s", text, why)
				return
			}
		}
		alertType := lookup(r, alertTypes, field, raw, "alert type")
		accountID := m.optionalRef(r, kindAccount, "accountId")
		if r.failed() {
			return
		}
		definition, known := domain.AlertByType(alertType)
		if !known {
			r.fail(field, "%v maps to %q, which the alert catalog does not hold", raw, alertType)
			return
		}
		if seen[scope{string(alertType), accountID}] {
			r.fail(field, "a second rule for the same alert and account in one space")
			return
		}
		seen[scope{string(alertType), accountID}] = true

		channels := map[string]bool{}
		for _, one := range r.stringList("channels") {
			channels[key(one)] = true
		}
		// Simplifi keeps the household's choices in preferences:
		// {isEnabled, isEmailOk, isPushOk, isInProductOk, is*Supported}.
		preferences := r.nested("preferences", nil)
		if found, ok := r.object("preferences"); ok {
			preferences = r.nested("preferences", found)
		}

		rule := &AlertRule{
			ID:        uuid.New(),
			UserID:    m.ownerID,
			AlertType: string(alertType),
			AccountID: accountID,

			IsEnabled: r.flag("isEnabled", r.flag("enabled", preferences.flag("isEnabled", true))),
			IsPaused:  r.flag("isPaused", false),

			ChannelEmail: definition.Allows(domain.ChannelEmail) &&
				r.flag("email", preferences.flag("isEmailOk", channels["EMAIL"])),
			ChannelPush: definition.Allows(domain.ChannelPush) &&
				r.flag("push", preferences.flag("isPushOk", channels["PUSH"])),
			ChannelInApp: definition.Allows(domain.ChannelInApp) &&
				r.flag("inApp", preferences.flag("isInProductOk", len(channels) == 0 || channels["IN_APP"])),
		}
		alertThreshold(r, definition, rule)
		if r.failed() {
			return
		}
		m.out.AlertRules = append(m.out.AlertRules, rule)
		m.report.count("alert_rules", 1)
	})
}

// thresholdFields are the explicit per-kind threshold fields a record may
// carry. "threshold" is not among them: it is read as whichever kind the
// alert compares against.
var thresholdFields = map[domain.ThresholdKind]string{
	domain.ThresholdAmount:  "thresholdAmount",
	domain.ThresholdCount:   "thresholdCount",
	domain.ThresholdPercent: "thresholdPercent",
}

// thresholdFormats are inputVariables' names for the kinds it describes.
var thresholdFormats = map[string]domain.ThresholdKind{
	"CURRENCY":   domain.ThresholdAmount,
	"PERCENTAGE": domain.ThresholdPercent,
}

// alertThreshold sets the one number an alert compares against: the
// household's value (an explicit field, or inputValues), else Simplifi's
// default, else the catalog's. A stored rule never falls back to the catalog,
// so an alert written without its number would never fire.
func alertThreshold(r *record, definition domain.AlertDefinition, rule *AlertRule) {
	for kind, field := range thresholdFields {
		if kind != definition.Threshold && r.present(field) {
			r.fail(field, "%s compares against a %s, not a %s", definition.Type, definition.Threshold, kind)
			return
		}
	}
	if definition.Threshold == domain.ThresholdNone {
		return
	}

	source, field := r, thresholdFields[definition.Threshold]
	switch {
	case r.present(field):
	case r.present("threshold"):
		field = "threshold"
	default:
		source, field = alertInputThreshold(r, definition)
	}
	if r.failed() {
		return
	}
	if source == nil {
		setDefaultThreshold(definition, rule)
		return
	}

	switch definition.Threshold {
	case domain.ThresholdAmount:
		rule.ThresholdAmount, rule.HasThresholdAmount = source.optMoney(field)
	case domain.ThresholdPercent:
		rule.ThresholdPct, rule.HasThresholdPct = source.optRate(field)
	case domain.ThresholdCount:
		if count, ok := source.optInt(field); ok {
			rule.ThresholdCount = &count
		}
	}
}

// alertInputThreshold finds the threshold in Simplifi's own shape: the value
// the household set in inputValues, or the default in inputVariables. It
// returns a nil record when neither holds one.
func alertInputThreshold(r *record, definition domain.AlertDefinition) (*record, string) {
	if values := alertInputValues(r); values != nil && values.present("threshold") {
		return values, "threshold"
	}
	variables, ok := r.object("inputVariables")
	if !ok {
		return nil, ""
	}
	threshold, ok := r.nested("inputVariables", variables).object("threshold")
	if !ok {
		return nil, ""
	}
	described := r.nested("inputVariables.threshold", threshold)
	if format := described.text("format", 0); format != "" {
		kind, known := thresholdFormats[key(format)]
		if !known {
			described.fail("format", "unknown threshold format %q", format)
			return nil, ""
		}
		if kind != definition.Threshold {
			described.fail("format", "%s compares against a %s, not a %s", definition.Type, definition.Threshold, kind)
			return nil, ""
		}
	}
	if !described.present("default") {
		return nil, ""
	}
	return described, "default"
}

// alertInputValues reads inputValues, which may arrive as an object or as the
// JSON text of one.
func alertInputValues(r *record) *record {
	switch typed := r.value("inputValues").(type) {
	case nil:
		return nil
	case map[string]any:
		return r.nested("inputValues", typed)
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		decoder := json.NewDecoder(strings.NewReader(typed))
		decoder.UseNumber()
		var decoded map[string]any
		if err := decoder.Decode(&decoded); err != nil {
			r.fail("inputValues", "not a JSON object: %v", err)
			return nil
		}
		return r.nested("inputValues", decoded)
	default:
		r.fail("inputValues", "expected an object, found %s", typeName(typed))
		return nil
	}
}

func setDefaultThreshold(definition domain.AlertDefinition, rule *AlertRule) {
	switch definition.Threshold {
	case domain.ThresholdAmount:
		rule.ThresholdAmount, rule.HasThresholdAmount = definition.DefaultAmount, true
	case domain.ThresholdPercent:
		rule.ThresholdPct, rule.HasThresholdPct = decimal.NewFromInt(int64(definition.DefaultPercent)), true
	case domain.ThresholdCount:
		count := definition.DefaultCount
		rule.ThresholdCount = &count
	}
}

// -- the GOAL container -----------------------------------------------

// dissolveGoalContainers takes Simplifi's savings-goal accounts back out of the
// ledger.
//
// Simplifi keeps a goal's money in an account of its own (`type: GOAL`) and
// counts it again in the real account's goalBalance; ours reserves inside the
// real account (calculations.md §6). So the container goes, with its leg of
// each contribution; the goal moves to the account whose goalBalance it
// matches, and the leg that left that account becomes the contribution, no
// longer a transfer.
//
// A container is only taken out when exactly one goal names it, exactly one
// account's goalBalance equals what that goal has saved, and every one of its
// rows is a transfer whose partner is somewhere real. Anything else keeps the
// container and says why.
func (m *Mapper) dissolveGoalContainers() {
	const storeName = "accountsStore"
	for containerID := range m.goalContainers {
		goal, reserve, ok := m.goalContainerTarget(storeName, containerID)
		if !ok {
			continue
		}
		legs, ok := m.containerLegs(storeName, containerID)
		if !ok {
			continue
		}

		for _, leg := range legs {
			partner := m.partnerLeg(leg)
			// Release the token on both sides so no half pair outlives the
			// dropped row.
			partner.TransferPairID = uuid.Nil
			for i, txnID := range goal.TxnIDs {
				if txnID == leg.ID {
					goal.TxnIDs[i] = partner.ID
				}
			}
		}

		goal.AccountID = reserve.ID
		m.dropContainer(containerID, legs)
		m.report.Warnf(KindGoalAccount, storeName, containerID.String(), "type",
			"a GOAL account holds %s that %q already carries in goalBalance; the "+
				"container is dropped and the goal reserves inside %q instead",
			goal.SavedSoFar, reserve.Name, reserve.Name)
	}
}

// goalContainerTarget is the goal a container belongs to and the real account
// holding its money, or ok=false with the reason reported.
func (m *Mapper) goalContainerTarget(
	storeName string, containerID uuid.UUID,
) (*Goal, *store.Account, bool) {
	var goals []*Goal
	for _, goal := range m.out.Goals {
		if goal.AccountID == containerID {
			goals = append(goals, goal)
		}
	}
	if len(goals) != 1 {
		m.report.Warnf(KindGoalAccount, storeName, containerID.String(), "type",
			"a GOAL account named by %d goals; it stays an account, because which "+
				"goal's reserve it holds is not decidable", len(goals))
		return nil, nil, false
	}
	goal := goals[0]

	var holders []*store.Account
	for _, account := range m.out.Accounts {
		if !m.goalContainers[account.ID] &&
			!account.GoalBalance.IsZero() && account.GoalBalance.Equal(goal.SavedSoFar) {
			holders = append(holders, account)
		}
	}
	if len(holders) != 1 {
		m.report.Warnf(KindGoalAccount, storeName, containerID.String(), "goalBalance",
			"%d accounts carry a goalBalance of %s, so the goal keeps its container "+
				"and reserves nothing in an account anybody spends from",
			len(holders), goal.SavedSoFar)
		return nil, nil, false
	}
	return goal, holders[0], true
}

// containerLegs is the container's rows, once every one of them is known to be
// a transfer with a partner somewhere real; a row without one cannot be
// dropped without losing its amount.
func (m *Mapper) containerLegs(storeName string, containerID uuid.UUID) ([]*store.Transaction, bool) {
	var legs []*store.Transaction
	for _, txn := range m.out.Transactions {
		if txn.AccountID != containerID {
			continue
		}
		partner := m.partnerLeg(txn)
		if partner == nil || m.goalContainers[partner.AccountID] {
			m.report.Warnf(KindGoalAccount, storeName, containerID.String(), "type",
				"a GOAL account holds a row that is not a transfer into it from a "+
					"real account, so the container stays: dropping it would lose %s",
				txn.Amount)
			return nil, false
		}
		legs = append(legs, txn)
	}
	return legs, true
}

func (m *Mapper) partnerLeg(leg *store.Transaction) *store.Transaction {
	if leg.TransferPairID == uuid.Nil {
		return nil
	}
	for _, other := range m.out.Transactions {
		if other.ID != leg.ID && other.TransferPairID == leg.TransferPairID {
			return other
		}
	}
	return nil
}

// dropContainer removes the account, its rows and its balance history.
func (m *Mapper) dropContainer(containerID uuid.UUID, legs []*store.Transaction) {
	dropped := make(map[uuid.UUID]bool, len(legs))
	for _, leg := range legs {
		dropped[leg.ID] = true
	}

	m.out.Accounts = slices.DeleteFunc(m.out.Accounts, func(a *store.Account) bool {
		return a.ID == containerID
	})
	m.out.Transactions = slices.DeleteFunc(m.out.Transactions, func(t *store.Transaction) bool {
		return dropped[t.ID]
	})
	snapshots := len(m.out.BalanceSnapshots)
	m.out.BalanceSnapshots = slices.DeleteFunc(m.out.BalanceSnapshots, func(b *BalanceSnapshot) bool {
		return b.AccountID == containerID
	})
	droppedSnapshots := snapshots - len(m.out.BalanceSnapshots)
	for _, goal := range m.out.Goals {
		goal.FundingAccountIDs = slices.DeleteFunc(goal.FundingAccountIDs, func(id uuid.UUID) bool {
			return id == containerID
		})
	}

	// The report counts what was written, not what was mapped.
	m.report.count("accounts", -1)
	m.report.count("transactions", -len(legs))
	m.report.count("balance_snapshots", -droppedSnapshots)
}
