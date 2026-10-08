package api

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewAttachmentServiceHandler(attachmentService{env}, opts...)
	})
}

type attachmentService struct{ env *Env }

// DeleteAttachment releases the row's hold on the file, and removes the file
// when nothing is left holding it. An optional transaction_id names which row
// lets go of a file attached to several; without it every transaction link
// goes.
func (s attachmentService) DeleteAttachment(
	ctx context.Context, req *agentifiv1.DeleteAttachmentRequest,
) (*agentifiv1.DeleteAttachmentResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveAttachment(ctx, s.env, sp, req.GetAttachmentId())
	if err != nil {
		return nil, err
	}
	only, err := uuidField(strings.TrimSpace(req.GetTransactionId()), "query", "transaction_id")
	if err != nil {
		return nil, err
	}

	docs := s.env.documents()
	links, err := s.env.DB.ListDocumentLinks(ctx, sp.ID(), row.ID)
	if err != nil {
		return nil, err
	}
	released := 0
	for _, link := range links {
		if link.Kind != store.DocumentLinkTransaction {
			continue
		}
		if only != uuid.Nil && link.TargetID != only {
			continue
		}
		if _, err := docs.Unlink(ctx, sp.ID(), row.ID, link.Kind, link.TargetID); err != nil {
			return nil, notFoundAs(err, "Attachment")
		}
		released++
	}
	if released == 0 {
		// A transaction_id that does not hold this file is the same mistake as
		// an id that does not exist, and gets the same answer.
		return nil, errNotFound("Attachment")
	}

	remaining, err := s.env.DB.ListDocumentLinks(ctx, sp.ID(), row.ID)
	if err != nil {
		return nil, err
	}
	if len(remaining) == 0 {
		// The person asking was told the file goes with the row, so it goes
		// now rather than after the purge job's week.
		if err := docs.Forget(ctx, sp.ID(), row.ID); err != nil && !isNotFound(err) {
			return nil, err
		}
	}
	return &agentifiv1.DeleteAttachmentResponse{}, nil
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
func purgeAttachments(ctx context.Context, env *Env, sp auth.SpaceContext, txnID uuid.UUID) error {
	documents, err := env.DB.ListDocumentsByLink(ctx, sp.ID(), store.DocumentLinkTransaction, txnID)
	if err != nil {
		return err
	}
	docs := env.documents()
	for _, row := range documents {
		if _, err := docs.Unlink(ctx, sp.ID(), row.ID, store.DocumentLinkTransaction, txnID); err != nil {
			return err
		}
	}
	return nil
}

// liveAttachment resolves a document by id. A document with no transaction
// link answers 404, so a bill's statement does not leak through this resource.
func liveAttachment(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (store.Document, error) {
	id, err := idFrom(rawID, "Attachment")
	if err != nil {
		return store.Document{}, err
	}
	row, err := env.DB.GetDocument(ctx, sp.ID(), id)
	if err != nil {
		return store.Document{}, notFoundAs(err, "Attachment")
	}
	links, err := env.DB.ListDocumentLinks(ctx, sp.ID(), row.ID)
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
