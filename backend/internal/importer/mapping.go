package importer

import (
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Export records in, rows or named errors out. Mapping is a pure function of
// the export; writing.go is the only part that needs a database.
//
// The fifteen steps run in the order docs/importing.md gives,
// and the order is load-bearing: each step resolves foreign keys against ids
// allocated by an earlier one. Ids are generated here, each store's in a first
// pass, so a record can name one that appears later. A record that cannot be
// mapped is reported, never substituted, and the run continues.

// The id namespaces. One Simplifi id can appear in two stores meaning two
// different things, so the map is keyed by both.
const (
	kindSpace       = "space"
	kindUser        = "user"
	kindInstitution = "institution"
	kindAccount     = "account"
	kindCategory    = "category"
	kindTag         = "tag"
	kindFilter      = "filter"
	kindRuleFilter  = "rule_filter"
	kindTransaction = "transaction"
	kindSeries      = "series"
	kindRule        = "rule"
	// kindTransactionRule is what a row's transactionRuleId names: a rule
	// from the rules file, or one rebuilt from its rows.
	kindTransactionRule = "transaction_rule"
	kindGoal            = "goal"
	kindMonth           = "month"
	kindEnvelopeGroup   = "envelope_group"
	kindWatchlist       = "watchlist"
	kindSecurity        = "security"
)

type transferLink struct {
	sourceID  string
	partnerID string
	field     string
}

type seriesLink struct {
	txn        *store.Transaction
	sourceID   string
	stModelID  string
	occurrence domain.Date
	hasDueOn   bool
}

type ruleLink struct {
	txn      *store.Transaction
	sourceID string
	ruleID   string
	// renamed is whether Simplifi wrote a renamedPayee on the row, and
	// forecast whether the row is a projection rather than money that moved.
	// Both decide whether the row is evidence of what a missing rule did.
	renamed  bool
	forecast bool
}

// Options are the choices the operator makes, which the export cannot.
type Options struct {
	// OwnerEmail names the user the import writes; the export carries no
	// identity, and alert rules are scoped to a user.
	OwnerEmail string
	// SpaceName overrides the dataset's own name.
	SpaceName string
	// IntoSpace, when set, is an existing empty space the import fills instead
	// of creating one. The owner is already a member of it.
	IntoSpace store.SpaceID
}

// Mapper holds one run's id bookkeeping and the deferred links.
type Mapper struct {
	export    *Export
	datasetID string
	options   Options
	report    *Report
	out       *Mapped

	ids      map[string]map[string]uuid.UUID
	spaceID  store.SpaceID
	ownerID  uuid.UUID
	currency string

	// goalContainers are the GOAL-type accounts dissolveGoalContainers takes
	// back out once everything that references one is mapped.
	goalContainers map[uuid.UUID]bool

	knownCategories      map[string]uuid.UUID
	tagsBySourceID       map[string]*store.Tag
	loginsToInstitutions map[string]string
	txnBySourceID        map[string]*store.Transaction
	transferLinks        []transferLink
	seriesLinks          []seriesLink
	completedSeries      map[uuid.UUID]bool
	accountsByID         map[uuid.UUID]*store.Account
	ruleLinks            []ruleLink
	documents            map[string]map[string]any
	envelopeIDsSeen      map[string]bool
	envelopeIDsRepeated  bool
}

func NewMapper(export *Export, datasetID string, options Options) *Mapper {
	m := &Mapper{
		export:               export,
		datasetID:            datasetID,
		options:              options,
		report:               NewReport(),
		ids:                  map[string]map[string]uuid.UUID{},
		currency:             "USD",
		knownCategories:      map[string]uuid.UUID{},
		tagsBySourceID:       map[string]*store.Tag{},
		loginsToInstitutions: map[string]string{},
		txnBySourceID:        map[string]*store.Transaction{},
		documents:            map[string]map[string]any{},
		envelopeIDsSeen:      map[string]bool{},
		completedSeries:      map[uuid.UUID]bool{},
		accountsByID:         map[uuid.UUID]*store.Account{},
	}
	m.spaceID = store.SpaceIDOf(m.allocate(kindSpace, datasetID))
	if options.IntoSpace != (store.SpaceID{}) {
		m.spaceID = options.IntoSpace
	}
	m.ownerID = m.allocate(kindUser, options.OwnerEmail)
	m.out = &Mapped{Report: m.report, SpaceID: m.spaceID, IntoExisting: options.IntoSpace != (store.SpaceID{})}
	return m
}

func (m *Mapper) Run() *Mapped {
	m.mapSpace()
	m.mapInstitutions()
	m.mapAccounts()
	m.mapCategories()
	m.mapTags()
	m.mapFilters()
	m.mapTransactions()
	m.mapSeries()
	m.mapRules()
	m.mapGoals()
	m.mapSpendingPlan()
	m.mapWatchlists()
	m.mapInvestments()
	m.mapBalanceHistory()
	m.mapAlertRules()
	m.dissolveGoalContainers()
	m.auditStores()
	return m.out
}

// -- id bookkeeping ---------------------------------------------------

// allocate returns our id for one Simplifi id, stable for the run.
func (m *Mapper) allocate(kind, sourceID string) uuid.UUID {
	table, found := m.ids[kind]
	if !found {
		table = map[string]uuid.UUID{}
		m.ids[kind] = table
	}
	if existing, seen := table[sourceID]; seen {
		return existing
	}
	fresh := uuid.New()
	table[sourceID] = fresh
	return fresh
}

// aliasID points a second source id at an id already allocated, for the case
// where one real thing arrives under several of Simplifi's ids.
func (m *Mapper) aliasID(kind, sourceID string, id uuid.UUID) {
	table, found := m.ids[kind]
	if !found {
		table = map[string]uuid.UUID{}
		m.ids[kind] = table
	}
	table[sourceID] = id
}

func (m *Mapper) lookupID(kind, sourceID string) (uuid.UUID, bool) {
	found, ok := m.ids[kind][sourceID]
	return found, ok
}

// resolveRef reads a required foreign key and refuses to invent one.
func (m *Mapper) resolveRef(r *record, kind, field string) uuid.UUID {
	reference := r.reference(field)
	if r.failed() {
		return uuid.Nil
	}
	if reference == "" {
		r.fail(field, "required reference is missing")
		return uuid.Nil
	}
	return m.resolveID(r, kind, field, reference)
}

func (m *Mapper) resolveID(r *record, kind, field, reference string) uuid.UUID {
	found, ok := m.lookupID(kind, reference)
	if !ok {
		r.fail(field, "references %s %q, which no earlier step imported", kind, reference)
		return uuid.Nil
	}
	return found
}

// optionalRef resolves a foreign key that may be absent, and still refuses to
// invent one that is present and unknown.
func (m *Mapper) optionalRef(r *record, kind, field string) uuid.UUID {
	reference := r.reference(field)
	if r.failed() || reference == "" {
		return uuid.Nil
	}
	return m.resolveID(r, kind, field, reference)
}

// txnIDs reads a stored audit trail of transaction ids, minus the ones we do
// not have. A dangling id is a warning, not an error: Simplifi's closed months
// name rows since deleted.
func (m *Mapper) txnIDs(storeName, recordID string, r *record, field string) []uuid.UUID {
	out := []uuid.UUID{}
	for _, sourceID := range r.stringList(field) {
		found, ok := m.lookupID(kindTransaction, sourceID)
		if !ok {
			m.report.Warnf(KindMissingReference, storeName, recordID, r.prefix+field,
				"names a transaction that is not in the export; dropped from the audit trail")
			continue
		}
		out = append(out, found)
	}
	return out
}

// -- the walker -------------------------------------------------------

func (m *Mapper) walk(storeName, kind string, build func(sourceID string, r *record)) {
	records, err := m.export.Records(m.datasetID, storeName)
	if err != nil {
		m.report.Errorf(storeName, "", "", "%s", err.Error())
		return
	}
	m.report.Read[storeName] = len(records)
	if kind != "" {
		for sourceID := range records {
			m.allocate(kind, sourceID)
		}
	}
	for _, sourceID := range sortedKeys(records) {
		r := newRecord(records[sourceID])
		m.checkFields(storeName, sourceID, records[sourceID], knownFields[storeName])
		build(sourceID, r)
		if problem := r.Err(); problem != nil {
			m.report.Errorf(storeName, sourceID, problem.Field, "%s", problem.Message)
		}
	}
}

func (m *Mapper) checkFields(storeName, recordID string, data map[string]any, known map[string]bool) {
	m.checkNestedFields(storeName, recordID, "", data, known)
}

// checkNestedFields notes each field the importer was never taught. A later
// exporter adds fields; one the mapping does not read cannot change a row it
// writes, so it is listed rather than refused.
func (m *Mapper) checkNestedFields(storeName, recordID, prefix string, data map[string]any, known map[string]bool) {
	for _, name := range unknownFields(data, known) {
		m.report.NotRead(storeName, recordID, prefix+name)
	}
}

// -- 1. datasets -> space ---------------------------------------------

func (m *Mapper) mapSpace() {
	const storeName = "datasetsStore"
	records, err := m.export.Records(m.datasetID, storeName)
	if err != nil {
		m.report.Errorf(storeName, "", "", "%s", err.Error())
		return
	}
	m.report.Read[storeName] = len(records)

	data := records[m.datasetID]
	if data == nil {
		for _, id := range sortedKeys(records) {
			data = records[id]
			break
		}
	}
	if data == nil {
		m.report.Warnf(KindDefaults, storeName, m.datasetID, "",
			"no dataset record; the space name, currency and timezone are defaults")
		data = map[string]any{}
	} else {
		m.checkFields(storeName, m.datasetID, data, knownFields[storeName])
	}

	r := newRecord(data)
	currency := r.textOr("currency", "USD", 3)
	name := m.options.SpaceName
	if name == "" {
		name = r.textOr("name", r.text("displayName", 255), 255)
	}
	if name == "" {
		name = "Simplifi " + m.datasetID
	}
	timezone := r.textOr("timeZone", r.textOr("timezone", "UTC", 64), 64)
	if problem := r.Err(); problem != nil {
		m.report.Errorf(storeName, m.datasetID, problem.Field, "%s", problem.Message)
		return
	}
	m.currency = currency

	m.out.Space = &store.Space{
		ID:              m.spaceID,
		Name:            name,
		PrimaryCurrency: currency,
		Timezone:        timezone,
	}
	// Alert rules are scoped to a user, so the import writes the space owner.
	now := time.Now().UTC()
	m.out.Users = append(m.out.Users, &store.User{
		ID:       m.ownerID,
		Email:    m.options.OwnerEmail,
		IsActive: true,
	})
	m.report.count("spaces", 1)
	m.report.count("users", 1)
	if m.out.IntoExisting {
		return
	}
	m.out.Memberships = append(m.out.Memberships, &store.Membership{
		ID:         uuid.New(),
		UserID:     m.ownerID,
		Role:       store.RoleOwner,
		AcceptedAt: &now,
	})
	m.report.count("memberships", 1)
}

// -- 2. institutions --------------------------------------------------

func (m *Mapper) mapInstitutions() {
	m.walk("institutionsStore", kindInstitution, func(sourceID string, r *record) {
		institution := &Institution{
			ID:         m.allocate(kindInstitution, sourceID),
			Name:       r.requiredText("name", 255),
			ExternalID: sourceID,
			Domain:     r.textOr("domain", r.text("url", 255), 255),
			LogoURL:    r.text("logoUrl", 500),
		}
		if r.failed() {
			return
		}
		m.out.Institutions = append(m.out.Institutions, institution)
		m.report.count("institutions", 1)
	})

	// Read for its institutionId alone: no connection carries over, and every
	// account arrives unlinked.
	const storeName = "institutionLoginsStore"
	logins, err := m.export.Records(m.datasetID, storeName)
	if err != nil {
		m.report.Errorf(storeName, "", "", "%s", err.Error())
		return
	}
	m.report.Read[storeName] = len(logins)
	for loginID, data := range logins {
		if institutionID, problem := referenceOf(data["institutionId"], "institutionId"); problem == nil && institutionID != "" {
			m.loginsToInstitutions[loginID] = institutionID
		}
	}
	if len(logins) > 0 {
		m.report.Warnf(KindUnlinked, storeName, "", "",
			"%d Simplifi connections are not carried over; every account arrives unlinked "+
				"and needs the SimpleFIN match step", len(logins))
	}
}

// -- 3. accounts ------------------------------------------------------

func (m *Mapper) mapAccounts() {
	const storeName = "accountsStore"
	m.walk(storeName, kindAccount, func(sourceID string, r *record) {
		taxon := m.accountTaxonomy(r)

		institutionID := uuid.Nil
		if loginID := r.reference("institutionLoginId"); loginID != "" {
			if mapped, found := m.loginsToInstitutions[loginID]; found {
				institutionID = m.resolveID(r, kindInstitution, "institutionLoginId", mapped)
			}
		}

		if r.flag("isConnected", false) {
			m.report.Warnf(KindUnlinked, storeName, sourceID, "isConnected",
				"%q was connected in Simplifi; imported unlinked, pending the SimpleFIN match",
				r.text("name", 255))
		}
		if r.flag("isIgnored", false) {
			m.report.Warnf(KindNoColumn, storeName, sourceID, "isIgnored",
				"has no column; the account is imported normally")
		}

		balance, hasBalance := r.optMoney("normalizedBalance")
		if !hasBalance {
			// A manual asset (a house, a car) has its value in onlineBalance
			// alone, already signed.
			balance, hasBalance = r.optMoney("onlineBalance")
		}
		balanceAt := r.optTime("onlineBalanceAt")
		if balanceAt == nil {
			balanceAt = r.optTime("balanceAsOf")
		}

		// The card's terms. The statement figures beside them stay dropped:
		// one cycle's numbers that nothing would refresh after the import.
		limit, hasLimit := r.optMoney("creditLimit")
		if hasLimit && !limit.IsPositive() {
			// Simplifi writes 0 for a card whose limit it never learned.
			limit, hasLimit = domain.Zero, false
		}
		rate, hasRate := r.optRate("interestRate")
		if hasRate {
			// The export does not say whether 24.99 or 0.2499 is meant.
			rate, hasRate = domain.APRAsRate(rate)
		}

		isGoalContainer := key(r.text("type", 64)) == "GOAL"
		account := &store.Account{
			ID:            m.allocate(kindAccount, sourceID),
			InstitutionID: institutionID,
			Name:          r.requiredText("name", 255),
			Description:   r.text("description", 500),
			Notes:         r.text("notes", 0),
			Kind:          taxon.kind,
			Type:          taxon.accountType,
			UsageType:     r.text("usageType", 32),
			Currency:      r.textOr("currency", m.currency, 3),
			MaskedNumber:  m.maskedNumber(storeName, sourceID, r),

			ProviderBalance:    balance,
			HasProviderBalance: hasBalance,
			ProviderBalanceAt:  balanceAt,
			GoalBalance:        r.moneyOr("goalBalance", domain.Zero),

			CreditLimit:     limit,
			HasCreditLimit:  hasLimit,
			InterestRate:    rate,
			HasInterestRate: hasRate,

			ExcludedFromReports:      r.flag("isExcludedFromReports", false),
			ExcludedFromSpendingPlan: r.flag("isExcludedFromBudgets", false),
			ExcludedFromAccountBar:   r.flag("isExcludedFromAccountBar", false),
			ExcludeBankPending:       r.flag("isBankPendingTxnsExcluded", false),
			// Set explicitly: Go's zero would take every account out of net
			// worth. A GOAL container's money is counted again in the real
			// account's goalBalance, so the container is not counted.
			IncludeInNetWorth: !isGoalContainer,

			IsClosed:  r.flag("isClosed", false),
			ClosedOn:  r.optDate("closedOn"),
			IsDeleted: r.flag("isDeleted", false),
		}
		if r.failed() {
			return
		}
		if isGoalContainer {
			if m.goalContainers == nil {
				m.goalContainers = map[uuid.UUID]bool{}
			}
			m.goalContainers[account.ID] = true
		}
		m.out.Accounts = append(m.out.Accounts, account)
		m.accountsByID[account.ID] = account
		m.report.count("accounts", 1)
	})
}

// categoryParent reads parentId, where "0" means the category is a root.
func (m *Mapper) categoryParent(r *record) uuid.UUID {
	if parent := r.reference("parentId"); parent == "" || parent == "0" {
		return uuid.Nil
	}
	return m.optionalRef(r, kindCategory, "parentId")
}

// accountTaxonomy translates type/subType to our kind plus picker label.
// subType, the finer fact, wins where present; an unknown one is an error
// rather than a fallback, which could put a debt on the wrong side of net
// worth.
func (m *Mapper) accountTaxonomy(r *record) accountTaxon {
	// Simplifi writes subType="UNKNOWN" for a house, a car or a savings goal,
	// which carry their kind on type.
	if r.present("subType") && key(r.text("subType", 64)) != "UNKNOWN" {
		return lookup(r, accountSubtypes, "subType", r.value("subType"), "account subtype")
	}
	return lookup(r, accountTypes, "type", r.value("type"), "account type")
}

func (m *Mapper) maskedNumber(storeName, recordID string, r *record) string {
	masked := r.text("accountNumberMasked", 0)
	if masked == "" {
		return ""
	}
	if utf8.RuneCountInString(masked) > 8 {
		// The relink match pairs on the trailing digits, so keep the tail.
		m.report.Warnf(KindNoColumn, storeName, recordID, "accountNumberMasked",
			"is %d characters; the column holds 8, so the tail is kept", utf8.RuneCountInString(masked))
		return keepTail(masked, 8)
	}
	return masked
}

// -- 4. categories ----------------------------------------------------

func (m *Mapper) mapCategories() {
	const storeName = "categoryStore"
	m.walk(storeName, kindCategory, func(sourceID string, r *record) {
		if r.present("usageType") {
			m.report.Warnf(KindNoColumn, storeName, sourceID, "usageType", "categories have no usage_type column")
		}
		knownID := r.text("knownCategoryId", 64)
		category := &store.Category{
			ID:              m.allocate(kindCategory, sourceID),
			ParentID:        m.categoryParent(r),
			Name:            r.requiredText("name", 255),
			Kind:            lookup(r, categoryKinds, "type", r.value("type"), "category type"),
			KnownCategoryID: knownID,
			TxfID:           r.text("txfId", 32),
			TxfIDs:          r.stringList("txfIds"),

			IsUserAssignable: !r.flag("isNotUserAssignable", false),
			IsEditable:       !r.flag("isNotEditable", false),

			ExcludedFromReports:      r.flag("isExcludedFromReports", false),
			ExcludedFromSpendingPlan: r.flag("isExcludedFromBudgets", false),
			ExcludedFromCategoryList: r.flag("isExcludedFromCategoryList", false),

			IsDeleted: r.flag("isDeleted", false),
		}
		if r.failed() {
			return
		}
		if knownID != "" {
			m.knownCategories[knownID] = category.ID
		}
		m.out.Categories = append(m.out.Categories, category)
		m.report.count("categories", 1)
	})
}

// -- 5. tags ----------------------------------------------------------

func (m *Mapper) mapTags() {
	byName := map[string]string{}
	m.walk("tagStore", kindTag, func(sourceID string, r *record) {
		name := r.requiredText("name", 255)
		if r.failed() {
			return
		}
		folded := strings.ToLower(name)
		if first, seen := byName[folded]; seen {
			r.fail("name", "duplicates tag %q; tag names are unique per space", first)
			return
		}
		byName[folded] = sourceID
		tag := &store.Tag{
			ID:        m.allocate(kindTag, sourceID),
			Name:      name,
			Color:     r.textOr("color", r.text("colour", 16), 16),
			IsDeleted: r.flag("isDeleted", false),
		}
		if r.failed() {
			return
		}
		m.tagsBySourceID[sourceID] = tag
		m.out.Tags = append(m.out.Tags, tag)
		m.report.count("tags", 1)
	})
}

// -- 6. filters, before anything that references one ------------------

func (m *Mapper) mapFilters() {
	const storeName = "filterStore"
	m.walk(storeName, kindFilter, func(sourceID string, r *record) {
		scope := scopeAdHoc
		if r.present("type") {
			scope = lookup(r, filterScopes, "type", r.value("type"), "filter scope")
		}
		filter := &store.Filter{
			ID:        m.allocate(kindFilter, sourceID),
			Name:      r.text("name", 255),
			Scope:     scope,
			QueryText: r.text("queryText", 1000),
			IsDeleted: r.flag("isDeleted", false),
		}
		if r.failed() {
			return
		}
		filter.Items = m.filterItems(storeName, sourceID, filter.ID, r, "filterItems")
		if r.failed() {
			return
		}
		m.out.Filters = append(m.out.Filters, filter)
		m.report.count("filters", 1)
		m.report.count("filter_items", len(filter.Items))
	})
}

func (m *Mapper) filterItems(storeName, recordID string, filterID uuid.UUID, r *record, field string) []store.FilterItem {
	raw, present := r.list(field)
	if !present || r.failed() {
		return nil
	}
	out := make([]store.FilterItem, 0, len(raw))
	for position, entry := range raw {
		data, ok := entry.(map[string]any)
		if !ok {
			r.fail(field, "expected an object, found %s", typeName(entry))
			return nil
		}
		m.checkNestedFields(storeName, recordID, field+".", data, filterItemFields)
		item := m.filterItem(r.nested(field, data), filterID, position)
		if r.failed() {
			return nil
		}
		out = append(out, item)
	}
	return out
}

func (m *Mapper) filterItem(r *record, filterID uuid.UUID, position int) store.FilterItem {
	field := lookup(r, filterFields, "type", r.value("type"), "facet")
	// The export carries a facet's ids in a list named after the facet
	// (chartOfAccounts, payees) as well as the generic ids/values.
	rawIDs := append(r.stringList("ids"), r.stringList("values")...)
	rawIDs = append(rawIDs, r.stringList("chartOfAccounts")...)
	rawIDs = append(rawIDs, r.stringList("payees")...)

	var valueIDs []uuid.UUID
	var valueTexts []string
	if kind, isJoinable := filterIDFields[field]; isJoinable {
		valueIDs = make([]uuid.UUID, 0, len(rawIDs))
		for _, one := range rawIDs {
			valueIDs = append(valueIDs, m.resolveID(r, kind, "ids", one))
		}
	} else {
		valueTexts = rawIDs
	}

	operator := domain.OpIn
	if r.present("operator") {
		operator = lookup(r, filterOperators, "operator", r.value("operator"), "operator")
	}

	amountMin, hasMin := r.optMoney("min")
	if !hasMin {
		amountMin, hasMin = r.optMoney("minAmount")
	}
	amountMax, hasMax := r.optMoney("max")
	if !hasMax {
		amountMax, hasMax = r.optMoney("maxAmount")
	}
	dateFrom := r.optDate("startDate")
	if dateFrom.IsZero() {
		dateFrom = r.optDate("from")
	}
	dateTo := r.optDate("endDate")
	if dateTo.IsZero() {
		dateTo = r.optDate("to")
	}

	return store.FilterItem{
		ID:         uuid.New(),
		FilterID:   filterID,
		Field:      string(field),
		Operator:   string(operator),
		GroupIndex: r.intOr("groupIndex", 0),
		Position:   position,
		Negated:    r.flag("negated", false) || r.flag("isNegated", false),

		ValueIDs:   valueIDs,
		ValueTexts: valueTexts,
		Text:       r.text("text", 500),

		AmountMin:    amountMin,
		HasAmountMin: hasMin,
		AmountMax:    amountMax,
		HasAmountMax: hasMax,

		DateFrom:   dateFrom,
		DateTo:     dateTo,
		DatePreset: r.text("datePreset", 32),
		State:      r.optFlag("state"),
	}
}

// -- the audit --------------------------------------------------------

// auditStores accounts for every store in the export that no step read:
// skipped with its reason, or noted as one the importer does not know.
func (m *Mapper) auditStores() {
	for _, storeName := range m.export.StoreNames(m.datasetID) {
		if _, handled := knownFields[storeName]; handled {
			continue
		}
		reason, ignored := IgnoredStores[storeName]
		if !ignored {
			m.report.NotRead(storeName, "", "")
			continue
		}
		m.report.IgnoredStores[storeName] = reason
		if _, counted := m.report.Read[storeName]; !counted {
			records, err := m.export.Records(m.datasetID, storeName)
			if err == nil {
				m.report.Read[storeName] = len(records)
			}
		}
	}
	if len(m.envelopeIDsSeen) > 0 {
		// Whether Simplifi's envelope id is stable across months is not
		// known, so the import reports it either way.
		if m.envelopeIDsRepeated {
			m.report.Warnf(KindSpendingPlan, "freeToSpendStore", "", "plannedSpendingItems.id",
				"envelope ids repeat across months, so recurring_group_id follows them")
		} else {
			m.report.Warnf(KindSpendingPlan, "freeToSpendStore", "", "plannedSpendingItems.id",
				"envelope ids are unique per month, so a recurring envelope's history is "+
					"only reconstructable through recurring_group_id")
		}
	}
}
