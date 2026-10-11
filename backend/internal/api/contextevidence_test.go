package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestContextEvidenceSaysWhatTheModelWasShown(t *testing.T) {
	out := &strings.Builder{}
	out.WriteString("## Context\n")
	section(out, "The transaction", map[string]any{"payee": "Example Shop"})
	section(out, "The Amazon order "+headingBehindCharge+" (decide from the items, not the payee)",
		map[string]any{"items": []map[string]any{
			{"title": "Wooden blocks", "quantity": 1},
			{"title": "Sock puppet", "quantity": 2},
		}})
	section(out, headingSimilar+" (12 most relevant, newest first)", "[]")
	section(out, headingCorrections+" (what was proposed)", []string{"one"})
	section(out, "Categories (reference a category by its id)", "Toys: x")
	section(out, headingGuidance, "Toys are gifts.")

	require.Equal(t, []string{
		"The Amazon order, 2 items",
		"12 similar past rows",
		"Your corrections to earlier proposals",
		"Your guidance",
	}, ContextEvidence(out.String()))
}

func TestContextEvidenceNamesAPayeeSeenNowhereElseAndAnAccountStandingIn(t *testing.T) {
	out := &strings.Builder{}
	section(out, headingSimilar, noPastRows)
	require.Equal(t, []string{"No past rows from this payee"}, ContextEvidence(out.String()))

	out.Reset()
	section(out, headingAccountRows+" (1 most recent) — the wording is generic", "[]")
	require.Equal(t, []string{"1 recent row from this account"}, ContextEvidence(out.String()))
}

func TestARunsDetailSaysWhatItWasShown(t *testing.T) {
	l := buildLedger(t)
	model := newFakeModel(t, answerReply("Could not guess: nothing to go on."))
	allowWrites(l, model)
	id := l.alex.post("/assistant-automations", suggestCategoriesBody(nil)).
		requireStatus(http.StatusCreated).json()["id"].(string)
	run := l.alex.post("/assistant-automations/"+id+"/run",
		map[string]any{"transaction_id": l.str("august_corner")}).
		requireStatus(http.StatusOK).json()

	detail := l.alex.get("/assistant-automations/runs/" + run["id"].(string)).
		requireStatus(http.StatusOK).json()
	evidence := detail["evidence"].([]any)
	require.NotEmpty(t, evidence)
	require.Regexp(t, `similar past row|No past rows|from this account`, evidence[0])
}
