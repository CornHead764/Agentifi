package api

import (
	"bytes"
	"context"
	"encoding/json"
	"mime"
	"net/http"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Bills: the household's logins at each provider, what each bills, the
// statements themselves, and the reminder each is attached to.
//
//   - No credential leaves here: store.BillConnection has no field for the
//     ciphertext, so nothing serialized from a listing can carry one.
//   - A biller is a value: the provider must be one of domain.Billers.
//
// What a bill does to a reminder is merged by the domain at read time and
// shown by /series and /occurrences.
//
// Two URLs stay plain HTTP: the failure screenshot, which is a JPEG, and
// POST /bills/connections/{connection_id}/bills, which also takes the
// statement file in a multipart body. CreateBill is the same write as a
// procedure, with the file sent through POST /documents.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewBillServiceHandler(billService{env}, opts...)
	})
	Register(Resource{Prefix: "/bills", Routes: func(rt *Routes) {
		rt.Write(http.MethodPost, "/connections/{connection_id}/bills", createBillOverHTTP)
		rt.Read(http.MethodGet, "/connections/{connection_id}/failure-screenshot", billFailureScreenshot)
	}})
}

type billService struct{ env *Env }

// BillCreate is a statement entered by hand, as the plain-HTTP route reads it.
type BillCreate struct {
	SubaccountID *uuid.UUID        `json:"subaccount_id"`
	DueOn        Date              `json:"due_on"`
	AmountDue    domain.Money      `json:"amount_due"`
	MinimumDue   Opt[domain.Money] `json:"minimum_due"`
	Currency     Opt[string]       `json:"currency"`
	AutopayOn    Opt[Date]         `json:"autopay_on"`
	IssuedOn     Opt[Date]         `json:"issued_on"`
	PeriodStart  Opt[Date]         `json:"period_start"`
	PeriodEnd    Opt[Date]         `json:"period_end"`
	StatementURL Opt[string]       `json:"statement_url"`
}

// --- Connections -------------------------------------------------------------

func (s billService) ListBillConnections(
	ctx context.Context, _ *agentifiv1.ListBillConnectionsRequest,
) (*agentifiv1.ListBillConnectionsResponse, error) {
	rows, err := s.env.DB.ListBillConnections(ctx, spaceFrom(ctx).ID())
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListBillConnectionsResponse{Connections: make([]*agentifiv1.BillConnection, 0, len(rows))}
	for _, row := range rows {
		out.Connections = append(out.Connections, billConnectionProto(row))
	}
	return out, nil
}

func (s billService) CreateBillConnection(
	ctx context.Context, req *agentifiv1.CreateBillConnectionRequest,
) (*agentifiv1.CreateBillConnectionResponse, error) {
	sp := spaceFrom(ctx)
	biller, known := domain.BillerByID(domain.BillerID(req.GetBiller()))
	if !known {
		return nil, errInvalid("unknown_biller", []string{"body", "biller"},
			"there is no bill provider called %q", req.GetBiller())
	}
	site, err := billSite(req.GetSite(), biller)
	if err != nil {
		return nil, err
	}

	connection := &store.BillConnection{
		Biller:           domain.BillerID(req.GetBiller()),
		Label:            strings.TrimSpace(req.GetLabel()),
		Username:         req.GetUsername(),
		Site:             site,
		CredentialSource: store.BillCredentialSession,
		AutopayRule:      domain.AutopayNone,
		PullEnabled:      true,
	}
	if err := applyAutopay(billOpt(req.AutopayRule), billOptInt(billOpt(req.AutopayDays)),
		billOptInt(billOpt(req.AutopayDay)), connection); err != nil {
		return nil, err
	}
	accountID, err := billOptUUID(billOpt(req.AutopayAccountId), "autopay_account_id")
	if err != nil {
		return nil, err
	}
	applyNullable(accountID, &connection.AutopayAccountID)
	if err := applyRequired("pull_enabled", billOpt(req.PullEnabled), &connection.PullEnabled); err != nil {
		return nil, err
	}
	applyNullable(billOpt(req.PullAt), &connection.PullAt)

	if biller.Generic {
		// Nothing is ever pulled here, and the switch says so rather than
		// being left on for a pull that the scheduler would skip anyway.
		connection.PullEnabled = false
	}
	if err := checkBillName(biller, connection.Label); err != nil {
		return nil, err
	}
	if err := checkBillLabelFree(ctx, s.env, sp, *connection, uuid.Nil); err != nil {
		return nil, err
	}
	if err := s.env.DB.CreateBillConnection(ctx, sp.ID(), connection); err != nil {
		return nil, err
	}
	// A provider with no sign-in bills by mail to the login itself, so its one
	// account is made here; a billed account never comes from a form.
	if biller.Access == domain.AccessEmail {
		named := connection.DisplayName()
		one := &store.BillSubaccount{
			ConnectionID: connection.ID, ExternalID: named, Label: named, IsSelected: true,
		}
		if err := s.env.DB.UpsertBillSubaccount(ctx, sp.ID(), one); err != nil {
			return nil, err
		}
	}
	return &agentifiv1.CreateBillConnectionResponse{Connection: billConnectionProto(*connection)}, nil
}

func (s billService) GetBillConnection(
	ctx context.Context, req *agentifiv1.GetBillConnectionRequest,
) (*agentifiv1.GetBillConnectionResponse, error) {
	connection, err := billConnectionOf(ctx, s.env, spaceFrom(ctx), req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetBillConnectionResponse{Connection: billConnectionProto(connection)}, nil
}

// billFailureScreenshot is the page the connection's last pull failed on.
func billFailureScreenshot(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := billConnectionOf(r.Context(), env, sp, chi.URLParam(r, "connection_id"))
	if err != nil {
		return err
	}
	shot, err := env.DB.BillPullScreenshot(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return notFoundAs(err, "Failure screenshot")
	}
	return writeFailureScreenshot(w, shot)
}

// GetBillPullTrail is the trail the connection's last unfinished sign-in
// kept, in the dialog's shape.
func (s billService) GetBillPullTrail(
	ctx context.Context, req *agentifiv1.GetBillPullTrailRequest,
) (*agentifiv1.GetBillPullTrailResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	trail, err := billsService(s.env).PullTrail(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, notFoundAs(err, "Sign-in trail")
	}
	return &agentifiv1.GetBillPullTrailResponse{Entries: billTrailEntries(trail)}, nil
}

func (s billService) UpdateBillConnection(
	ctx context.Context, req *agentifiv1.UpdateBillConnectionRequest,
) (*agentifiv1.UpdateBillConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("label", optOf(mask, "label", req.Label), &connection.Label); err != nil {
		return nil, err
	}
	connection.Label = strings.TrimSpace(connection.Label)
	if biller, known := domain.BillerByID(connection.Biller); known {
		if err := checkBillName(biller, connection.Label); err != nil {
			return nil, err
		}
	}
	applyNullable(optOf(mask, "username", req.Username), &connection.Username)
	if site := optOf(mask, "site", req.Site); site.Set {
		// A cleared site is allowed; an empty one is refused at the sign-in.
		biller, _ := domain.BillerByID(connection.Biller)
		kept, err := billSite(site.Value, biller)
		if err != nil {
			return nil, err
		}
		connection.Site = kept
	}
	if source := optOf(mask, "credential_source", req.CredentialSource); source.Present() {
		switch source.Value {
		case store.BillCredentialStored:
			// A password is kept by a sign-in that lands, never by a
			// settings save: there is nothing here to seal.
			if !connection.HasCredential {
				return nil, errInvalid("invalid", []string{"body", "credential_source"},
					"no password is kept for this connection; sign in and keep the password first")
			}
			connection.CredentialSource = source.Value
		case store.BillCredentialSession, store.BillCredentialTyped:
			connection.CredentialSource = source.Value
		default:
			return nil, errInvalid("invalid", []string{"body", "credential_source"},
				"credential_source must be session, stored or typed")
		}
	}
	if err := applyAutopay(optOf(mask, "autopay_rule", req.AutopayRule),
		billOptInt(optOf(mask, "autopay_days", req.AutopayDays)),
		billOptInt(optOf(mask, "autopay_day", req.AutopayDay)), &connection); err != nil {
		return nil, err
	}
	accountID, err := billOptUUID(optOf(mask, "autopay_account_id", req.AutopayAccountId), "autopay_account_id")
	if err != nil {
		return nil, err
	}
	applyNullable(accountID, &connection.AutopayAccountID)
	if err := applyRequired("pull_enabled", optOf(mask, "pull_enabled", req.PullEnabled),
		&connection.PullEnabled); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "pull_at", req.PullAt), &connection.PullAt)
	if err := checkBillLabelFree(ctx, s.env, sp, connection, connection.ID); err != nil {
		return nil, err
	}
	// Moving off the kept password forgets it: a sealed secret nothing reads
	// or shows should not exist.
	if connection.HasCredential && connection.CredentialSource != store.BillCredentialStored {
		if err := s.env.DB.ClearBillConnectionCredential(ctx, sp.ID(), connection.ID); err != nil {
			return nil, err
		}
		connection.HasCredential = false
	}
	if err := s.env.DB.UpdateBillConnection(ctx, sp.ID(), &connection); err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateBillConnectionResponse{Connection: billConnectionProto(connection)}, nil
}

// DeleteBillConnection cascades to the subaccounts, their bills and their
// links. Nothing on a series is touched; a bill never writes there.
func (s billService) DeleteBillConnection(
	ctx context.Context, req *agentifiv1.DeleteBillConnectionRequest,
) (*agentifiv1.DeleteBillConnectionResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteBillConnection(ctx, sp.ID(), connection.ID); err != nil {
		return nil, notFoundAs(err, "Bill connection")
	}
	return &agentifiv1.DeleteBillConnectionResponse{}, nil
}

// applyAutopay writes the rule and the one figure its kind uses, clearing the
// other so a changed rule never answers with a stale date.
func applyAutopay(
	rule Opt[string], days, day Opt[int], connection *store.BillConnection,
) error {
	if rule.Present() {
		switch domain.AutopayRuleKind(rule.Value) {
		case domain.AutopayNone, domain.AutopayDaysBeforeDue,
			domain.AutopayOnDueDate, domain.AutopayDayOfMonth:
			connection.AutopayRule = domain.AutopayRuleKind(rule.Value)
		default:
			return errInvalid("invalid", []string{"body", "autopay_rule"},
				"autopay_rule must be none, days_before_due, on_due_date or day_of_month")
		}
	}
	if days.Present() {
		connection.AutopayDays = days.Value
	}
	if day.Present() {
		if day.Value < 1 || day.Value > 31 {
			return errInvalid("invalid", []string{"body", "autopay_day"},
				"autopay_day must be a day of the month")
		}
		connection.AutopayDayOfMonth = day.Value
	}
	switch connection.AutopayRule {
	case domain.AutopayDaysBeforeDue:
		connection.AutopayDayOfMonth = 0
	case domain.AutopayDayOfMonth:
		connection.AutopayDays = 0
	case domain.AutopayNone, domain.AutopayOnDueDate:
		connection.AutopayDays, connection.AutopayDayOfMonth = 0, 0
	}
	return nil
}

// --- Subaccounts and bills ---------------------------------------------------

func (s billService) ListBillSubaccounts(
	ctx context.Context, req *agentifiv1.ListBillSubaccountsRequest,
) (*agentifiv1.ListBillSubaccountsResponse, error) {
	sp := spaceFrom(ctx)
	var connectionID uuid.UUID
	if raw := strings.TrimSpace(req.GetConnectionId()); raw != "" {
		parsed, err := uuid.Parse(raw)
		if err != nil {
			return nil, errInvalid("uuid_parsing", []string{"query", "connection_id"},
				"connection_id must be a uuid")
		}
		connectionID = parsed
	}
	rows, err := s.env.DB.ListBillSubaccounts(ctx, sp.ID(), connectionID)
	if err != nil {
		return nil, err
	}
	connections, err := s.env.DB.ListBillConnections(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	billers := make(map[uuid.UUID]domain.BillerID, len(connections))
	for _, one := range connections {
		billers[one.ID] = one.Biller
	}
	linked, err := billSeriesLinks(ctx, s.env, sp)
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.ListBillSubaccountsResponse{Subaccounts: make([]*agentifiv1.BillSubaccount, 0, len(rows))}
	for _, row := range rows {
		out.Subaccounts = append(out.Subaccounts,
			billSubaccountProto(row, billers[row.ConnectionID], linked[row.ID], row.AccountID))
	}
	return out, nil
}

func (s billService) CreateBillSubaccount(
	ctx context.Context, req *agentifiv1.CreateBillSubaccountRequest,
) (*agentifiv1.CreateBillSubaccountResponse, error) {
	sp := spaceFrom(ctx)
	connection, err := billConnectionOf(ctx, s.env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if req.GetLabel() == "" {
		return nil, errInvalid("missing", []string{"body", "label"}, "label is required")
	}
	subaccount := &store.BillSubaccount{
		ConnectionID: connection.ID, ExternalID: req.GetExternalId(),
		Label: req.GetLabel(), IsSelected: true,
	}
	if subaccount.ExternalID == "" {
		subaccount.ExternalID = req.GetLabel()
	}
	applyNullable(billOpt(req.MaskedNumber), &subaccount.MaskedNumber)
	if err := s.env.DB.UpsertBillSubaccount(ctx, sp.ID(), subaccount); err != nil {
		return nil, err
	}
	return &agentifiv1.CreateBillSubaccountResponse{
		Subaccount: billSubaccountProto(*subaccount, connection.Biller, uuid.Nil, uuid.Nil),
	}, nil
}

func (s billService) UpdateBillSubaccount(
	ctx context.Context, req *agentifiv1.UpdateBillSubaccountRequest,
) (*agentifiv1.UpdateBillSubaccountResponse, error) {
	sp := spaceFrom(ctx)
	subaccount, err := billSubaccountOf(ctx, s.env, sp, req.GetSubaccountId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	accountID, err := billOptUUID(optOf(mask, "account_id", req.AccountId), "account_id")
	if err != nil {
		return nil, err
	}
	if err := applyRequired("label", optOf(mask, "label", req.Label), &subaccount.Label); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "masked_number", req.MaskedNumber), &subaccount.MaskedNumber)
	if selected := optOf(mask, "is_selected", req.IsSelected); selected.Present() &&
		selected.Value != subaccount.IsSelected {
		if err := s.env.DB.SetBillSubaccountSelected(ctx, sp.ID(), subaccount.ID, selected.Value); err != nil {
			return nil, err
		}
		subaccount.IsSelected = selected.Value
	}
	if err := s.env.DB.UpsertBillSubaccount(ctx, sp.ID(), &subaccount); err != nil {
		return nil, err
	}
	if accountID.Set {
		if err := checkStatementAccount(ctx, s.env, sp, accountID.Value); err != nil {
			return nil, err
		}
		linked, err := service.NewBills(s.env.DB).LinkAccount(ctx, sp.ID(), subaccount.ID, accountID.Value)
		if err != nil {
			return nil, err
		}
		subaccount.AccountID = linked.AccountID
	}
	connection, err := s.env.DB.GetBillConnection(ctx, sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateBillSubaccountResponse{
		Subaccount: billSubaccountProto(subaccount, connection.Biller, uuid.Nil, subaccount.AccountID),
	}, nil
}

// checkStatementAccount refuses a link to an account that is not this
// household's, or not billed by statement. Nil is an unlink.
func checkStatementAccount(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	account, err := env.DB.GetAccount(ctx, sp.ID(), id)
	if err != nil {
		if isNotFound(err) {
			return errInvalid("not_found", []string{"body", "account_id"}, "there is no such account")
		}
		return err
	}
	if account.IsDeleted {
		return errInvalid("not_found", []string{"body", "account_id"}, "there is no such account")
	}
	if !domain.HasStatement(account.Kind) {
		return errInvalid("invalid", []string{"body", "account_id"},
			"only a credit card or a loan has a statement for a bill to fill")
	}
	return nil
}

func (s billService) ListSubaccountBills(
	ctx context.Context, req *agentifiv1.ListSubaccountBillsRequest,
) (*agentifiv1.ListSubaccountBillsResponse, error) {
	sp := spaceFrom(ctx)
	subaccount, err := billSubaccountOf(ctx, s.env, sp, req.GetSubaccountId())
	if err != nil {
		return nil, err
	}
	connection, err := s.env.DB.GetBillConnection(ctx, sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return nil, err
	}
	rows, err := s.env.DB.ListBills(ctx, sp.ID(), subaccount.ID)
	if err != nil {
		return nil, err
	}
	rule := connection.AutopayRuleValue()
	out := &agentifiv1.ListSubaccountBillsResponse{Bills: make([]*agentifiv1.Bill, 0, len(rows))}
	for _, row := range rows {
		out.Bills = append(out.Bills, billProto(row, rule))
	}
	return out, nil
}

// MarkBillPaid takes a person's word that a statement was paid where the
// ledger cannot see, which ends its pay-manually reminder.
func (s billService) MarkBillPaid(
	ctx context.Context, req *agentifiv1.MarkBillPaidRequest,
) (*agentifiv1.MarkBillPaidResponse, error) {
	sp := spaceFrom(ctx)
	id, err := idFrom(req.GetBillId(), "Bill")
	if err != nil {
		return nil, err
	}
	bill, err := s.env.DB.GetBill(ctx, sp.ID(), id)
	if err != nil {
		return nil, notFoundAs(err, "Bill")
	}
	if err := s.env.DB.MarkBillPaid(ctx, sp.ID(), bill.ID); err != nil {
		return nil, err
	}
	return &agentifiv1.MarkBillPaidResponse{}, nil
}

func (s billService) CreateBill(
	ctx context.Context, req *agentifiv1.CreateBillRequest,
) (*agentifiv1.CreateBillResponse, error) {
	return createBill(ctx, s.env, spaceFrom(ctx), req, uploadedStatement{})
}

// createBillOverHTTP is CreateBill's REST URL, which also takes the statement
// file in a multipart body.
func createBillOverHTTP(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	body, statement, err := billCreateBody(w, r)
	if err != nil {
		return err
	}
	defer removeMultipartTemp(r)
	out, err := createBill(r.Context(), env, sp, body.proto(chi.URLParam(r, "connection_id")), statement)
	if err != nil {
		return err
	}
	return writeRESTMessage(w, http.StatusCreated, out)
}

// createBill records a statement somebody read themselves, through
// service.Ingest so the same cycle twice is one bill and a newer cycle
// supersedes the old.
func createBill(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	req *agentifiv1.CreateBillRequest, statement uploadedStatement,
) (*agentifiv1.CreateBillResponse, error) {
	connection, err := billConnectionOf(ctx, env, sp, req.GetConnectionId())
	if err != nil {
		return nil, err
	}
	if req.GetDueOn() == "" {
		return nil, errInvalid("missing", []string{"body", "due_on"}, "due_on is required")
	}
	dueOn, err := billDateFrom(req.GetDueOn(), "due_on")
	if err != nil {
		return nil, err
	}
	amountDue := domain.Zero
	if req.AmountDue != nil {
		if amountDue, err = moneyFrom(req.AmountDue, "body", "amount_due"); err != nil {
			return nil, err
		}
	}
	if amountDue.IsNegative() {
		return nil, errInvalid("invalid", []string{"body", "amount_due"},
			"amount_due is what is owed, which is a magnitude")
	}
	var named *uuid.UUID
	if req.SubaccountId != nil {
		id, err := uuid.Parse(req.GetSubaccountId())
		if err != nil {
			return nil, errInvalid("uuid_parsing", []string{"body", "subaccount_id"},
				"subaccount_id must be a uuid")
		}
		named = &id
	}

	subaccount, err := billSubaccountFor(ctx, env, sp, connection, named)
	if err != nil {
		return nil, err
	}

	bill := store.Bill{
		DueOn: dueOn, AmountDue: amountDue,
		Currency: sp.Space.PrimaryCurrency, Status: domain.BillOpen,
		Source: store.BillSourceManual, FetchedAt: env.now().UTC(),
	}
	if req.Currency != nil {
		bill.Currency = req.GetCurrency()
	}
	if req.MinimumDue != nil {
		minimum, err := moneyFrom(req.MinimumDue, "body", "minimum_due")
		if err != nil {
			return nil, err
		}
		if minimum.IsNegative() {
			return nil, errInvalid("invalid", []string{"body", "minimum_due"},
				"minimum_due is what is owed, which is a magnitude")
		}
		bill.MinimumDue, bill.HasMinimumDue = minimum, true
	}
	for _, date := range []struct {
		raw   *string
		field string
		into  *domain.Date
	}{
		{req.AutopayOn, "autopay_on", &bill.AutopayOn},
		{req.IssuedOn, "issued_on", &bill.IssuedOn},
		{req.PeriodStart, "period_start", &bill.PeriodStart},
		{req.PeriodEnd, "period_end", &bill.PeriodEnd},
	} {
		if date.raw == nil {
			continue
		}
		if *date.into, err = billDateFrom(*date.raw, date.field); err != nil {
			return nil, err
		}
	}
	bill.StatementURL = req.GetStatementUrl()

	documents := env.documents()
	upload := service.DocumentUpload{
		Bytes:            statement.Bytes,
		Filename:         statement.Filename,
		Source:           store.DocumentSourceUpload,
		UploadedByUserID: sp.UserID(),
		Link:             store.DocumentLink{Kind: store.DocumentLinkBill, Role: store.DocumentRoleStatement},
	}
	// Checked before the bill is written, so a refused file fails the whole
	// request rather than leaving a bill behind.
	if statement.given {
		if err := documents.Check(upload); err != nil {
			return nil, documentStoreError(err, statement.Filename)
		}
	}

	result, err := service.NewBills(env.DB).Ingest(ctx, sp.ID(), subaccount.ID, []store.Bill{bill})
	if err != nil {
		return nil, err
	}
	if stored, held := service.BillOfCycle(result.Bills, bill.DueOn, bill.Invoice); statement.given && held {
		document, err := documents.AttachStatement(ctx, sp.ID(), stored.ID, upload)
		if err != nil {
			return nil, documentStoreError(err, statement.Filename)
		}
		for i := range result.Bills {
			if result.Bills[i].ID == stored.ID {
				result.Bills[i].DocumentID = document.ID
			}
		}
	}
	out := &agentifiv1.CreateBillResponse{
		New: int32(result.New), Amended: int32(result.Amended),
		Unchanged: int32(result.Unchanged), Superseded: int32(result.Superseded),
		Bills: make([]*agentifiv1.Bill, 0, len(result.Bills)),
	}
	rule := connection.AutopayRuleValue()
	for _, one := range result.Bills {
		out.Bills = append(out.Bills, billProto(one, rule))
	}
	return out, nil
}

// uploadedStatement is the file a hand-entered bill may arrive with.
type uploadedStatement struct {
	given bool
	uploadedFile
}

// billCreateBody reads a statement's own fields, and the statement file when
// one came with them. JSON or multipart: form values are re-encoded as the JSON
// object the same decoder reads, which works because every BillCreate field is
// a JSON string. A `bill` part carrying the whole object is accepted too.
func billCreateBody(w http.ResponseWriter, r *http.Request) (BillCreate, uploadedStatement, error) {
	var body BillCreate
	kind, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || kind != "multipart/form-data" {
		return body, uploadedStatement{}, decodeBody(r, &body)
	}
	if err := parseUpload(w, r, "document", service.MaxDocumentBytes); err != nil {
		return body, uploadedStatement{}, err
	}

	raw := []byte(strings.TrimSpace(r.FormValue("bill")))
	if len(raw) == 0 {
		fields := map[string]string{}
		for name, values := range r.MultipartForm.Value {
			if name == "bill" || len(values) == 0 {
				continue
			}
			fields[name] = values[0]
		}
		if raw, err = json.Marshal(fields); err != nil {
			return body, uploadedStatement{}, err
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&body); err != nil {
		return body, uploadedStatement{}, decodeError(err, raw)
	}

	statement, found, err := readUpload(r, "document", service.MaxDocumentBytes)
	if err != nil {
		return body, uploadedStatement{}, err
	}
	return body, uploadedStatement{given: found, uploadedFile: statement}, nil
}

// proto is the body as CreateBill's request. A null reads as absent, as it
// did when the route read the body itself.
func (b BillCreate) proto(connectionID string) *agentifiv1.CreateBillRequest {
	req := &agentifiv1.CreateBillRequest{
		ConnectionId: connectionID,
		AmountDue:    moneyProto(b.AmountDue),
	}
	if !domain.Date(b.DueOn).IsZero() {
		req.DueOn = domain.Date(b.DueOn).String()
	}
	if b.SubaccountID != nil {
		req.SubaccountId = proto.String(b.SubaccountID.String())
	}
	if b.MinimumDue.Present() {
		req.MinimumDue = &agentifiv1.NullableMoney{Amount: b.MinimumDue.Value.String()}
	}
	if b.Currency.Present() {
		req.Currency = proto.String(b.Currency.Value)
	}
	for _, date := range []struct {
		from Opt[Date]
		into **string
	}{
		{b.AutopayOn, &req.AutopayOn}, {b.IssuedOn, &req.IssuedOn},
		{b.PeriodStart, &req.PeriodStart}, {b.PeriodEnd, &req.PeriodEnd},
	} {
		if date.from.Present() {
			*date.into = proto.String(domain.Date(date.from.Value).String())
		}
	}
	if b.StatementURL.Present() {
		req.StatementUrl = proto.String(b.StatementURL.Value)
	}
	return req
}

// --- Links -------------------------------------------------------------------

func (s billService) CreateBillLink(
	ctx context.Context, req *agentifiv1.CreateBillLinkRequest,
) (*agentifiv1.CreateBillLinkResponse, error) {
	sp := spaceFrom(ctx)
	seriesID, err := billBodyUUID(req.GetSeriesId(), "series_id")
	if err != nil {
		return nil, err
	}
	subaccountID, err := billBodyUUID(req.GetSubaccountId(), "subaccount_id")
	if err != nil {
		return nil, err
	}
	if _, err := service.NewSeriesMatcher(s.env.DB).GetSeries(ctx, sp.ID(), seriesID); err != nil {
		return nil, notFoundAs(err, "Series")
	}
	link, err := service.NewBills(s.env.DB).Link(ctx, sp.ID(), seriesID, subaccountID)
	if err != nil {
		return nil, notFoundAs(err, "Bill subaccount")
	}
	return &agentifiv1.CreateBillLinkResponse{Link: &agentifiv1.BillLink{
		SeriesId: link.SeriesID.String(), SubaccountId: link.SubaccountID.String(),
		CreatedAt: timestamppb.New(link.CreatedAt),
	}}, nil
}

func (s billService) DeleteBillLink(
	ctx context.Context, req *agentifiv1.DeleteBillLinkRequest,
) (*agentifiv1.DeleteBillLinkResponse, error) {
	seriesID, err := idFrom(req.GetSeriesId(), "Bill link")
	if err != nil {
		return nil, err
	}
	if err := service.NewBills(s.env.DB).Unlink(ctx, spaceFrom(ctx).ID(), seriesID); err != nil {
		return nil, notFoundAs(err, "Bill link")
	}
	return &agentifiv1.DeleteBillLinkResponse{}, nil
}

// --- Loading and rendering ---------------------------------------------------

// billConnectionOf loads the connection an id names in this space. A
// malformed id and a connection in another space are the same 404.
func billConnectionOf(ctx context.Context, env *Env, sp auth.SpaceContext, raw string) (store.BillConnection, error) {
	id, err := idFrom(raw, "Bill connection")
	if err != nil {
		return store.BillConnection{}, err
	}
	row, err := env.DB.GetBillConnection(ctx, sp.ID(), id)
	if err != nil {
		return store.BillConnection{}, notFoundAs(err, "Bill connection")
	}
	return row, nil
}

func billSubaccountOf(ctx context.Context, env *Env, sp auth.SpaceContext, raw string) (store.BillSubaccount, error) {
	id, err := idFrom(raw, "Bill subaccount")
	if err != nil {
		return store.BillSubaccount{}, err
	}
	row, err := env.DB.GetBillSubaccount(ctx, sp.ID(), id)
	if err != nil {
		return store.BillSubaccount{}, notFoundAs(err, "Bill subaccount")
	}
	return row, nil
}

// billSubaccountFor resolves which of a connection's subaccounts a body names.
// A connection that bills exactly one thing needs no id.
func billSubaccountFor(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	connection store.BillConnection, named *uuid.UUID,
) (store.BillSubaccount, error) {
	rows, err := env.DB.ListBillSubaccounts(ctx, sp.ID(), connection.ID)
	if err != nil {
		return store.BillSubaccount{}, err
	}
	if named != nil {
		for _, row := range rows {
			if row.ID == *named {
				return row, nil
			}
		}
		return store.BillSubaccount{}, errNotFound("Bill subaccount")
	}
	switch len(rows) {
	case 0:
		return store.BillSubaccount{}, errConflict(
			"%s has nothing to bill yet; add a subaccount first", connection.DisplayName())
	case 1:
		return rows[0], nil
	default:
		return store.BillSubaccount{}, errInvalid("missing", []string{"body", "subaccount_id"},
			"%s bills several accounts, so the bill has to name one", connection.DisplayName())
	}
}

// billSeriesLinks is the reminder each linked subaccount keeps current.
func billSeriesLinks(ctx context.Context, env *Env, sp auth.SpaceContext) (map[uuid.UUID]uuid.UUID, error) {
	links, err := env.DB.ListSeriesBillLinks(ctx, sp.ID(), nil)
	if err != nil {
		return nil, err
	}
	linked := make(map[uuid.UUID]uuid.UUID, len(links))
	for _, link := range links {
		linked[link.SubaccountID] = link.SeriesID
	}
	return linked, nil
}

// checkBillName refuses an email-only connection with no name: the name is
// the company.
func checkBillName(biller domain.Biller, label string) error {
	if biller.Generic && strings.TrimSpace(label) == "" {
		return errInvalid("missing", []string{"body", "label"},
			"name the company: an email-only provider is called what you call it")
	}
	return nil
}

// checkBillLabelFree keeps the label unique per provider, as the schema does,
// so the refusal reads as a sentence rather than a constraint name.
func checkBillLabelFree(
	ctx context.Context, env *Env, sp auth.SpaceContext, one store.BillConnection, self uuid.UUID,
) error {
	rows, err := env.DB.ListBillConnections(ctx, sp.ID())
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Biller == one.Biller && row.Label == one.Label && row.ID != self {
			if strings.TrimSpace(one.Label) == "" {
				return errConflict("there is already a %s connection with no name; "+
					"give this one a name to tell the two apart", one.ProviderName())
			}
			return errConflict("a %s connection named %q already exists", one.ProviderName(), one.Label)
		}
	}
	return nil
}

func billConnectionProto(one store.BillConnection) *agentifiv1.BillConnection {
	out := &agentifiv1.BillConnection{
		Id: one.ID.String(), Biller: string(one.Biller), Label: one.Label, Username: one.Username,
		Site:             one.Site,
		CredentialSource: one.CredentialSource, HasTotp: one.HasTOTP, Connected: one.HasSession,
		SecondFactor: string(one.SecondFactor),
		SignedInAt:   billNullableTimestamp(one.SignedInAt), NeedsSignIn: one.NeedsSignIn,
		SignInPaused:     one.SignInPausedFor,
		AutopayRule:      string(one.AutopayRule),
		AutopayAccountId: billNullableID(one.AutopayAccountID),
		PullEnabled:      one.PullEnabled, PullAt: dbconv.NullText(one.PullAt),
		LastPulledAt: billNullableTimestamp(one.LastPulledAt), LastPullStatus: one.LastPullStatus,
		LastPullError: one.LastPullError, HasFailureScreenshot: one.HasFailureScreenshot,
		HasTrail:  one.HasTrail,
		Pulling:   service.BillPullRunning(one.ID),
		CreatedAt: timestamppb.New(one.CreatedAt),
	}
	if one.AutopayDays != 0 {
		out.AutopayDays = proto.Int32(int32(one.AutopayDays))
	}
	if one.AutopayDayOfMonth != 0 {
		out.AutopayDay = proto.Int32(int32(one.AutopayDayOfMonth))
	}
	return out
}

// billSubaccountProto renders one billed account. series is the reminder it
// keeps current and account the card or loan it is, either uuid.Nil.
func billSubaccountProto(
	one store.BillSubaccount, biller domain.BillerID, series, account uuid.UUID,
) *agentifiv1.BillSubaccount {
	return &agentifiv1.BillSubaccount{
		Id: one.ID.String(), ConnectionId: one.ConnectionID.String(), Biller: string(biller),
		ExternalId: one.ExternalID, Label: one.Label,
		MaskedNumber: dbconv.NullText(one.MaskedNumber), IsSelected: one.IsSelected,
		SeriesId: billNullableID(series), AccountId: billNullableID(account),
	}
}

// billProto renders one statement against the rule of the connection it came
// from, which is what resolves pays_on.
func billProto(one store.Bill, rule domain.AutopayRule) *agentifiv1.Bill {
	return &agentifiv1.Bill{
		Id: one.ID.String(), SubaccountId: one.SubaccountID.String(), DueOn: one.DueOn.String(),
		AmountDue:  moneyProto(one.AmountDue),
		MinimumDue: nullableMoneyProto(one.MinimumDue, one.HasMinimumDue),
		Currency:   one.Currency,
		IssuedOn:   billNullableDate(one.IssuedOn), PeriodStart: billNullableDate(one.PeriodStart),
		PeriodEnd: billNullableDate(one.PeriodEnd), AutopayOn: billNullableDate(one.AutopayOn),
		PaysOn: billNullableDate(service.BillPaysOn(one, rule)),
		Status: string(one.Status), Source: one.Source, StatementUrl: one.StatementURL,
		DocumentId: billNullableID(one.DocumentID), FetchedAt: timestamppb.New(one.FetchedAt),
		AmendedAt: billNullableTimestamp(one.AmendedAt),
	}
}

// --- Wire conversions --------------------------------------------------------

// billOpt is a create request's optional field as handler logic reads it:
// absent or a value, never cleared.
func billOpt[T any](value *T) Opt[T] {
	if value == nil {
		return Opt[T]{}
	}
	return Opt[T]{Set: true, Value: *value}
}

func billOptInt(o Opt[int32]) Opt[int] {
	return Opt[int]{Set: o.Set, Null: o.Null, Value: int(o.Value)}
}

// billOptUUID parses an id field of a request body; a cleared one is uuid.Nil.
func billOptUUID(o Opt[string], field string) (Opt[uuid.UUID], error) {
	out := Opt[uuid.UUID]{Set: o.Set, Null: o.Null}
	if !o.Present() {
		return out, nil
	}
	id, err := uuid.Parse(o.Value)
	if err != nil {
		return out, errInvalid("uuid_parsing", []string{"body", field}, "%s must be a uuid", field)
	}
	out.Value = id
	return out, nil
}

// billBodyUUID parses an id a body must carry. Left out it is uuid.Nil,
// which names no row.
func billBodyUUID(raw, field string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errInvalid("uuid_parsing", []string{"body", field}, "%s must be a uuid", field)
	}
	return id, nil
}

func billDateFrom(raw, field string) (domain.Date, error) {
	date, err := parseDate(raw)
	if err != nil {
		return domain.Date{}, errInvalid("date_parsing", []string{"body", field}, "%s", err)
	}
	return date, nil
}

// billNullableDate is unset for the zero date.
func billNullableDate(d domain.Date) *string {
	if d.IsZero() {
		return nil
	}
	return proto.String(d.String())
}

// billNullableID is unset for uuid.Nil.
func billNullableID(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	return proto.String(id.String())
}

func billNullableTimestamp(t *time.Time) *timestamppb.Timestamp {
	if t == nil {
		return nil
	}
	return timestamppb.New(*t)
}

// writeRESTMessage answers a plain-HTTP route with a procedure's response, in
// the shape the REST bridge gives a bridged route's.
func writeRESTMessage(w http.ResponseWriter, status int, message proto.Message) error {
	data, err := (jsonCodec{}).Marshal(message)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	return writeJSON(w, status, restMessage(message.ProtoReflect().Descriptor(), raw))
}
