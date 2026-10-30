package domain

import (
	"fmt"
	"math"
	"sort"
)

type CategoryEvalLabel struct {
	Key      string `json:"key"`
	Date     Date   `json:"date"`
	Category string `json:"category"`
}

// SplitCategoryEvaluation keeps whole calendar days on one side of the split.
// It sorts a copy, leaving the caller's labels untouched.
func SplitCategoryEvaluation(labels []CategoryEvalLabel, holdout float64, cutoff Date) (train, test []CategoryEvalLabel, boundary Date, err error) {
	fail := func(message string) ([]CategoryEvalLabel, []CategoryEvalLabel, Date, error) {
		return nil, nil, Date{}, fmt.Errorf("category evaluation: %s", message)
	}
	if len(labels) < 2 {
		return fail("need nonempty training and test sets")
	}
	for _, label := range labels {
		if label.Date.IsZero() || DateOf(label.Date.Time()) != label.Date {
			return fail("labels require valid dates")
		}
	}
	sorted := append([]CategoryEvalLabel(nil), labels...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Date != sorted[j].Date {
			return sorted[i].Date.Before(sorted[j].Date)
		}
		return sorted[i].Key < sorted[j].Key
	})
	boundary = cutoff
	if cutoff.IsZero() {
		if math.IsNaN(holdout) || holdout <= 0 || holdout >= 1 {
			return fail("holdout must be between zero and one")
		}
		count := int(math.Ceil(float64(len(sorted)) * holdout))
		boundary = sorted[len(sorted)-count].Date
	} else if DateOf(cutoff.Time()) != cutoff {
		return fail("cutoff must be a valid date")
	}
	split := sort.Search(len(sorted), func(i int) bool { return !sorted[i].Date.Before(boundary) })
	if split == 0 || split == len(sorted) {
		return fail("need nonempty training and test sets")
	}
	return sorted[:split:split], sorted[split:], boundary, nil
}

type CategoryEvalPrediction struct {
	TrueCategory          string   `json:"true_category"`
	SuggestedCategory     string   `json:"suggested_category"`
	Basis                 string   `json:"basis"`
	Confidence            *float64 `json:"confidence"`
	TrueCategoryName      string   `json:"true_category_name,omitempty"`
	SuggestedCategoryName string   `json:"suggested_category_name,omitempty"`
}

type CategoryEvalMetrics struct {
	Rows      int      `json:"rows"`
	Suggested int      `json:"suggested"`
	Correct   int      `json:"correct"`
	Coverage  *float64 `json:"coverage"`
	Accuracy  *float64 `json:"accuracy"`
}

type CategoryEvalConfusion struct {
	TrueCategory      string `json:"true_category"`
	SuggestedCategory string `json:"suggested_category"`
	Count             int    `json:"count"`
}

type CategoryEvalScore struct {
	Overall      CategoryEvalMetrics            `json:"overall"`
	ByBasis      map[string]CategoryEvalMetrics `json:"by_basis"`
	ByConfidence map[string]CategoryEvalMetrics `json:"by_confidence"`
	Confusions   []CategoryEvalConfusion        `json:"confusions"`
}

// ScoreCategoryEvaluation measures abstention separately from wrong suggestions.
// Every group's coverage uses all evaluated rows, not just that group's rows.
func ScoreCategoryEvaluation(predictions []CategoryEvalPrediction) CategoryEvalScore {
	score := CategoryEvalScore{
		ByBasis: make(map[string]CategoryEvalMetrics),
		ByConfidence: map[string]CategoryEvalMetrics{
			"unknown": {}, "<0.4": {}, "0.4-0.7": {}, "0.7-0.9": {}, ">=0.9": {},
		},
		Confusions: []CategoryEvalConfusion{},
	}
	type pair struct{ truth, suggested string }
	confusions := make(map[pair]int)
	displayNames := make(map[string]string)
	rememberName := func(id, name string) {
		if name != "" && (displayNames[id] == "" || name < displayNames[id]) {
			displayNames[id] = name
		}
	}
	add := func(m CategoryEvalMetrics, p CategoryEvalPrediction) CategoryEvalMetrics {
		m.Rows++
		if p.SuggestedCategory != "" {
			m.Suggested++
			if p.SuggestedCategory == p.TrueCategory {
				m.Correct++
			}
		}
		return m
	}
	for _, p := range predictions {
		rememberName(p.TrueCategory, p.TrueCategoryName)
		rememberName(p.SuggestedCategory, p.SuggestedCategoryName)
		score.Overall = add(score.Overall, p)
		score.ByBasis[p.Basis] = add(score.ByBasis[p.Basis], p)
		band := categoryEvalConfidenceBand(p.Confidence)
		score.ByConfidence[band] = add(score.ByConfidence[band], p)
		if p.SuggestedCategory != "" && p.SuggestedCategory != p.TrueCategory {
			confusions[pair{p.TrueCategory, p.SuggestedCategory}]++
		}
	}
	finish := func(m CategoryEvalMetrics) CategoryEvalMetrics {
		if len(predictions) > 0 {
			coverage := float64(m.Suggested) / float64(len(predictions))
			m.Coverage = &coverage
		}
		if m.Suggested > 0 {
			accuracy := float64(m.Correct) / float64(m.Suggested)
			m.Accuracy = &accuracy
		}
		return m
	}
	score.Overall = finish(score.Overall)
	for basis, m := range score.ByBasis {
		score.ByBasis[basis] = finish(m)
	}
	for band, m := range score.ByConfidence {
		score.ByConfidence[band] = finish(m)
	}
	display := func(id string) string {
		if name := displayNames[id]; name != "" {
			return name
		}
		return id
	}
	type rankedConfusion struct {
		identity pair
		value    CategoryEvalConfusion
	}
	ranked := make([]rankedConfusion, 0, len(confusions))
	for p, count := range confusions {
		ranked = append(ranked, rankedConfusion{p, CategoryEvalConfusion{display(p.truth), display(p.suggested), count}})
	}
	sort.Slice(ranked, func(i, j int) bool {
		a, b := ranked[i], ranked[j]
		if a.value.Count != b.value.Count {
			return a.value.Count > b.value.Count
		}
		if a.value.TrueCategory != b.value.TrueCategory {
			return a.value.TrueCategory < b.value.TrueCategory
		}
		if a.value.SuggestedCategory != b.value.SuggestedCategory {
			return a.value.SuggestedCategory < b.value.SuggestedCategory
		}
		if a.identity.truth != b.identity.truth {
			return a.identity.truth < b.identity.truth
		}
		return a.identity.suggested < b.identity.suggested
	})
	if len(ranked) > 20 {
		ranked = ranked[:20]
	}
	for _, confusion := range ranked {
		score.Confusions = append(score.Confusions, confusion.value)
	}
	return score
}

func categoryEvalConfidenceBand(confidence *float64) string {
	if confidence == nil || math.IsNaN(*confidence) || math.IsInf(*confidence, 0) {
		return "unknown"
	}
	switch {
	case *confidence < 0.4:
		return "<0.4"
	case *confidence < 0.7:
		return "0.4-0.7"
	case *confidence < 0.9:
		return "0.7-0.9"
	default:
		return ">=0.9"
	}
}
