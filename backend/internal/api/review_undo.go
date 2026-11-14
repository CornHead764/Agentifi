package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// An undo token is the rows' state before a review write, held by the
// client and signed so it can only restore what this server wrote. A row is
// restored only while it is exactly as the write left it.

// reviewUndoFor is how long a token undoes; the toast that offers it is gone
// in seconds, so this only bounds a replayed token.
const reviewUndoFor = 24 * time.Hour

type reviewUndo struct {
	Space   string          `json:"space"`
	Expires time.Time       `json:"expires"`
	Rows    []reviewUndoRow `json:"rows"`
}

// reviewUndoRow compares what review writes, not updated_at, which a
// balance recompute moves on every sync.
type reviewUndoRow struct {
	ID     uuid.UUID   `json:"id"`
	Before reviewState `json:"before"`
	After  reviewState `json:"after"`
}

// reviewState is the part of a row a review write changes.
type reviewState struct {
	CategoryID *uuid.UUID   `json:"category_id"`
	Splits     []SplitWrite `json:"splits"`
	IsReviewed bool         `json:"is_reviewed"`
}

func reviewStateOf(txn store.Transaction) reviewState {
	out := reviewState{IsReviewed: txn.IsReviewed, Splits: []SplitWrite{}}
	if txn.CategoryID != uuid.Nil {
		id := txn.CategoryID
		out.CategoryID = &id
	}
	for _, split := range txn.Splits {
		cut := SplitWrite{Amount: split.Amount, Memo: &split.Memo, TagIDs: split.TagIDs}
		if split.CategoryID != uuid.Nil {
			id := split.CategoryID
			cut.CategoryID = &id
		}
		if cut.TagIDs == nil {
			cut.TagIDs = []uuid.UUID{}
		}
		out.Splits = append(out.Splits, cut)
	}
	return out
}

func (a reviewState) same(b reviewState) bool {
	left, errLeft := json.Marshal(a)
	right, errRight := json.Marshal(b)
	return errLeft == nil && errRight == nil && string(left) == string(right)
}

// restoreBody is the PATCH that puts a row back to this state.
func (a reviewState) restoreBody(current reviewState) map[string]any {
	body := map[string]any{"is_reviewed": a.IsReviewed}
	switch {
	case len(a.Splits) > 0:
		body["splits"] = a.Splits
	case len(current.Splits) > 0:
		body["splits"] = []SplitWrite{}
		body["category_id"] = a.CategoryID
	default:
		body["category_id"] = a.CategoryID
	}
	return body
}

func (e *Env) reviewUndoKey() []byte {
	sum := sha256.Sum256([]byte("agentifi:review-undo:v1:" + e.Cfg.SecretKey))
	return sum[:]
}

func (e *Env) signReviewUndo(sp auth.SpaceContext, rows []reviewUndoRow) (string, error) {
	if len(rows) == 0 {
		return "", nil
	}
	payload, err := json.Marshal(reviewUndo{Space: sp.ID().String(), Expires: e.now().Add(reviewUndoFor), Rows: rows})
	if err != nil {
		return "", err
	}
	mac := hmac.New(sha256.New, e.reviewUndoKey())
	mac.Write(payload)
	enc := base64.RawURLEncoding
	return enc.EncodeToString(payload) + "." + enc.EncodeToString(mac.Sum(nil)), nil
}

// openReviewUndo checks a token's signature, space and age.
func (e *Env) openReviewUndo(sp auth.SpaceContext, token string) (reviewUndo, error) {
	var out reviewUndo
	refused := errBadRequest("this undo is no longer available")
	payloadPart, sigPart, ok := strings.Cut(token, ".")
	if !ok {
		return out, refused
	}
	enc := base64.RawURLEncoding
	payload, err := enc.DecodeString(payloadPart)
	if err != nil {
		return out, refused
	}
	sig, err := enc.DecodeString(sigPart)
	if err != nil {
		return out, refused
	}
	mac := hmac.New(sha256.New, e.reviewUndoKey())
	mac.Write(payload)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return out, refused
	}
	if json.Unmarshal(payload, &out) != nil || out.Space != sp.ID().String() || !e.now().Before(out.Expires) {
		return out, refused
	}
	return out, nil
}
