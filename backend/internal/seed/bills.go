package seed

import (
	"context"
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
	"time"
)

func mustDate(s string) time.Time {
	t, err := time.Parse(time.DateOnly, s)
	if err != nil {
		panic(err)
	}
	return t
}
func seedBills(ctx context.Context, h *household) error {
	st, sid := h.st, h.space.ID
	c := store.BillConnection{Biller: "generic", Label: "Seed water and power", Username: "seed@example.invalid", CredentialSource: store.BillCredentialStored, PullEnabled: false}
	if err := st.CreateBillConnection(ctx, sid, &c); err != nil {
		return err
	}
	if err := st.SaveBillConnectionCredential(ctx, sid, c.ID, store.BillCredential{Username: c.Username, Password: "invented-not-a-secret"}); err != nil {
		return err
	}
	if err := st.SaveBillConnectionSession(ctx, sid, c.ID, `{"cookies":[],"origins":[]}`, "seed-profile", true); err != nil {
		return err
	}
	sub := store.BillSubaccount{ConnectionID: c.ID, ExternalID: "water-001", Label: "Home water", MaskedNumber: "••1234", IsSelected: true, AccountID: h.accounts[0].ID}
	if err := st.UpsertBillSubaccount(ctx, sid, &sub); err != nil {
		return err
	}
	recur, err := domain.NewRecurrence(domain.AliasEveryMonth, domain.FreqMonthly, 1, []int{15}, nil, nil)
	if err != nil {
		return err
	}
	series := store.SeriesWrite{AccountID: h.accounts[0].ID, CategoryID: h.categories[0].ID, Kind: "bill", DisplayName: "Water bill", Description: "SEED CITY WATER", Amount: money("-72.35"), Currency: h.space.PrimaryCurrency, Recurrence: recur, StartOn: h.date(-23, 0), IsActive: true, TemplateTagIDs: []uuid.UUID{h.tag.ID},
		TemplateSplits: []byte(`[{"amount":"-50.00","category_id":"` + h.categories[0].ID.String() + `","memo":"Water","tag_ids":["` + h.tag.ID.String() + `"]},{"amount":"-22.35","category_id":"` + h.categories[3].ID.String() + `","memo":"Sewer","tag_ids":[]}]`)}
	if err := st.CreateSeries(ctx, sid, &series); err != nil {
		return err
	}
	if _, err := st.LinkSeriesBill(ctx, sid, series.ID, sub.ID); err != nil {
		return err
	}
	docs := service.NewDocuments(st, h.storage)
	docs.Now = func() time.Time { return h.today }
	for m := -2; m <= 0; m++ {
		due := h.date(m, -15)
		b := store.Bill{SubaccountID: sub.ID, DueOn: due, Invoice: fmt.Sprintf("WATER-%s", due.String()), AmountDue: money("72.35"), Currency: h.space.PrimaryCurrency, Source: "seed", ExternalID: due.String(), Status: domain.BillOpen, FetchedAt: h.today}
		if err := st.UpsertBill(ctx, sid, &b, false); err != nil {
			return err
		}
		// Edge catalogue: non-ASCII statement filenames, missing issue/period dates,
		// a statement document with an application-managed durable storage key.
		if _, _, err := docs.Store(ctx, sid, service.DocumentUpload{Bytes: []byte("%PDF-1.4\n1 0 obj<</Type/Catalog>>endobj\n%%EOF\n"), Filename: "Relevé Straße İ 💧.pdf", Source: "bill", SourceRef: b.Invoice, Link: store.DocumentLink{Kind: "bill", TargetID: b.ID, Role: "statement"}}); err != nil {
			return err
		}
		if m < 0 {
			t, err := st.GetTransaction(ctx, sid, h.txns[20-m].ID)
			if err != nil {
				return err
			}
			t.SeriesID = series.ID
			t.SeriesDueOn = due
			t.Date = due
			t.Amount = money("-72.35")
			t.IsPending = false
			if err := st.UpdateTransaction(ctx, sid, &t); err != nil {
				return err
			}
		}
	}
	ch := store.BillChallenge{ConnectionID: c.ID, AgentSession: "seed-session", Method: "otp", Prompt: "Invented verification code", State: "pending", RaisedBy: "seed", ExpiresAt: h.today.Add(time.Hour)}
	if err := st.CreateBillChallenge(ctx, sid, &ch); err != nil {
		return err
	}
	payments := []store.BillPayment{
		{ExternalID: "pay-seed-001", PaidOn: h.date(-1, -15), Amount: money("72.35"), Method: "Visa ending 0000", FetchedAt: h.today},
		{ExternalID: "pay-seed-002", PaidOn: h.date(-2, -15), Amount: money("72.35"), Method: "Bank transfer", FetchedAt: h.today},
	}
	if _, err := st.UpsertBillPayments(ctx, sid, sub.ID, payments); err != nil {
		return err
	}
	if err := st.MarkBillPull(ctx, sid, c.ID, store.BillPullFailed, "Seed deliberate provider failure", []byte{137, 80, 78, 71, 13, 10, 26, 10}, []byte(`[{"at":"2026-06-29T09:15:00Z","step":"read","state":"note","url":"https://billing.example.test/statements","title":"Statements"}]`)); err != nil {
		return err
	}
	mail := store.EmailConnection{Label: "Seed mailbox", Kind: store.EmailKindIMAP, Address: "bills@example.invalid", Host: "imap.example.invalid", Port: 993, Username: "seed", Folder: "INBOX", Enabled: false}
	if err := st.CreateEmailConnection(ctx, sid, &mail); err != nil {
		return err
	}
	rule := store.MailRule{Name: "Seed water statements", Enabled: true, Sender: "water@example.invalid", SubjectContains: "Statement", Action: "bill", BillConnectionID: c.ID, BillSubaccountID: sub.ID, AccountID: h.accounts[0].ID, Direction: "expense", AmountLabel: "Amount due"}
	if err := st.CreateMailRule(ctx, sid, &rule); err != nil {
		return err
	}
	_, err = st.RecordBillEmail(ctx, sid, &store.BillEmail{ConnectionID: mail.ID, MessageID: "<seed-water@example.invalid>", ReceivedAt: h.today, Sender: "water@example.invalid", Subject: "Relevé 💧", Outcome: store.EmailOutcomeRule, RuleID: rule.ID, TransactionID: h.txns[22].ID})
	return err
}
func seedPlan(ctx context.Context, h *household) error {
	p := service.NewPlan(h.st)
	p.Now = func() time.Time { return h.today }
	for m := -2; m <= 0; m++ {
		row, err := p.OpenMonth(ctx, h.space.ID, domain.MonthOf(h.date(m, 0)))
		if err != nil {
			return err
		}
		if err := p.InsertEnvelope(ctx, h.space.ID, service.EnvelopeRow{ID: uuid.New(), MonthID: row.ID, FilterID: h.filter.ID, Name: "Café envelope", TargetAmount: money("250"), Recurring: true}); err != nil {
			return err
		}
	}
	view, err := p.Compute(ctx, h.space.ID, domain.MonthOf(h.date(0, 0)))
	if err != nil {
		return err
	}
	return p.SaveResults(ctx, h.space.ID, view)
}
