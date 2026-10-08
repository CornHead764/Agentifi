package api

import (
	"bytes"
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/importer"
	"github.com/CornHead764/agentifi/backend/internal/importer/csvimport"
	"github.com/CornHead764/agentifi/backend/internal/importer/ofximport"
)

// Importing a statement file from the app, through the same readers, mapper
// and idempotent writer the CLI uses; an OFX file becomes the CSV importer's
// rows.
//
// Preview first: the client posts with `dry_run` to see what the file holds,
// then again to commit. A committing upload settles the rows it wrote before
// converting them, so conversion runs over the settled ledger.

func init() {
	Register(Resource{Prefix: "/imports", Routes: func(rt *Routes) {
		rt.Write(http.MethodPost, "/", importFile)
	}})
}

// maxImportBytes caps an upload, generously; it is a cap because the body is
// read into memory to be sniffed.
const maxImportBytes = 32 << 20

func importFile(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := parseUpload(w, r, "file", maxImportBytes); err != nil {
		return err
	}
	file, err := requireUpload(r, "file", maxImportBytes)
	if err != nil {
		return err
	}
	raw := file.Bytes

	dryRun := r.FormValue("dry_run") == "true"
	account := strings.TrimSpace(r.FormValue("account"))

	report := importer.NewReport()
	var (
		mapped   *csvimport.Mapped
		response agentifiv1.ImportFileResponse
	)
	if looksLikeOFX(raw) {
		doc, err := ofximport.Parse(bytes.NewReader(raw))
		if err != nil {
			return errBadRequest("%s could not be read: %s", file.Filename, err)
		}
		for _, one := range doc.Accounts {
			name := account
			if name == "" {
				name = ofximport.AccountName(one)
			}
			response.Accounts = append(response.Accounts, name)
		}
		response.Format, response.Transactions = "ofx", int32(doc.Count())
		mapped = csvimport.Map(report, ofximport.Rows(doc, account, report),
			csvimport.Options{Currency: currencyOf(doc, env), Source: file.Filename})
		response.Summary = csvimport.RenderAs(mapped, "OFX")
	} else {
		rows, readable := csvimport.ReadRows(bytes.NewReader(raw), report)
		if !readable {
			return errBadRequest("%s is not a Simplifi transaction export", file.Filename)
		}
		response.Format, response.Transactions = "csv", int32(len(rows))
		mapped = csvimport.Map(report, rows,
			csvimport.Options{Currency: csvimport.DefaultCurrency})
		for _, one := range mapped.Accounts {
			response.Accounts = append(response.Accounts, one.Row.Name)
		}
		response.Summary = csvimport.Render(mapped)
	}

	response.DryRun = dryRun
	response.Errors = problems(mapped.Report.Errors)
	response.Warnings = warningGroupProtos(mapped.Report.Groups())

	// An unrepresentable file writes nothing at all, whichever mode this is.
	// Half a ledger is worse than none of one.
	if !mapped.Report.OK() {
		return writeProtoJSON(w, http.StatusUnprocessableEntity, &response)
	}
	if dryRun {
		return writeProtoJSON(w, http.StatusOK, &response)
	}

	result, err := csvimport.Write(r.Context(), env.DB, sp.ID(), mapped)
	if err != nil {
		return err
	}
	// Everything the sync does to a pushed row. Not inside the write's
	// transaction, so a failure leaves rows unsettled and answers 500: a
	// re-upload adds and settles nothing, so the user must be told. The rows
	// keep needs_settle, and the next sync (or `agentifi settle`) finishes them.
	if _, err := NewIngest(env).AfterIngest(r.Context(), env.DB, sp.ID(), result.TransactionIDs); err != nil {
		return err
	}
	response.Written = &agentifiv1.ImportWritten{
		Accounts:            int32(result.AccountsCreated),
		Categories:          int32(result.CategoriesCreated),
		Tags:                int32(result.TagsCreated),
		Transactions:        int32(result.TransactionsWritten),
		TransactionsSkipped: int32(result.TransactionsSkipped),
	}
	return writeProtoJSON(w, http.StatusOK, &response)
}

// looksLikeOFX decides the format from the bytes rather than the extension: a
// .qfx served as text/plain or a renamed download is still OFX.
func looksLikeOFX(raw []byte) bool {
	head := raw
	if len(head) > 4096 {
		head = head[:4096]
	}
	upper := strings.ToUpper(string(head))
	return strings.Contains(upper, "<OFX") || strings.Contains(upper, "OFXHEADER")
}

func currencyOf(doc ofximport.Document, env *Env) string {
	for _, account := range doc.Accounts {
		if account.Currency != "" {
			return strings.ToUpper(account.Currency)
		}
	}
	if env.Cfg.PrimaryCurrency != "" {
		return env.Cfg.PrimaryCurrency
	}
	return csvimport.DefaultCurrency
}

// writeProtoJSON answers a plain HTTP route with a message in the procedures'
// JSON form, so a client reads both with one generated type.
func writeProtoJSON(w http.ResponseWriter, status int, message proto.Message) error {
	data, err := jsonCodec{}.Marshal(message)
	if err != nil {
		return err
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(data)
	return nil
}

func warningGroupProtos(groups []importer.Group) []*agentifiv1.ImportWarningGroup {
	out := make([]*agentifiv1.ImportWarningGroup, 0, len(groups))
	for _, group := range groups {
		out = append(out, &agentifiv1.ImportWarningGroup{
			Kind: string(group.Kind), Note: group.Note, Summary: group.Summary,
			Action: group.Action, Count: int32(group.Count), Items: group.Items,
		})
	}
	return out
}

func problems(all []importer.Problem) []string {
	out := make([]string, 0, len(all))
	for _, one := range all {
		out = append(out, one.Render())
	}
	return out
}
