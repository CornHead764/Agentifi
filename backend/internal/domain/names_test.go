package domain

import "testing"

func names(rows ...string) []NameCandidate {
	out := make([]NameCandidate, 0, len(rows))
	for i, row := range rows {
		out = append(out, NameCandidate{ID: string(rune('a' + i)), Name: row})
	}
	return out
}

func TestANameResolvesToTheOneRowItMeans(t *testing.T) {
	accounts := names("Harbor Cash Back", "Everyday Checking", "Rewards Visa")
	got := ResolveName("my harbor card", accounts)
	if got.Match == nil || got.Match.Name != "Harbor Cash Back" {
		t.Fatalf("want Harbor Cash Back, got %+v", got)
	}
}

func TestAnAmbiguousNameComesBackAsTheChoices(t *testing.T) {
	// Two Harbor cards: picking the first would be the guess this exists to stop.
	accounts := names("Harbor Cash Back", "Harbor Travel", "Everyday Checking")
	got := ResolveName("harbor card", accounts)
	if got.Match != nil {
		t.Fatalf("resolved %q when two rows fit", got.Match.Name)
	}
	if !got.Ambiguous() || len(got.Choices) != 2 {
		t.Fatalf("want the two Harbor cards as choices, got %+v", got.Choices)
	}
}

func TestAnExactNameWinsOverTheRowsItBegins(t *testing.T) {
	categories := names("Groceries", "Groceries & Household")
	got := ResolveName("groceries", categories)
	if got.Match == nil || got.Match.Name != "Groceries" {
		t.Fatalf("want the exact Groceries, got %+v", got)
	}
}

func TestAWordBeginningANameIsEnough(t *testing.T) {
	got := ResolveName("grocer", names("Groceries", "Gas", "Dining Out"))
	if got.Match == nil || got.Match.Name != "Groceries" {
		t.Fatalf("want Groceries, got %+v", got)
	}
}

func TestAnIdResolvesToItsRow(t *testing.T) {
	rows := []NameCandidate{{ID: "6f1c0000-0000-0000-0000-000000000001", Name: "Groceries"}}
	got := ResolveName("6F1C0000-0000-0000-0000-000000000001", rows)
	if got.Match == nil {
		t.Fatalf("an id did not resolve")
	}
}

func TestAnAliasIsMatchedLikeTheName(t *testing.T) {
	rows := []NameCandidate{
		{ID: "a", Name: "Sample Rewards Card", Aliases: []string{"0000"}},
		{ID: "b", Name: "Everyday Checking", Aliases: []string{"0077"}},
	}
	got := ResolveName("card ending 0000", rows)
	if got.Match != nil {
		// "ending" is a word no row has, so this is not a match — the alias
		// has to be asked for on its own.
		t.Fatalf("resolved on a word no row carries: %+v", got.Match)
	}
	got = ResolveName("0000", rows)
	if got.Match == nil || got.Match.ID != "a" {
		t.Fatalf("want the card ending 0000, got %+v", got)
	}
}

func TestANameNothingMatchesResolvesToNothing(t *testing.T) {
	got := ResolveName("Brokerage", names("Everyday Checking", "Rewards Visa"))
	if got.Match != nil || len(got.Choices) != 0 {
		t.Fatalf("want nothing, got %+v", got)
	}
}

func TestANearMissIsNotAMatch(t *testing.T) {
	// No edit distance: "Grocery" does not begin "Groceries", and quietly
	// resolving a misspelling is a guess.
	got := ResolveName("grocerys", names("Groceries"))
	if got.Match != nil {
		t.Fatalf("resolved a misspelling to %q", got.Match.Name)
	}
}

func TestAParentPathResolvesTheChild(t *testing.T) {
	rows := []NameCandidate{
		{ID: "a", Name: "Groceries", Aliases: []string{"Food & Dining › Groceries"}},
		{ID: "b", Name: "Restaurants", Aliases: []string{"Food & Dining › Restaurants"}},
	}
	got := ResolveName("Food & Dining > Groceries", rows)
	if got.Match == nil || got.Match.ID != "a" {
		t.Fatalf("want Groceries, got %+v", got)
	}
}
