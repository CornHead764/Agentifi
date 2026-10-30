package domain

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
)

func TestSplitEval(t *testing.T) {
	// Expected values come from the split rule in docs/categorization-evaluation.md.
	day := func(d int) Date { return NewDate(2025, 1, d) }
	labels := []CategoryEvalLabel{
		{"f", day(4), "Food"}, {"b", day(2), "Food"},
		{"a", day(1), "Food"}, {"e", day(4), "Food"},
		{"d", day(3), "Food"}, {"c", day(2), "Food"},
	}
	original := append([]CategoryEvalLabel(nil), labels...)
	tests := []struct {
		name        string
		holdout     float64
		cutoff      Date
		train, test string
		boundary    Date
	}{
		// ceil(6 * .2) = 2: both day-4 rows are held out.
		{"automatic", .2, Date{}, "abcd", "ef", day(4)},
		// ceil(6 * .5) = 3: day 3 and day 4 are held out.
		{"rounded", .5, Date{}, "abc", "def", day(3)},
		// ceil(6 * .7) = 5 selects day 2, including both day-2 rows.
		{"whole day", .7, Date{}, "a", "bcdef", day(2)},
		{"explicit ignores fraction", math.NaN(), day(2), "a", "bcdef", day(2)},
	}
	keys := func(rows []CategoryEvalLabel) string {
		var result string
		for _, row := range rows {
			result += row.Key
		}
		return result
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			train, test, boundary, err := SplitCategoryEvaluation(labels, tt.holdout, tt.cutoff)
			if err != nil {
				t.Fatal(err)
			}
			if keys(train) != tt.train || keys(test) != tt.test || boundary != tt.boundary {
				t.Fatalf("got %s / %s at %s; want %s / %s at %s", keys(train), keys(test), boundary, tt.train, tt.test, tt.boundary)
			}
			for _, a := range train {
				for _, b := range test {
					if !a.Date.Before(b.Date) {
						t.Fatalf("date leak: %s >= %s", a.Date, b.Date)
					}
				}
			}
		})
	}
	if !reflect.DeepEqual(labels, original) {
		t.Fatal("input was mutated")
	}
}

func TestSplitEvalRejectsEmpty(t *testing.T) {
	day := NewDate(2025, 1, 1)
	labels := []CategoryEvalLabel{{Key: "a", Date: day}, {Key: "b", Date: day.AddDays(1)}}
	for _, fraction := range []float64{0, -1, 1, 2, math.NaN(), math.Inf(1)} {
		if _, _, _, err := SplitCategoryEvaluation(labels, fraction, Date{}); err == nil {
			t.Errorf("fraction %v accepted", fraction)
		}
	}
	for _, rows := range [][]CategoryEvalLabel{nil, labels[:1], {labels[0], labels[0]}, {{Date: Date{}}, labels[1]}, {{Date: NewDate(2025, 2, 30)}, labels[1]}} {
		if _, _, _, err := SplitCategoryEvaluation(rows, .2, Date{}); err == nil {
			t.Errorf("accepted rows: %+v", rows)
		}
	}
	for _, cutoff := range []Date{day, day.AddDays(-1), day.AddDays(2), NewDate(2025, 2, 30)} {
		if _, _, _, err := SplitCategoryEvaluation(labels, .2, cutoff); err == nil {
			t.Errorf("accepted cutoff: %s", cutoff)
		}
	}
}

func categoryEvalRatio(v float64) *float64 { return &v }

func TestScoreEval(t *testing.T) {
	// Expected counts are worked by hand from the metrics in docs/categorization-evaluation.md.
	confidence := func(v float64) *float64 { return &v }
	predictions := []CategoryEvalPrediction{
		{TrueCategory: "Food", SuggestedCategory: "Food", Basis: "history", Confidence: confidence(.9)},
		{TrueCategory: "Food", SuggestedCategory: "Travel", Basis: "history", Confidence: confidence(.7)},
		{TrueCategory: "Travel", SuggestedCategory: "Travel", Basis: "rule", Confidence: confidence(.4)},
		{TrueCategory: "Food", SuggestedCategory: "", Basis: "history", Confidence: nil},
		{TrueCategory: "Food", SuggestedCategory: "Travel", Basis: "rule", Confidence: confidence(.39)},
		{TrueCategory: "Travel", SuggestedCategory: "Food", Basis: "model", Confidence: nil},
		{TrueCategory: "Food", SuggestedCategory: "Food", Basis: "model", Confidence: confidence(.89)},
		{TrueCategory: "", SuggestedCategory: "", Basis: "", Confidence: confidence(.69)},
	}
	got := ScoreCategoryEvaluation(predictions)
	// Six of eight receive suggestions; three of those six are right.
	if !reflect.DeepEqual(got.Overall, CategoryEvalMetrics{8, 6, 3, categoryEvalRatio(.75), categoryEvalRatio(.5)}) {
		t.Fatalf("overall: %+v", got.Overall)
	}
	wantBasis := map[string]CategoryEvalMetrics{
		"history": {3, 2, 1, categoryEvalRatio(.25), categoryEvalRatio(.5)}, "rule": {2, 2, 1, categoryEvalRatio(.25), categoryEvalRatio(.5)},
		"model": {2, 2, 1, categoryEvalRatio(.25), categoryEvalRatio(.5)}, "": {1, 0, 0, categoryEvalRatio(0), nil},
	}
	if !reflect.DeepEqual(got.ByBasis, wantBasis) {
		t.Errorf("basis: %+v", got.ByBasis)
	}
	wantConfidence := map[string]CategoryEvalMetrics{
		"unknown": {2, 1, 0, categoryEvalRatio(.125), categoryEvalRatio(0)}, "<0.4": {1, 1, 0, categoryEvalRatio(.125), categoryEvalRatio(0)},
		"0.4-0.7": {2, 1, 1, categoryEvalRatio(.125), categoryEvalRatio(1)}, "0.7-0.9": {2, 2, 1, categoryEvalRatio(.25), categoryEvalRatio(.5)}, ">=0.9": {1, 1, 1, categoryEvalRatio(.125), categoryEvalRatio(1)},
	}
	if !reflect.DeepEqual(got.ByConfidence, wantConfidence) {
		t.Errorf("confidence: %+v", got.ByConfidence)
	}
	wantConfusions := []CategoryEvalConfusion{{"Food", "Travel", 2}, {"Travel", "Food", 1}}
	if !reflect.DeepEqual(got.Confusions, wantConfusions) {
		t.Errorf("confusions: %+v", got.Confusions)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"overall", "by_basis", "by_confidence", "confusions", "rows", "suggested", "correct", "coverage", "accuracy", "true_category", "suggested_category", "count"} {
		if !strings.Contains(string(encoded), `"`+key+`":`) {
			t.Errorf("missing JSON key %s: %s", key, encoded)
		}
	}
}

func TestScoreEvalEmpty(t *testing.T) {
	got := ScoreCategoryEvaluation(nil)
	if got.Overall != (CategoryEvalMetrics{}) || len(got.ByBasis) != 0 || len(got.ByConfidence) != 5 || len(got.Confusions) != 0 {
		t.Fatalf("empty score: %+v", got)
	}
	for band, metrics := range got.ByConfidence {
		if metrics != (CategoryEvalMetrics{}) {
			t.Errorf("%s: %+v", band, metrics)
		}
	}
	encoded, err := json.Marshal(got.Overall)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"rows":0,"suggested":0,"correct":0,"coverage":null,"accuracy":null}` {
		t.Fatalf("undefined overall ratios: %s", encoded)
	}
	abstentions := ScoreCategoryEvaluation([]CategoryEvalPrediction{{TrueCategory: "Food", Basis: "history"}})
	for name, metrics := range abstentions.ByConfidence {
		if metrics.Accuracy != nil || metrics.Coverage == nil || *metrics.Coverage != 0 {
			t.Errorf("abstaining/empty confidence band %s: %+v", name, metrics)
		}
	}
	encoded, err = json.Marshal(abstentions.ByBasis["history"])
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"rows":1,"suggested":0,"correct":0,"coverage":0,"accuracy":null}` {
		t.Fatalf("abstaining basis ratios: %s", encoded)
	}
	for _, v := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		score := ScoreCategoryEvaluation([]CategoryEvalPrediction{{Confidence: &v}})
		if score.ByConfidence["unknown"].Rows != 1 {
			t.Errorf("confidence %v not unknown", v)
		}
	}
}

func TestConfusionOrder(t *testing.T) {
	var predictions []CategoryEvalPrediction
	for i := 24; i >= 0; i-- {
		predictions = append(predictions, CategoryEvalPrediction{TrueCategory: fmt.Sprintf("Category%02d", i), SuggestedCategory: "Wrong"})
	}
	predictions = append(predictions,
		CategoryEvalPrediction{TrueCategory: "Category24", SuggestedCategory: "Wrong"},
		CategoryEvalPrediction{TrueCategory: "Category00", SuggestedCategory: "Another"},
	)
	score := ScoreCategoryEvaluation(predictions)
	if len(score.Confusions) != 20 {
		t.Fatalf("got %d confusions", len(score.Confusions))
	}
	if score.Confusions[0] != (CategoryEvalConfusion{"Category24", "Wrong", 2}) ||
		score.Confusions[1] != (CategoryEvalConfusion{"Category00", "Another", 1}) ||
		score.Confusions[2] != (CategoryEvalConfusion{"Category00", "Wrong", 1}) ||
		score.Confusions[19] != (CategoryEvalConfusion{"Category17", "Wrong", 1}) {
		t.Fatalf("wrong confusion order: %+v", score.Confusions)
	}
}

func TestConfusionDisplayOrder(t *testing.T) {
	var predictions []CategoryEvalPrediction
	// Identity order is opposite display order; all pairs have count one.
	for i := 24; i >= 0; i-- {
		predictions = append(predictions, CategoryEvalPrediction{
			TrueCategory: fmt.Sprintf("id%02d", 24-i), SuggestedCategory: "wrong-id",
			TrueCategoryName: fmt.Sprintf("Display%02d", i), SuggestedCategoryName: "Wrong",
		})
	}
	score := ScoreCategoryEvaluation(predictions)
	if len(score.Confusions) != 20 {
		t.Fatalf("confusions: %+v", score.Confusions)
	}
	for i, confusion := range score.Confusions {
		want := CategoryEvalConfusion{fmt.Sprintf("Display%02d", i), "Wrong", 1}
		if confusion != want {
			t.Errorf("confusion %d: %+v, want %+v", i, confusion, want)
		}
	}
}

func TestDisplayLabelsKeepIdentities(t *testing.T) {
	predictions := []CategoryEvalPrediction{
		{TrueCategory: "a", SuggestedCategory: "b", TrueCategoryName: "Same", SuggestedCategoryName: "Same"},
		{TrueCategory: "a", SuggestedCategory: "b", TrueCategoryName: "Same", SuggestedCategoryName: "Same"},
		{TrueCategory: "c", SuggestedCategory: "d", TrueCategoryName: "Same", SuggestedCategoryName: "Same"},
		{TrueCategory: "e", SuggestedCategory: "e", TrueCategoryName: "First", SuggestedCategoryName: "Second"},
	}
	score := ScoreCategoryEvaluation(predictions)
	want := []CategoryEvalConfusion{{"Same", "Same", 2}, {"Same", "Same", 1}}
	if !reflect.DeepEqual(score.Confusions, want) {
		t.Errorf("confusions: %+v", score.Confusions)
	}
	if score.Overall.Correct != 1 {
		t.Errorf("correctness must compare IDs: %+v", score.Overall)
	}
	// Display suggested category is the second ordering key, not its identity.
	score = ScoreCategoryEvaluation([]CategoryEvalPrediction{
		{TrueCategory: "t", SuggestedCategory: "a", TrueCategoryName: "True", SuggestedCategoryName: "Zulu"},
		{TrueCategory: "t", SuggestedCategory: "z", TrueCategoryName: "True", SuggestedCategoryName: "Alpha"},
	})
	if score.Confusions[0].SuggestedCategory != "Alpha" {
		t.Errorf("suggested label order: %+v", score.Confusions)
	}
}
