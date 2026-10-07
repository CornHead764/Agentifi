package api

import (
	"bytes"
	"net/http"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/auth"
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

// ImportResponse is what one upload did, or would do.
type ImportResponse struct {
	// Format is "ofx" or "csv", decided from the content rather than the name.
	Format string `json:"format"`
	// DryRun says nothing was written.
	DryRun bool `json:"dry_run"`
	// Summary is the human-readable report, the same text the CLI prints.
	Summary string `json:"summary"`

	Accounts     []string `json:"accounts"`
	Transactions int      `json:"transactions"`

	Errors []string `json:"errors"`
	// Warnings are grouped by kind, notes of what was not read last.
	Warnings []importer.Group `json:"warnings"`

	// Written is absent on a dry run.
	Written *ImportWritten `json:"written"`
}

// ImportWritten is what reached the database.
type ImportWritten struct {
	Accounts            int `json:"accounts"`
	Categories          int `json:"categories"`
	Tags                int `json:"tags"`
	Transactions        int `json:"transactions"`
	TransactionsSkipped int `json:"transactions_skipped"`
}

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
		response ImportResponse
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
		response.Format, response.Transactions = "ofx", doc.Count()
		mapped = csvimport.Map(report, ofximport.Rows(doc, account, report),
			csvimport.Options{Currency: currencyOf(doc, env), Source: file.Filename})
		response.Summary = csvimport.RenderAs(mapped, "OFX")
	} else {
		rows, readable := csvimport.ReadRows(bytes.NewReader(raw), report)
		if !readable {
			return errBadRequest("%s is not a Simplifi transaction export", file.Filename)
		}
		response.Format, response.Transactions = "csv", len(rows)
		mapped = csvimport.Map(report, rows,
			csvimport.Options{Currency: csvimport.DefaultCurrency})
		for _, one := range mapped.Accounts {
			response.Accounts = append(response.Accounts, one.Row.Name)
		}
		response.Summary = csvimport.Render(mapped)
	}

	response.DryRun = dryRun
	response.Errors = problems(mapped.Report.Errors)
	response.Warnings = mapped.Report.Groups()

	// An unrepresentable file writes nothing at all, whichever mode this is.
	// Half a ledger is worse than none of one.
	if !mapped.Report.OK() {
		return writeJSON(w, http.StatusUnprocessableEntity, response)
	}
	if dryRun {
		return writeJSON(w, http.StatusOK, response)
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
	response.Written = &ImportWritten{
		Accounts:            result.AccountsCreated,
		Categories:          result.CategoriesCreated,
		Tags:                result.TagsCreated,
		Transactions:        result.TransactionsWritten,
		TransactionsSkipped: result.TransactionsSkipped,
	}
	return writeJSON(w, http.StatusOK, response)
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

func problems(all []importer.Problem) []string {
	out := make([]string, 0, len(all))
	for _, one := range all {
		out = append(out, one.Render())
	}
	return out
}
