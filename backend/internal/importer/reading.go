package importer

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Reading the Simplifi export: the envelope, and the coercions. The shape is
// the one tools/extractors/extract-simplifi.js writes:
//
//	{exportedAt, appVersion, source,
//	 datasets: {datasetId: {storeName: {version, data: {resourcesById: {id: record}}}}}}
//
// Amounts arrive as JSON numbers and must never pass through float64, so
// ParseExport uses UseNumber and a float64 reaching a coercion is an error.
// An absent field reads as zero plus false; a value a coercion cannot
// represent is a FieldError, never a substituted zero.

// IgnoredStores holds stores with no data of ours, each with the reason it is
// skipped, so "not imported" is never mistaken for "not noticed".
var IgnoredStores = map[string]string{
	"authStore":                    "bearer tokens; never written to disk",
	"entitlementsStore":            "Quicken subscription plan",
	"profileStore":                 "Quicken identity",
	"simplifiCacheMeta":            "client plumbing",
	"appStore":                     "client plumbing",
	"featureFlagsStore":            "client plumbing",
	"postponedActionsStore":        "client plumbing",
	"domainLogosStore":             "logo cache",
	"tickerLogosStore":             "logo cache",
	"investmentsNewsfeedStore":     "ticker news",
	"achievementsStore":            "gamification; no financial value",
	"milestonesStore":              "gamification; no financial value",
	"fundingAccountStore":          "real ACH rails; out of scope",
	"a2aTransfersStore":            "real ACH rails; out of scope",
	"billPresentmentSlice":         "Bill Connect e-bill data; out of scope",
	"institutionLoginsStore":       "aggregator links carry no portable credential",
	"preferencesStore":             "client preferences, not financial data",
	"preferencesV2Store":           "client preferences, not financial data",
	"alertsStore":                  "the alert feed is regenerated; only the rules carry over",
	"subscriptionsStore":           "subscription classification rides transaction.isSubscription",
	"accountsValueChangesStore":    "period deltas, derived from balance history",
	"accountsBalancesHistoryStore": "metadata for accountsBalancesStore",
	"investmentIRRStore":           "performance series, recomputed",
	"investmentTWRRStore":          "performance series, recomputed",
	"documentStore":                "read through transaction.attachments, not on its own",
}

// Export is one parsed export file, addressed by dataset and store.
type Export struct {
	Raw        map[string]any
	ExportedAt string
	AppVersion string
}

// ParseExport decodes the file the extractor downloaded. UseNumber keeps every
// amount out of float64.
func ParseExport(data []byte) (*Export, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw map[string]any
	if err := decoder.Decode(&raw); err != nil {
		return nil, fmt.Errorf("importer: cannot parse the export as JSON: %w", err)
	}
	return NewExport(raw)
}

// Simplifi's client store is Immutable.js, and its serializer leaves four
// markers behind that are notation rather than data:
//
//   - "#IMMUTABLE_RECORD_<hash>" and "#IMMUTABLE_MAP_<hash>" appear as keys on
//     the object they describe, carrying the bare value true; the map marker
//     sits inside resourcesById alongside the real records.
//   - "#IMMUTABLE_LIST_<hash>" appears as an element of the list it describes.
//   - "#UNDEFINED_<hash>" is how the serializer writes undefined, so the key
//     goes.
//
// The hashes are not a documented constant, so the prefix is matched.
const (
	immutableMarkerPrefix = "#IMMUTABLE_"
	undefinedMarker       = "#UNDEFINED_"
)

func isImmutableMarker(key string) bool { return strings.HasPrefix(key, immutableMarkerPrefix) }

func isUndefined(value any) bool {
	text, ok := value.(string)
	return ok && strings.HasPrefix(text, undefinedMarker)
}

// scrubSentinels removes the serializer's markers in place, depth first.
func scrubSentinels(node any) any {
	switch typed := node.(type) {
	case map[string]any:
		for key, value := range typed {
			if isImmutableMarker(key) || isUndefined(value) {
				delete(typed, key)
				continue
			}
			typed[key] = scrubSentinels(value)
		}
		return typed
	case []any:
		out := typed[:0]
		for _, value := range typed {
			if isUndefined(value) {
				continue
			}
			if text, ok := value.(string); ok && isImmutableMarker(text) {
				continue
			}
			out = append(out, scrubSentinels(value))
		}
		return out
	default:
		return node
	}
}

func NewExport(raw map[string]any) (*Export, error) {
	if raw == nil {
		return nil, fmt.Errorf("importer: not a Simplifi export: the file holds no object")
	}
	if _, found := raw["datasets"]; !found {
		return nil, fmt.Errorf(
			"importer: not a Simplifi export: no 'datasets' key. " +
				"Expected the file tools/extractors/extract-simplifi.js writes")
	}
	scrubSentinels(raw)
	export := &Export{Raw: raw}
	export.ExportedAt, _ = raw["exportedAt"].(string)
	export.AppVersion, _ = raw["appVersion"].(string)
	return export, nil
}

func (e *Export) datasets() map[string]any {
	found, _ := e.Raw["datasets"].(map[string]any)
	return found
}

func (e *Export) DatasetIDs() []string {
	out := make([]string, 0, len(e.datasets()))
	for id := range e.datasets() {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}

func (e *Export) stores(datasetID string) (map[string]any, error) {
	dataset, found := e.datasets()[datasetID]
	if !found {
		return nil, fmt.Errorf("importer: no dataset %q in the export; have %v", datasetID, e.DatasetIDs())
	}
	stores, _ := dataset.(map[string]any)
	return stores, nil
}

func (e *Export) StoreNames(datasetID string) []string {
	stores, err := e.stores(datasetID)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(stores))
	for name := range stores {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

// Records returns resourcesById for one store, or an empty map if it never
// hydrated (the page that populates it was never opened during extraction).
func (e *Export) Records(datasetID, storeName string) (map[string]map[string]any, error) {
	stores, err := e.stores(datasetID)
	if err != nil {
		return nil, err
	}
	wrapped, _ := stores[storeName].(map[string]any)
	data, _ := wrapped["data"].(map[string]any)
	resources, present := data["resourcesById"]
	if !present || resources == nil {
		return map[string]map[string]any{}, nil
	}
	asMap, ok := resources.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("%s: resourcesById is %T, not an object", storeName, resources)
	}
	out := make(map[string]map[string]any, len(asMap))
	for id, value := range asMap {
		if asRecord, ok := value.(map[string]any); ok {
			out[id] = asRecord
		}
	}
	return out, nil
}

// -- coercions ------------------------------------------------------------

// errBox is one record's first problem, shared with the nested readers a split
// line or a filter clause is read through so that a problem inside one reaches
// the walker that discards the whole record.
type errBox struct{ err *FieldError }

// record reads one export record. Mapping a record is all-or-nothing: the
// first problem sticks, later coercions return zero values, and the caller
// checks once at the end.
type record struct {
	data   map[string]any
	prefix string
	box    *errBox
}

func newRecord(data map[string]any) *record {
	if data == nil {
		data = map[string]any{}
	}
	return &record{data: data, box: &errBox{}}
}

// nested reads an object inside a record, reporting its fields under a dotted
// name.
func (r *record) nested(field string, data map[string]any) *record {
	return &record{data: data, prefix: r.prefix + field + ".", box: r.box}
}

func (r *record) Err() *FieldError { return r.box.err }
func (r *record) failed() bool     { return r.box.err != nil }

func (r *record) fail(field, format string, args ...any) {
	if r.box.err != nil {
		return
	}
	r.box.err = fieldErrorf(r.prefix+field, format, args...)
}

func (r *record) adopt(err *FieldError) {
	if err == nil || r.box.err != nil {
		return
	}
	r.box.err = err
}

func (r *record) value(field string) any { return r.data[field] }

// present reports a field that carries a value. JSON null and an absent key
// are the same fact: Simplifi wrote nothing there.
func (r *record) present(field string) bool {
	value, found := r.data[field]
	return found && value != nil
}

func (r *record) object(field string) (map[string]any, bool) {
	value := r.data[field]
	if value == nil {
		return nil, false
	}
	asMap, ok := value.(map[string]any)
	if !ok {
		r.fail(field, "expected an object, found %s", typeName(value))
		return nil, false
	}
	return asMap, true
}

func (r *record) list(field string) ([]any, bool) {
	value := r.data[field]
	if value == nil {
		return nil, false
	}
	asList, ok := value.([]any)
	if !ok {
		r.fail(field, "expected a list, found %s", typeName(value))
		return nil, false
	}
	return asList, true
}

// -- amounts --------------------------------------------------------------

// number is the one route from a JSON value to an exact decimal, through
// domain.MoneyFromJSONValue: bool and float64 are refused, since a float64
// has already lost digits.
func (r *record) number(field string, value any) (decimal.Decimal, bool) {
	amount, err := domain.MoneyFromJSONValue(value)
	if err != nil {
		r.fail(field, "%s", err)
		return decimal.Zero, false
	}
	return amount.Decimal(), true
}

func (r *record) money(field string) domain.Money {
	value := r.data[field]
	if value == nil || value == "" {
		r.fail(field, "required amount is missing")
		return domain.Zero
	}
	parsed, _ := r.number(field, value)
	return domain.FromDecimal(parsed)
}

// moneyOr reads an amount that has a documented default — a bucket Simplifi
// leaves out when it is zero.
func (r *record) moneyOr(field string, fallback domain.Money) domain.Money {
	value := r.data[field]
	if value == nil || value == "" {
		return fallback
	}
	parsed, _ := r.number(field, value)
	return domain.FromDecimal(parsed)
}

// optMoney reads a nullable amount, reporting whether it was set. An override
// nobody set is not the same fact as an override of zero.
func (r *record) optMoney(field string) (domain.Money, bool) {
	value := r.data[field]
	if value == nil || value == "" {
		return domain.Zero, false
	}
	parsed, ok := r.number(field, value)
	return domain.FromDecimal(parsed), ok
}

// optRate reads a share count or a price, through the same exact coercion as
// money.
func (r *record) optRate(field string) (domain.Rate, bool) {
	value := r.data[field]
	if value == nil || value == "" {
		return decimal.Zero, false
	}
	return r.number(field, value)
}

// -- dates ----------------------------------------------------------------

// parseDate accepts 2026-08-15 and 2026-08-21T18:00:00.000Z alike; Simplifi
// mixes the two, and an instant is truncated to its day (domain.ParseDate).
func parseDate(text string) (domain.Date, bool) {
	parsed, err := domain.ParseDate(text)
	if err != nil {
		return domain.Date{}, false
	}
	return parsed, true
}

func (r *record) date(field string) domain.Date {
	value := r.data[field]
	if value == nil || value == "" {
		r.fail(field, "required date is missing")
		return domain.Date{}
	}
	return r.readDate(field, value)
}

// optDate returns the zero date for an absent value. domain.Date's zero is
// what the store writes as NULL, so "no date" survives to the column.
func (r *record) optDate(field string) domain.Date {
	value := r.data[field]
	if value == nil || value == "" {
		return domain.Date{}
	}
	return r.readDate(field, value)
}

func (r *record) readDate(field string, value any) domain.Date {
	text, ok := value.(string)
	if !ok {
		r.fail(field, "not a date: %s", typeName(value))
		return domain.Date{}
	}
	parsed, ok := parseDate(text)
	if !ok {
		r.fail(field, "not an ISO date: %q", text)
		return domain.Date{}
	}
	return parsed
}

func (r *record) optTime(field string) *time.Time {
	value := r.data[field]
	if value == nil || value == "" {
		return nil
	}
	if number, isNumber := value.(json.Number); isNumber {
		// An epoch, and 0 is Simplifi's "never". Seconds and milliseconds
		// both appear and are told apart by magnitude.
		epoch, err := number.Int64()
		if err != nil || epoch == 0 {
			return nil
		}
		if epoch > 1e11 {
			epoch /= 1000
		}
		at := time.Unix(epoch, 0).UTC()
		return &at
	}
	text, ok := value.(string)
	if !ok {
		r.fail(field, "not a timestamp: %s", typeName(value))
		return nil
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05", "2006-01-02"} {
		if parsed, err := time.Parse(layout, text); err == nil {
			return &parsed
		}
	}
	r.fail(field, "not an ISO timestamp: %q", text)
	return nil
}

// monthStart reads freeToSpendStore.date — "2026-08" — as the first of that
// month. Every month row must land on the first, or two rows for one month
// would both satisfy the unique constraint.
func (r *record) monthStart(field string) domain.Date {
	text, ok := r.data[field].(string)
	if !ok || text == "" {
		r.fail(field, "not a month: %v", r.data[field])
		return domain.Date{}
	}
	if len(text) == 7 {
		month, ok := domain.ParseMonth(text)
		if !ok {
			r.fail(field, "not a month: %q", text)
			return domain.Date{}
		}
		return month.FirstDay()
	}
	parsed, ok := parseDate(text)
	if !ok {
		r.fail(field, "not a month: %q", text)
		return domain.Date{}
	}
	return domain.NewDate(parsed.Year, parsed.Month, 1)
}

// -- flags, text, ids -----------------------------------------------------

// flag reads a boolean that refuses to guess: no truthiness, and an absent
// flag takes the fallback.
func (r *record) flag(field string, fallback bool) bool {
	value := r.data[field]
	if value == nil {
		return fallback
	}
	asBool, ok := value.(bool)
	if !ok {
		r.fail(field, "expected a boolean, found %s", typeName(value))
		return fallback
	}
	return asBool
}

// optFlag keeps the third state a rule action needs: null means "leave alone".
func (r *record) optFlag(field string) *bool {
	value := r.data[field]
	if value == nil {
		return nil
	}
	asBool, ok := value.(bool)
	if !ok {
		r.fail(field, "expected a boolean, found %s", typeName(value))
		return nil
	}
	return &asBool
}

// text reads an optional string, empty when absent. limit of zero means the
// column has no length the value can exceed.
func (r *record) text(field string, limit int) string {
	value := r.data[field]
	if value == nil {
		return ""
	}
	asString, ok := value.(string)
	if !ok {
		r.fail(field, "expected text, found %s", typeName(value))
		return ""
	}
	asString = strings.TrimSpace(asString)
	if asString == "" {
		return ""
	}
	// Characters, not bytes: a varchar(255) in Postgres holds 255 characters,
	// and a length check in bytes rejects an accented payee that fits.
	if length := utf8.RuneCountInString(asString); limit > 0 && length > limit {
		r.fail(field, "text is %d characters; the column holds %d", length, limit)
		return ""
	}
	return asString
}

// keepTail keeps the last n characters, which for a masked account number is
// the part the relink match screen pairs on.
func keepTail(text string, n int) string {
	runes := []rune(text)
	if len(runes) <= n {
		return text
	}
	return string(runes[len(runes)-n:])
}

// emoji reads a field Simplifi writes as codepoint notation ("U+1f413", or a
// space- or hyphen-separated sequence), decoding it to the glyph. The limit
// applies to the decoded glyph; anything else passes through unchanged.
func (r *record) emoji(field string, limit int) string {
	raw := r.text(field, 0)
	decoded := decodeCodepoints(raw)
	if length := utf8.RuneCountInString(decoded); limit > 0 && length > limit {
		r.fail(field, "emoji is %d characters; the column holds %d", length, limit)
		return ""
	}
	return decoded
}

// decodeCodepoints turns "U+1f413" into 🐓, and leaves anything else alone.
func decodeCodepoints(raw string) string {
	if raw == "" || !strings.Contains(strings.ToUpper(raw), "U+") {
		return raw
	}
	parts := strings.FieldsFunc(raw, func(c rune) bool { return c == ' ' || c == '-' || c == '_' })
	var out strings.Builder
	for _, part := range parts {
		digits, found := strings.CutPrefix(strings.ToUpper(part), "U+")
		if !found {
			return raw
		}
		point, err := strconv.ParseInt(digits, 16, 32)
		if err != nil || !utf8.ValidRune(rune(point)) {
			return raw
		}
		out.WriteRune(rune(point))
	}
	return out.String()
}

func (r *record) textOr(field, fallback string, limit int) string {
	if found := r.text(field, limit); found != "" {
		return found
	}
	return fallback
}

func (r *record) requiredText(field string, limit int) string {
	found := r.text(field, limit)
	if found == "" && !r.failed() {
		r.fail(field, "required text is missing")
	}
	return found
}

func (r *record) optInt(field string) (int, bool) {
	value := r.data[field]
	if value == nil {
		return 0, false
	}
	number, ok := value.(json.Number)
	if !ok {
		r.fail(field, "expected a whole number, found %s", typeName(value))
		return 0, false
	}
	whole, err := number.Int64()
	if err != nil {
		r.fail(field, "expected a whole number, found %s", number.String())
		return 0, false
	}
	return int(whole), true
}

func (r *record) intOr(field string, fallback int) int {
	if found, ok := r.optInt(field); ok {
		return found
	}
	return fallback
}

// stringList reads a list of ids. Anything that is not a list of strings or of
// {id} objects is a shape change and says so.
func (r *record) stringList(field string) []string {
	items, ok := r.list(field)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		switch typed := item.(type) {
		case string:
			out = append(out, typed)
		case map[string]any:
			if id, ok := typed["id"].(string); ok && id != "" {
				out = append(out, id)
				continue
			}
			r.fail(field, "list holds an object with no id: %v", typed)
			return nil
		default:
			r.fail(field, "list holds %s, not ids", typeName(item))
			return nil
		}
	}
	return out
}

// reference reads a foreign key that Simplifi writes as either an id or a
// {id, type} object; coa, transfer and matchedTxn appear in both shapes.
func (r *record) reference(field string) string {
	found, err := referenceOf(r.data[field], r.prefix+field)
	r.adopt(err)
	return found
}

func referenceOf(value any, field string) (string, *FieldError) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return typed, nil
	case json.Number:
		// Some ids are written unquoted, transactionRuleId among them. The id
		// table is keyed on the text form either way.
		return typed.String(), nil
	case map[string]any:
		for _, key := range []string{"id", "txnId", "transactionId", "categoryId", "accountId"} {
			if found, ok := typed[key].(string); ok && found != "" {
				return found, nil
			}
		}
		return "", nil
	default:
		return "", fieldErrorf(field, "not a reference: %s", typeName(value))
	}
}

// coaKind is what a coa ("category or account") reference is tagged with. A
// coa naming an account is a transfer leg; its id is never a category id.
const (
	coaCategory          = "CATEGORY"
	coaAccount           = "ACCOUNT"
	coaUncategorized     = "UNCATEGORIZED"
	coaBalanceAdjustment = "BALANCE_ADJUSTMENT"
)

// taggedRef reads a {type, id} reference. A bare string, as older stores
// write, is reported as a category.
func (r *record) taggedRef(field string) (kind, id string) {
	switch typed := r.data[field].(type) {
	case nil:
		return "", ""
	case string:
		return coaCategory, typed
	case map[string]any:
		tag, _ := typed["type"].(string)
		if tag == "" {
			tag = coaCategory
		}
		found, err := referenceOf(typed, r.prefix+field)
		r.adopt(err)
		return key(tag), found
	default:
		r.fail(field, "not a reference: %s", typeName(r.data[field]))
		return "", ""
	}
}

// unknownFields lists the fields the importer has never been taught about, so
// a person decides whether each needs a column or a place on a drop list.
func unknownFields(data map[string]any, known map[string]bool) []string {
	var out []string
	for key := range data {
		if !known[key] {
			out = append(out, key)
		}
	}
	sort.Strings(out)
	return out
}

func typeName(value any) string {
	switch value.(type) {
	case json.Number:
		return "number"
	case string:
		return "text"
	case bool:
		return "boolean"
	case map[string]any:
		return "object"
	case []any:
		return "list"
	case nil:
		return "null"
	default:
		return fmt.Sprintf("%T", value)
	}
}
