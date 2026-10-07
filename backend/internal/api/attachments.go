package api

import (
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Attachments: the files hung off a transaction, a view over the document
// store. An attachment is a document with a `transaction` link and its id is
// the document's id; one is added through POST /documents with kind
// transaction.
//
// A transaction is the only owner this resource knows. A document with no
// transaction link answers 404 here, whatever else it is a document of.
//
// Delete releases the row's hold, and removes the document and its bytes at
// once when nothing else links it, because the person was told the file goes
// with the row.

func init() {
	Register(Resource{Prefix: "/attachments", Routes: func(rt *Routes) {
		rt.Write(http.MethodDelete, "/{attachment_id}", deleteAttachment)
	}})
}

// deleteAttachment releases the row's hold on the file, and removes the file
// when nothing is left holding it. An optional transaction_id names which row
// lets go of a file attached to several; without it every transaction link
// goes.
func deleteAttachment(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveAttachment(r, env, sp)
	if err != nil {
		return err
	}
	only, given, err := queryUUID(r, "transaction_id")
	if err != nil {
		return err
	}

	docs := env.documents()
	links, err := env.DB.ListDocumentLinks(r.Context(), sp.ID(), row.ID)
	if err != nil {
		return err
	}
	released := 0
	for _, link := range links {
		if link.Kind != store.DocumentLinkTransaction {
			continue
		}
		if given && link.TargetID != only {
			continue
		}
		if _, err := docs.Unlink(r.Context(), sp.ID(), row.ID, link.Kind, link.TargetID); err != nil {
			return notFoundAs(err, "Attachment")
		}
		released++
	}
	if released == 0 {
		// A transaction_id that does not hold this file is the same mistake as
		// an id that does not exist, and gets the same answer.
		return errNotFound("Attachment")
	}

	remaining, err := env.DB.ListDocumentLinks(r.Context(), sp.ID(), row.ID)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		// The person asking was told the file goes with the row, so it goes
		// now rather than after the purge job's week.
		if err := docs.Forget(r.Context(), sp.ID(), row.ID); err != nil && !isNotFound(err) {
			return err
		}
	}
	return writeNoContent(w)
}

// attachmentTransaction resolves the row an attachment hangs off, in this
// space, refusing a deleted one: a file added to a row in the trash would be
// unreachable at once.
func attachmentTransaction(
	env *Env, r *http.Request, sp auth.SpaceContext, txnID uuid.UUID,
) (store.Transaction, error) {
	txn, err := env.DB.GetTransaction(r.Context(), sp.ID(), txnID)
	if err != nil {
		return store.Transaction{}, notFoundAs(err, "Transaction")
	}
	if txn.IsDeleted {
		return store.Transaction{}, errNotFound("Transaction")
	}
	return txn, nil
}

// purgeAttachments releases a transaction's files when the transaction goes.
// Released, not deleted: the document may also belong to a bill or an order,
// and a file nothing holds is left to the purge job's grace period, so
// deleting the wrong row stays recoverable.
func purgeAttachments(env *Env, r *http.Request, sp auth.SpaceContext, txnID uuid.UUID) error {
	documents, err := env.DB.ListDocumentsByLink(
		r.Context(), sp.ID(), store.DocumentLinkTransaction, txnID)
	if err != nil {
		return err
	}
	docs := env.documents()
	for _, row := range documents {
		if _, err := docs.Unlink(
			r.Context(), sp.ID(), row.ID, store.DocumentLinkTransaction, txnID); err != nil {
			return err
		}
	}
	return nil
}

// liveAttachment resolves a document by id. A document with no transaction
// link answers 404, so a bill's statement does not leak through this resource.
func liveAttachment(r *http.Request, env *Env, sp auth.SpaceContext) (store.Document, error) {
	row, err := fromPath(r, sp, "attachment_id", "Attachment", env.DB.GetDocument)
	if err != nil {
		return store.Document{}, err
	}
	links, err := env.DB.ListDocumentLinks(r.Context(), sp.ID(), row.ID)
	if err != nil {
		return store.Document{}, err
	}
	for _, link := range links {
		if link.Kind == store.DocumentLinkTransaction {
			return row, nil
		}
	}
	return store.Document{}, errNotFound("Attachment")
}
