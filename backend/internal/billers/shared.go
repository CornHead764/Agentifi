package billers

import (
	"bytes"
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// Helpers shared by the JSON bill providers. A figure a module cannot read is
// a note and no bill; nothing here ever answers 0.

// Answered is one call's result, with the body kept as text so a non-JSON
// answer is reportable rather than a parse error.
type Answered struct {
	OK     bool
	Status int
	// Body is the parsed JSON, or nil when it was not JSON.
	Body map[string]any
	// Excerpt is the start of what came back, for the note a failure writes.
	Excerpt string
	// Raw is the whole body, for a module that wants the bytes.
	Raw []byte
}

// Ask makes one call. `who` names the provider in a network error.
func Ask(
	ctx context.Context, fetch browser.Fetcher, method, url string,
	headers map[string]string, body []byte, who string,
) (Answered, error) {
	var payload io.Reader
	if len(body) > 0 {
		payload = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, payload)
	if err != nil {
		return Answered{}, err
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	response, err := httpx.Read(fetch, req, askLimit)
	if err != nil && response.Status == 0 {
		return Answered{}, fmt.Errorf("%s could not be reached: %w", who, err)
	}
	if err != nil {
		return Answered{}, fmt.Errorf("%s answered unreadably: %w", who, err)
	}
	answered := Answered{
		OK:      response.OK(),
		Status:  response.Status,
		Excerpt: response.Excerpt(),
		Raw:     response.Body,
	}
	if parsed, ok := decodeBody(response.Body); ok {
		answered.Body = parsed
	}
	return answered, nil
}

// askLimit is the largest answer a module reads, a statement PDF included.
const askLimit = 64 << 20

// decodeBody is a JSON object with its numbers kept as the text the provider
// sent (json.Number), so an amount never passes through a float64.
func decodeBody(raw []byte) (map[string]any, bool) {
	var parsed map[string]any
	if httpx.DecodeJSON(raw, &parsed) != nil {
		return nil, false
	}
	return parsed, true
}

func AskJSON(
	ctx context.Context, fetch browser.Fetcher, method, url string,
	headers map[string]string, body any, who string,
) (Answered, error) {
	encoded, err := json.Marshal(body)
	if err != nil {
		return Answered{}, err
	}
	return Ask(ctx, fetch, method, url, headers, encoded, who)
}

// Envelope is the `{status: {type, message}, data}` shape most utility portals
// answer with, and `{Result}` on older services.
type Envelope struct {
	OK      bool
	Message string
	Data    any
}

func ReadEnvelope(body map[string]any) Envelope {
	if body == nil {
		return Envelope{}
	}
	out := Envelope{}
	if status, ok := body["status"].(map[string]any); ok {
		kind := strings.ToLower(text(status["type"]))
		out.Message = text(status["message"])
		out.OK = kind == "success"
		if kind == "" {
			out.OK = body["data"] != nil || body["Result"] != nil
		}
	} else {
		out.OK = body["data"] != nil || body["Result"] != nil
	}
	if data, held := body["data"]; held {
		out.Data = data
	} else {
		out.Data = body["Result"]
	}
	return out
}

// Rows is the envelope's data as a list of objects, or nothing.
func (e Envelope) Rows() []map[string]any {
	list, ok := e.Data.([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, one := range list {
		if row, ok := one.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

// Row is the envelope's data as one object — the first of a list, or the
// object itself.
func (e Envelope) Row() map[string]any {
	if row, ok := e.Data.(map[string]any); ok {
		return row
	}
	if rows := e.Rows(); len(rows) > 0 {
		return rows[0]
	}
	return nil
}

// Pick is the first of these keys the object carries as a non-empty value.
// Services rename fields between tenants and releases, so a module names every
// spelling it has seen.
func Pick(row map[string]any, keys ...string) any {
	if row == nil {
		return nil
	}
	for _, key := range keys {
		value, held := row[key]
		if !held || value == nil {
			continue
		}
		if strings.TrimSpace(text(value)) == "" {
			continue
		}
		return value
	}
	return nil
}

var (
	isoDay      = regexp.MustCompile(`^(\d{4})-(\d{2})-(\d{2})`)
	americanDay = regexp.MustCompile(`^(\d{2})-(\d{2})-(\d{4})` + clockTime)
	slashedDay  = regexp.MustCompile(`^(\d{1,2})/(\d{1,2})/(\d{4})` + clockTime)
	// aspNetDay is how an ASP.NET MVC JsonResult writes a DateTime:
	// milliseconds since the epoch, with an offset when the value carried one.
	aspNetDay = regexp.MustCompile(`^\\?/Date\((-?\d+)(?:[+-]\d{4})?\)\\?/$`)
)

// clockTime is the time of day an ASP.NET page prints after a date
// ("12/31/2025 12:00:00 AM"). Anything else after the day still fails.
const clockTime = `(?:\s+\d{1,2}:\d{2}(?::\d{2})?(?:\s*[AaPp][Mm])?)?$`

// ISODate is an ISO `YYYY-MM-DD`, or "". MM-DD-YYYY and M/D/YYYY are read by
// hand: a generic date parse of either is ambiguous.
func ISODate(value any) string {
	if value == nil {
		return ""
	}
	raw := strings.TrimSpace(text(value))
	if raw == "" {
		return ""
	}
	if m := americanDay.FindStringSubmatch(raw); m != nil {
		return fmt.Sprintf("%s-%s-%s", m[3], m[1], m[2])
	}
	if m := isoDay.FindStringSubmatch(raw); m != nil {
		return fmt.Sprintf("%s-%s-%s", m[1], m[2], m[3])
	}
	if m := slashedDay.FindStringSubmatch(raw); m != nil {
		return fmt.Sprintf("%s-%02s-%02s", m[3], pad(m[1]), pad(m[2]))
	}
	// The day is read in UTC: a local midnight anywhere west of Greenwich is
	// still that day there.
	if m := aspNetDay.FindStringSubmatch(raw); m != nil {
		if ms, err := strconv.ParseInt(m[1], 10, 64); err == nil {
			return time.UnixMilli(ms).UTC().Format("2006-01-02")
		}
	}
	return ""
}

// DayIn is the first date written in text as an ISO `YYYY-MM-DD`, or "". It
// reads every spelling bill mail uses (billmail.ParseDate), month names
// included, month first throughout.
func DayIn(text string) string {
	if day, ok := billmail.ParseDate(text); ok {
		return day.String()
	}
	return ""
}

func pad(value string) string {
	if len(value) == 1 {
		return "0" + value
	}
	return value
}

// Amount is a provider's figure as money, or false when it cannot be read; a
// bill sent as 0 would say nothing is owed. A page's answer arrives as float64
// because the browser driver decoded it; the shortest round-trip form of a
// float64 is the literal the page wrote, so the figure is read from that text.
func Amount(value any) (domain.Money, bool) {
	switch typed := value.(type) {
	case float64:
		value = json.Number(strconv.FormatFloat(typed, 'f', -1, 64))
	case int:
		value = json.Number(strconv.Itoa(typed))
	}
	amount, err := domain.MoneyFromJSONValue(value)
	return amount, err == nil
}

// Owes says a figure is money the household still has to move. A credit or a
// zero is not owed.
func Owes(amount domain.Money) bool { return amount.IsPositive() }

// Flag reads a JSON boolean or the string of one.
func Flag(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return err == nil && parsed
	}
	return false
}

// text is any scalar as the string a reader wants. A float that is a whole
// number prints as one: an account number that arrived as JSON 12345 must not
// become "12345.000000".
func text(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		if typed == float64(int64(typed)) {
			return fmt.Sprintf("%d", int64(typed))
		}
		return fmt.Sprintf("%g", typed)
	case json.Number:
		return typed.String()
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func Text(value any) string { return text(value) }

// Shape is a JSON value's keys and types and none of its values, for a note
// about an answer that did not carry what a module looked for. A string that
// reads as a day is "date"; a key that looks like an identifier is "<id>".
func Shape(value any) string { return shapeOf(value, 0, shapeKeys) }

// ShapeEvery is Shape naming every key, for an answer whose key names are
// themselves the finding.
func ShapeEvery(value any) string { return shapeOf(value, 0, 0) }

const (
	shapeDepth = 4
	shapeKeys  = 40
)

var identifierKey = regexp.MustCompile(`\d{4,}`)

// keys caps the keys named at each level; zero names them all.
func shapeOf(value any, depth, keys int) string {
	switch typed := value.(type) {
	case nil:
		return "null"
	case bool:
		return "bool"
	case float64, int, int64, json.Number:
		return "number"
	case string:
		if ISODate(typed) != "" {
			return "date"
		}
		return "string"
	case []map[string]any:
		rows := make([]any, len(typed))
		for index, row := range typed {
			rows[index] = row
		}
		return shapeOf(rows, depth, keys)
	case []any:
		if len(typed) == 0 {
			return "[]"
		}
		if depth >= shapeDepth {
			return fmt.Sprintf("[%d]", len(typed))
		}
		return fmt.Sprintf("[%d × %s]", len(typed), shapeOf(typed[0], depth+1, keys))
	case map[string]any:
		if depth >= shapeDepth {
			return "{…}"
		}
		names := make([]string, 0, len(typed))
		for key := range typed {
			names = append(names, key)
		}
		sort.Strings(names)
		parts := make([]string, 0, len(names))
		for index, key := range names {
			if keys > 0 && index == keys {
				parts = append(parts, fmt.Sprintf("… %d more", len(names)-keys))
				break
			}
			name := key
			if identifierKey.MatchString(key) {
				name = "<id>"
			}
			parts = append(parts, name+": "+shapeOf(typed[key], depth+1, keys))
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprintf("%T", value)
}

// jsonish is a figure as a note should print it: what the provider actually
// sent, rather than Go's own rendering of whatever it decoded to.
func jsonish(value any) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "null"
	}
	return string(encoded)
}

// isPDF says the bytes are a statement rather than a page. A portal whose
// session has lapsed answers a statement request with its sign-in page under
// HTTP 200; anything not a PDF is a note and never filed.
func isPDF(body []byte) bool { return strings.HasPrefix(string(body), "%PDF-") }

// pdfDocument is a statement a module has checked with isPDF.
func pdfDocument(body []byte, filename string) *Document {
	return &Document{Bytes: body, ContentType: "application/pdf", Filename: filename}
}

// fetchedPDF is a statement fetched as a plain GET inside the signed-in page;
// what names it in the note when it is not one.
func fetchedPDF(call Call, what, address string) ([]byte, bool) {
	answer, err := AskPage(call, plainPageCall, browser.PageCall{URL: address, Read: browser.ReadBytes})
	if err != nil {
		call.Notes.Addf("%s could not be fetched: %v", what, err)
		return nil, false
	}
	body, err := answer.Bytes()
	if answer.Status != 200 || err != nil || !isPDF(body) {
		call.Notes.Addf("%s answered HTTP %d as %s, not a PDF", what, answer.Status, cmp.Or(answer.Type, "nothing"))
		return nil, false
	}
	return body, true
}

// decodedPDF is a statement a call answered in base64; what names it in the
// note when it is not one.
func decodedPDF(call Call, what, encoded string) ([]byte, bool) {
	body, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil || !isPDF(body) {
		call.Notes.Addf("%s answered something that is not a PDF", what)
		return nil, false
	}
	return body, true
}

func plural(count int) string { return pluralWith(count, "s") }

// pluralWith is plural for a word whose plural is not just an s.
func pluralWith(count int, suffix string) string {
	if count == 1 {
		return ""
	}
	return suffix
}

// statementFilename is the provider, the tail of the billed account, and the
// day the statement belongs to (the module's choice of date: some providers
// write only a due date).
func statementFilename(provider string, bill Bill, stamp string) string {
	return fmt.Sprintf("%s-%s-%s.pdf", provider, domain.LastFour(bill.Subaccount), stamp)
}

// rawOf is what a bill kept for its own second pass: FetchDocument is handed
// the bill and nothing else. No raw block decodes to the zero value, which
// FetchDocument reads as no statement.
func rawOf[T any](bill Bill) T {
	var out T
	if len(bill.Raw) > 0 {
		_ = json.Unmarshal(bill.Raw, &out)
	}
	return out
}
