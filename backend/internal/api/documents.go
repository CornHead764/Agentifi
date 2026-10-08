package api

import (
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Documents: every file the household holds, linked to whatever it is a
// document of. /attachments is the transaction-scoped view of the same store.
//
// The content route answers bytes, not JSON, which an in-process dispatch
// caller has to expect; the metadata route is the one to ask.
//
// No listing carries bytes, so a household's statements never ride along in a
// register refresh or the logs and caches on the way.

func init() {
	Register(Resource{Prefix: "/documents", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listDocuments)
		rt.Write(http.MethodPost, "/", uploadDocument)
		rt.Read(http.MethodGet, "/{document_id}", readDocument)
		rt.Read(http.MethodGet, "/{document_id}/content", downloadDocument)
	}})
}

type DocumentResponse struct {
	ID       uuid.UUID `json:"id"`
	Filename string    `json:"filename"`
	// ContentType is sniffed from the bytes by the server, never the
	// uploader's word for it.
	ContentType string `json:"content_type"`
	SizeBytes   int    `json:"size_bytes"`
	// URL is relative to the API mount point and served by this application,
	// not the storage backend, because the permission check lives here.
	URL string `json:"url"`
	// Source is where the file came from: upload, bill_pull, merchant_pull,
	// email, receipt_scan.
	Source    string `json:"source"`
	SourceRef string `json:"source_ref"`
	// Via is how this document reached the row that asked: "transaction" when
	// a person attached it, "receipt" when it was filed on the row as the
	// statement of the bill it paid or the invoice of the order it was matched
	// to. Empty when the question was not "what is behind this row".
	Via string `json:"via,omitempty"`
	// ReceiptOf says what a receipt is the paperwork of. Set on an attachment
	// too when the same file is also the row's receipt.
	ReceiptOf        *ReceiptOfResponse `json:"receipt_of,omitempty"`
	UploadedByUserID *uuid.UUID         `json:"uploaded_by_user_id"`
	CreatedAt        time.Time          `json:"created_at"`
}

// ReceiptOfResponse names a receipt's source: a bill (Name is the provider
// connection's label, DueOn its due date) or a merchant order (Name is the
// merchant, OrderNumber the order's).
type ReceiptOfResponse struct {
	Kind        string `json:"kind"`
	Name        string `json:"name"`
	DueOn       string `json:"due_on,omitempty"`
	OrderNumber string `json:"order_number,omitempty"`
}

// listDocuments answers what stands behind one transaction: what a person
// attached, and the statement of the bill it settled and the invoice of the
// order it matched, filed on it as receipts.
// transaction_id is required: no screen lists every file in a space.
func listDocuments(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	txnID, given, err := queryUUID(r, "transaction_id")
	if err != nil {
		return err
	}
	if !given {
		return errInvalid("missing", []string{"query", "transaction_id"},
			"transaction_id is required")
	}
	if _, err := attachmentTransaction(env, r, sp, txnID); err != nil {
		return err
	}
	behind, err := env.DB.DocumentsBehindTransaction(r.Context(), sp.ID(), txnID)
	if err != nil {
		return err
	}
	out := make([]DocumentResponse, 0, len(behind))
	for _, one := range behind {
		response := documentResponse(one.Document)
		response.Via = string(one.Via)
		if source := one.Receipt; source != nil {
			response.ReceiptOf = &ReceiptOfResponse{
				Kind: string(source.Of), Name: source.Name, OrderNumber: source.OrderNumber,
			}
			if !source.DueOn.IsZero() {
				response.ReceiptOf.DueOn = source.DueOn.String()
			}
		}
		out = append(out, response)
	}
	return writeJSON(w, http.StatusOK, out)
}

func readDocument(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveDocument(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, documentResponse(row))
}

// uploadDocument stores a file and links it. The link is required: an unlinked
// document is an orphan the purge job collects a week later.
func uploadDocument(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := parseUpload(w, r, "file", service.MaxDocumentBytes); err != nil {
		return err
	}
	defer removeMultipartTemp(r)
	file, err := requireUpload(r, "file", service.MaxDocumentBytes)
	if err != nil {
		return err
	}

	kind := store.DocumentLinkKind(strings.TrimSpace(r.FormValue("kind")))
	if !kind.Valid() {
		return errInvalid("invalid", []string{"body", "kind"},
			"kind must be one of bill, transaction, merchant_order, receipt")
	}
	targetID, err := uuid.Parse(strings.TrimSpace(r.FormValue("target_id")))
	if err != nil {
		return errInvalid("missing", []string{"body", "target_id"},
			"target_id is required and must be a uuid")
	}
	// The link's target is polymorphic and has no foreign key, so a kind this
	// resource cannot check is refused rather than written unverified.
	switch kind {
	case store.DocumentLinkTransaction:
		if _, err := attachmentTransaction(env, r, sp, targetID); err != nil {
			return err
		}
	case store.DocumentLinkBill:
		if _, err := env.DB.GetBill(r.Context(), sp.ID(), targetID); err != nil {
			return notFoundAs(err, "Bill")
		}
	default:
		return errConflict(
			"documents can only be linked to a transaction or a bill from the API; a %s "+
				"document is stored by the pull that fetched it", string(kind))
	}
	role := strings.TrimSpace(r.FormValue("role"))
	if role == "" {
		role = store.DocumentRoleAttachment
	}

	row, _, err := env.documents().Store(r.Context(), sp.ID(), service.DocumentUpload{
		Bytes:            file.Bytes,
		Filename:         file.Filename,
		Source:           store.DocumentSourceUpload,
		UploadedByUserID: sp.UserID(),
		Link:             store.DocumentLink{Kind: kind, TargetID: targetID, Role: role},
	})
	if err != nil {
		return documentStoreError(err, file.Filename)
	}
	return writeJSON(w, http.StatusCreated, documentResponse(row))
}

// downloadDocument streams the bytes. `nosniff` stops a browser second-guessing
// the stored type, the content security policy neuters anything rendered
// anyway, and the disposition is `inline` so a receipt can be viewed in place.
func downloadDocument(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveDocument(r, env, sp)
	if err != nil {
		return err
	}
	return writeDocumentContent(env, w, r, row)
}

func writeDocumentContent(
	env *Env, w http.ResponseWriter, r *http.Request, row store.Document,
) error {
	if env.Storage == nil {
		return errBadGateway("this deployment has no attachment storage configured")
	}
	data, err := env.Storage.Download(r.Context(), row.StorageKey)
	if err != nil {
		if errors.Is(err, provider.ErrFileNotFound) {
			return errNotFound("Attachment content")
		}
		return err
	}

	download, _, err := queryBool(r, "download")
	if err != nil {
		return err
	}
	disposition := "inline"
	if download {
		disposition = "attachment"
	}
	header := w.Header()
	header.Set("Content-Type", row.ContentType)
	header.Set("Content-Length", fmt.Sprint(len(data)))
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("Content-Security-Policy", "default-src 'none'; sandbox")
	header.Set("Content-Disposition",
		mime.FormatMediaType(disposition, map[string]string{"filename": row.Filename}))
	// Private: the response is one household's receipt and is only reachable
	// with this caller's token.
	header.Set("Cache-Control", "private, max-age=300")

	w.WriteHeader(http.StatusOK)
	_, err = w.Write(data)
	return err
}

// documentStoreError maps the service's refusals onto the statuses the
// attachment resource answers with.
func documentStoreError(err error, filename string) error {
	switch {
	case errors.Is(err, service.ErrNoStorage):
		return errBadGateway("this deployment has no attachment storage configured")
	case errors.Is(err, service.ErrDocumentTooLarge):
		return errInvalid("too_large", []string{"body", "file"},
			"an attachment may be at most %d MB", service.MaxDocumentBytes>>20)
	case errors.Is(err, service.ErrUnsupportedDocument):
		if strings.Contains(err.Error(), "the file is empty") {
			return errInvalid("invalid", []string{"body", "file"}, "the file is empty")
		}
		return errConflict(
			"%q is not a supported attachment; upload a JPEG, PNG, GIF, WebP, HEIC or PDF",
			filename)
	}
	return err
}

func liveDocument(r *http.Request, env *Env, sp auth.SpaceContext) (store.Document, error) {
	return fromPath(r, sp, "document_id", "Document", env.DB.GetDocument)
}

// NewDocuments builds the file service this environment's deployment implies,
// for the scheduler, which holds one for the life of the process.
func NewDocuments(env *Env) *service.Documents { return env.documents() }

// documents builds the file service per request rather than holding it on
// Env, so a changed storage backend is never cached stale.
func (e *Env) documents() *service.Documents {
	return service.NewDocuments(e.DB, e.Storage)
}

func documentResponse(d store.Document) DocumentResponse {
	return DocumentResponse{
		ID:               d.ID,
		Filename:         d.Filename,
		ContentType:      d.ContentType,
		SizeBytes:        d.SizeBytes,
		URL:              "/documents/" + d.ID.String() + "/content",
		Source:           d.Source,
		SourceRef:        d.SourceRef,
		UploadedByUserID: dbconv.NullUUID(d.UploadedByUserID),
		CreatedAt:        d.CreatedAt,
	}
}
