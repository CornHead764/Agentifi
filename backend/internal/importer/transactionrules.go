package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Simplifi's transaction rules, from the rules file.
//
// Simplifi keeps its transaction rules on the server and never writes them to
// IndexedDB, so the export holds none. Its Rules page reads them from
//
//	GET <service>/transaction-rules?limit=5000  ->  {resources: [rule, ...]}
//
// and that response, saved from the browser's network panel, is the rules
// file. A rule found there is the real definition, and the rows that name it
// link to it instead of to a rule rebuilt from their evidence.
//
// One rule is conditions ANDed together, with the payee condition's criteria
// as alternatives (Simplifi's "+" button). Our filter says the same thing with
// groups: each payee criterion is a group of its own, and the account, amount
// and category conditions are repeated into every group.

const transactionRulesStore = "transactionRulesStore"

// sourceRefTransactionRuleDefinition marks a rule read from the rules file, as
// against sourceRefTransactionRule, one rebuilt from the rows it touched. The
// id after either prefix is the same Simplifi id, which is how a rules-only run
// finds the rebuilt row a definition upgrades.
const sourceRefTransactionRuleDefinition = "simplifi:transaction-rules:"

// AddTransactionRules adds the rules file to one dataset of the export.
//
// It takes the response as the network panel saves it, {resources: [...]}, or
// the bare list.
func (e *Export) AddTransactionRules(datasetID string, data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return fmt.Errorf("importer: cannot parse the rules file as JSON: %w", err)
	}
	var list []any
	switch typed := raw.(type) {
	case []any:
		list = typed
	case map[string]any:
		found, ok := typed["resources"].([]any)
		if !ok {
			return fmt.Errorf("importer: not Simplifi's transaction rules: expected {resources: [...]}, " +
				"the response to GET transaction-rules")
		}
		list = found
	default:
		return fmt.Errorf("importer: not Simplifi's transaction rules: the file holds %s", typeName(raw))
	}
	scrubSentinels(list)

	stores, err := e.stores(datasetID)
	if err != nil {
		return err
	}
	if _, present := stores[transactionRulesStore]; present {
		return fmt.Errorf("importer: the export already holds %s", transactionRulesStore)
	}
	byID := make(map[string]any, len(list))
	for i, item := range list {
		rule, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("importer: rules file entry %d is %s, not a rule", i, typeName(item))
		}
		id, err := referenceOf(rule["id"], "id")
		if err != nil || id == "" {
			return fmt.Errorf("importer: rules file entry %d has no id", i)
		}
		if _, repeated := byID[id]; repeated {
			return fmt.Errorf("importer: rules file holds rule %q twice", id)
		}
		byID[id] = rule
	}
	stores[transactionRulesStore] = map[string]any{"data": map[string]any{"resourcesById": byID}}
	return nil
}

// The nested vocabularies of a transaction rule.
var (
	payeeConditionFields    = set("payeeType", "payeeMatchingCriteria")
	payeeCriterionFields    = set("payeeMatchingOperator", "payeeName")
	amountConditionFields   = set("amountMatchingOperator", "amountType", "amount1", "amount2")
	accountConditionFields  = set("accountSourceType", "accountSourceIds")
	chartOfAccountRefFields = set("id", "type")
)

func (m *Mapper) mapTransactionRules() {
	records, err := m.export.Records(m.datasetID, transactionRulesStore)
	if err != nil || len(records) == 0 {
		return
	}
	priorities := ruleChainOrder(records)
	m.walk(transactionRulesStore, kindTransactionRule, func(sourceID string, r *record) {
		m.mapTransactionRule(sourceID, r, priorities[sourceID])
	})
}

// ruleChainOrder reads Simplifi's rule order, a list linked through
// prevTransactionRuleId and nextTransactionRuleId, as 1, 2, 3. A rule the
// chain does not reach follows the ones it does, by id, so a broken link costs
// that rule its place and not its existence.
func ruleChainOrder(records map[string]map[string]any) map[string]int {
	next := map[string]string{}
	var heads []string
	for id, data := range records {
		if following, err := referenceOf(data["nextTransactionRuleId"], ""); err == nil && following != "" {
			next[id] = following
		}
		if previous, err := referenceOf(data["prevTransactionRuleId"], ""); err != nil || previous == "" {
			heads = append(heads, id)
		}
	}
	slices.Sort(heads)
	order := map[string]int{}
	for _, head := range heads {
		for at := head; at != ""; at = next[at] {
			if _, seen := order[at]; seen {
				break
			}
			if _, known := records[at]; !known {
				break
			}
			order[at] = len(order) + 1
		}
	}
	for _, id := range sortedKeys(records) {
		if _, placed := order[id]; !placed {
			order[id] = len(order) + 1
		}
	}
	return order
}

// transactionRuleDraft collects one rule's conditions before they become
// filter items, and the reasons it cannot run as Simplifi ran it.
type transactionRuleDraft struct {
	payee    []store.FilterItem
	shared   []store.FilterItem
	disabled []string
	firstKey string
}

func (m *Mapper) mapTransactionRule(sourceID string, r *record, priority int) {
	draft := &transactionRuleDraft{}
	m.rulePayeeCondition(sourceID, r, draft)
	m.ruleAccountCondition(sourceID, r, draft)
	m.ruleAmountCondition(sourceID, r, draft)
	m.ruleCategoryConditions(sourceID, r, draft)
	switch key(r.text("usageSourceType", 32)) {
	case "", "PERSONAL":
	default:
		draft.disabled = append(draft.disabled, "it matches on Simplifi's business usage, which has no counterpart here")
	}
	if r.failed() {
		return
	}

	setCategory := uuid.Nil
	if kind, id := r.taggedRef("coaTargetCondition"); id != "" && !r.failed() {
		switch kind {
		case coaCategory:
			found, ok := m.lookupID(kindCategory, id)
			if !ok {
				draft.disabled = append(draft.disabled, fmt.Sprintf("it files rows under category %q, which is not in the export", id))
			}
			setCategory = found
		case coaUncategorized:
		default:
			draft.disabled = append(draft.disabled, fmt.Sprintf("it files rows under a %s, not a category", strings.ToLower(kind)))
		}
	}
	var tags []uuid.UUID
	for _, tagID := range r.stringList("tagsTargetIds") {
		tag, found := m.tagsBySourceID[tagID]
		if !found {
			draft.disabled = append(draft.disabled, fmt.Sprintf("it adds tag %q, which is not in the export", tagID))
			continue
		}
		tags = append(tags, tag.ID)
	}
	if r.present("businessCoaTargetCondition") || r.present("businessTargetId") {
		draft.disabled = append(draft.disabled, "it sets Simplifi's business usage, which has no counterpart here")
	}

	rule := &Rule{
		ID:        m.allocate(kindTransactionRule, sourceID),
		Priority:  priority,
		IsActive:  true,
		IsDeleted: r.flag("isDeleted", false),
		SourceRef: sourceRefTransactionRuleDefinition + sourceID,

		SetPayee:      r.text("payeeTarget", 255),
		SetCategoryID: setCategory,
		AddTagIDs:     tags,
		SetNotes:      r.text("memoTarget", 0),

		// Each is null when the rule leaves the flag alone, and the two are
		// read apart: a rule may exclude from reports and say nothing about
		// the spending plan.
		SetExcludedFromReports:      r.optFlag("isExcludedFromReportsTarget"),
		SetExcludedFromSpendingPlan: r.optFlag("isExcludedFromF2STarget"),
		SetIsReviewed:               r.optFlag("isReviewedTarget"),
	}
	if r.failed() {
		return
	}

	label := rule.SetPayee
	if label == "" {
		label = draft.firstKey
	}
	if label == "" {
		label = "Simplifi rule " + sourceID
	}
	rule.Name = textutil.Clip(label, 255)

	items := draft.items()
	if len(items) == 0 {
		draft.disabled = append(draft.disabled, "it has no condition this import can read, and a rule without one matches every row")
	}
	acts := rule.SetPayee != "" || rule.SetCategoryID != uuid.Nil || len(rule.AddTagIDs) > 0 || rule.SetNotes != "" ||
		rule.SetExcludedFromReports != nil || rule.SetExcludedFromSpendingPlan != nil || rule.SetIsReviewed != nil
	if !acts {
		draft.disabled = append(draft.disabled, "it has no action this import can read")
	}
	if rule.IsDeleted {
		rule.IsActive = false
	} else if len(draft.disabled) > 0 {
		rule.IsActive = false
		m.report.Warnf(KindRuleOff, transactionRulesStore, sourceID, "",
			"rule %q arrives switched off: %s", rule.Name, strings.Join(draft.disabled, "; "))
	}

	filterID := m.allocate(kindRuleFilter, sourceRefTransactionRuleDefinition+sourceID)
	filter := &store.Filter{ID: filterID, Name: rule.Name, Scope: scopeRule}
	for i := range items {
		items[i].ID = uuid.New()
		items[i].FilterID = filterID
	}
	filter.Items = items
	rule.FilterID = filterID
	m.out.Filters = append(m.out.Filters, filter)
	m.report.count("filters", 1)
	m.report.count("filter_items", len(items))
	m.out.Rules = append(m.out.Rules, rule)
	m.report.count("rules", 1)
}

// items lays the conditions out as groups: one per payee alternative, each
// carrying every other condition, or a single group when there is no payee
// condition.
func (d *transactionRuleDraft) items() []store.FilterItem {
	alternatives := d.payee
	if len(alternatives) == 0 {
		alternatives = []store.FilterItem{{}}
	}
	var out []store.FilterItem
	for group, alternative := range alternatives {
		position := 0
		add := func(item store.FilterItem) {
			item.GroupIndex = group
			item.Position = position
			position++
			out = append(out, item)
		}
		if alternative.Field != "" {
			add(alternative)
		}
		for _, item := range d.shared {
			item.ValueIDs = slices.Clone(item.ValueIDs)
			add(item)
		}
	}
	return out
}

// rulePayeeCondition reads the payee alternatives. STATEMENT is the bank's
// wording and INFERRED ("Quicken name") the display payee, so each lands on
// the name ground rule 5 says it is. A Contains criterion is keywords that
// must all be present, which is what our contains operator tests.
func (m *Mapper) rulePayeeCondition(sourceID string, r *record, draft *transactionRuleDraft) {
	condition, present := r.object("payeeSourceCondition")
	if !present {
		return
	}
	m.checkNestedFields(transactionRulesStore, sourceID, "payeeSourceCondition.", condition, payeeConditionFields)
	payee := r.nested("payeeSourceCondition", condition)
	field := domain.FieldStatementName
	switch key(payee.text("payeeType", 32)) {
	case "", "STATEMENT":
	case "INFERRED":
		field = domain.FieldPayee
	default:
		payee.fail("payeeType", "unknown payee type: %q", payee.text("payeeType", 32))
		return
	}
	criteria, _ := payee.list("payeeMatchingCriteria")
	for i, raw := range criteria {
		data, ok := raw.(map[string]any)
		if !ok {
			payee.fail("payeeMatchingCriteria", "entry %d is %s, not a criterion", i, typeName(raw))
			return
		}
		m.checkNestedFields(transactionRulesStore, sourceID,
			fmt.Sprintf("payeeSourceCondition.payeeMatchingCriteria.%d.", i), data, payeeCriterionFields)
		criterion := payee.nested(fmt.Sprintf("payeeMatchingCriteria.%d", i), data)
		name := strings.TrimSpace(criterion.text("payeeName", 500))
		if name == "" {
			continue
		}
		if draft.firstKey == "" {
			draft.firstKey = name
		}
		item := store.FilterItem{Field: string(field)}
		switch key(criterion.text("payeeMatchingOperator", 32)) {
		case "", "CONTAINS":
			item.Operator = string(domain.OpContains)
			item.ValueTexts = strings.FieldsFunc(name, func(c rune) bool { return c == ' ' || c == ',' })
			if len(item.ValueTexts) > 1 {
				m.report.Warnf(KindRuleKeywordOrder, transactionRulesStore, sourceID, "payeeSourceCondition",
					"keywords %q must all be present here, in any order; Simplifi also wants them in that order", name)
			}
		case "EQUALS":
			item.Operator = string(domain.OpIsExactly)
			item.ValueTexts = []string{name}
		default:
			criterion.fail("payeeMatchingOperator", "unknown payee match: %q", criterion.text("payeeMatchingOperator", 32))
			return
		}
		draft.payee = append(draft.payee, item)
	}
}

// ruleAccountCondition reads SPECIFIC accounts as an account condition.
// PERSONAL is every account of a space with no business side, so it adds
// nothing; BUSINESS cannot be said here.
func (m *Mapper) ruleAccountCondition(sourceID string, r *record, draft *transactionRuleDraft) {
	condition, present := r.object("accountSourceCondition")
	if !present {
		return
	}
	m.checkNestedFields(transactionRulesStore, sourceID, "accountSourceCondition.", condition, accountConditionFields)
	account := r.nested("accountSourceCondition", condition)
	switch key(account.text("accountSourceType", 32)) {
	case "", "PERSONAL":
		return
	case "SPECIFIC":
	case "BUSINESS":
		draft.disabled = append(draft.disabled, "it matches Simplifi's business accounts, which have no counterpart here")
		return
	default:
		account.fail("accountSourceType", "unknown account condition: %q", account.text("accountSourceType", 32))
		return
	}
	var ids []uuid.UUID
	for _, accountID := range account.stringList("accountSourceIds") {
		found, ok := m.lookupID(kindAccount, accountID)
		if !ok {
			draft.disabled = append(draft.disabled, fmt.Sprintf("it matches account %q, which is not in the export", accountID))
			continue
		}
		ids = append(ids, found)
	}
	if len(ids) == 0 {
		return
	}
	draft.shared = append(draft.shared, store.FilterItem{
		Field: string(domain.FieldAccount), Operator: string(domain.OpIn), ValueIDs: ids,
	})
}

// ruleAmountCondition reads the amount condition. Simplifi's amounts are
// signed, and its own client reads a positive amount1 as income; an explicit
// amountType wins over the sign. Ours compare magnitudes and carry the
// direction in State, true for income.
func (m *Mapper) ruleAmountCondition(sourceID string, r *record, draft *transactionRuleDraft) {
	condition, present := r.object("amountSourceCondition")
	if !present {
		return
	}
	m.checkNestedFields(transactionRulesStore, sourceID, "amountSourceCondition.", condition, amountConditionFields)
	amount := r.nested("amountSourceCondition", condition)
	first, hasFirst := amount.optMoney("amount1")
	second, hasSecond := amount.optMoney("amount2")
	if amount.failed() {
		return
	}
	income := hasFirst && first.IsPositive()
	switch key(amount.text("amountType", 32)) {
	case "":
	case "INCOME":
		income = true
	case "EXPENSE":
		income = false
	default:
		amount.fail("amountType", "unknown amount type: %q", amount.text("amountType", 32))
		return
	}
	item := store.FilterItem{Field: string(domain.FieldAmount), State: &income}
	switch key(amount.text("amountMatchingOperator", 32)) {
	case "ANY_AMOUNT":
		return
	case "EQUALS":
		item.Operator, item.AmountMin, item.HasAmountMin = string(domain.OpEquals), first.Abs(), hasFirst
	case "GREATER_THAN":
		item.Operator, item.AmountMin, item.HasAmountMin = string(domain.OpGreaterThan), first.Abs(), hasFirst
	case "LESS_THAN":
		item.Operator, item.AmountMax, item.HasAmountMax = string(domain.OpLessThan), first.Abs(), hasFirst
	case "BETWEEN":
		low, high := first.Abs(), second.Abs()
		if low.GreaterThan(high) {
			low, high = high, low
		}
		item.Operator = string(domain.OpBetween)
		item.AmountMin, item.HasAmountMin = low, hasFirst
		item.AmountMax, item.HasAmountMax = high, hasSecond
	default:
		amount.fail("amountMatchingOperator", "unknown amount match: %q", amount.text("amountMatchingOperator", 32))
		return
	}
	if !item.HasAmountMin && !item.HasAmountMax {
		amount.fail("amount1", "the %s condition has no amount", item.Operator)
		return
	}
	draft.shared = append(draft.shared, item)
}

// ruleCategoryConditions reads coaSourceConditions, the categories a row must
// already be in.
func (m *Mapper) ruleCategoryConditions(sourceID string, r *record, draft *transactionRuleDraft) {
	entries, _ := r.list("coaSourceConditions")
	var ids []uuid.UUID
	for i, raw := range entries {
		data, ok := raw.(map[string]any)
		if !ok {
			r.fail("coaSourceConditions", "entry %d is %s, not a category", i, typeName(raw))
			return
		}
		m.checkNestedFields(transactionRulesStore, sourceID, fmt.Sprintf("coaSourceConditions.%d.", i), data, chartOfAccountRefFields)
		kind, id := r.nested("coaSourceConditions", map[string]any{"coa": data}).taggedRef("coa")
		if id == "" {
			continue
		}
		if kind != coaCategory {
			draft.disabled = append(draft.disabled, fmt.Sprintf("it matches rows filed under a %s, not a category", strings.ToLower(kind)))
			continue
		}
		found, ok := m.lookupID(kindCategory, id)
		if !ok {
			draft.disabled = append(draft.disabled, fmt.Sprintf("it matches category %q, which is not in the export", id))
			continue
		}
		ids = append(ids, found)
	}
	if len(ids) == 0 {
		return
	}
	draft.shared = append(draft.shared, store.FilterItem{
		Field: string(domain.FieldCategory), Operator: string(domain.OpIn), ValueIDs: ids,
	})
}
