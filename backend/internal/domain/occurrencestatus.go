package domain

// OccurrenceStatus is the chip an occurrence carries, wherever it is drawn:
// the plan's Bills bucket, the reminders, a series' history and the review
// queue. `received` and `paid` are one state (money moved), worded for income
// and for everything else.
type OccurrenceStatus string

const (
	OccurrenceReceived OccurrenceStatus = "received"
	OccurrencePaid     OccurrenceStatus = "paid"
	OccurrencePastDue  OccurrenceStatus = "past_due"
	OccurrenceUpcoming OccurrenceStatus = "upcoming"
	OccurrenceSkipped  OccurrenceStatus = "skipped"
)

// FulfilledStatus is what "the money moved" is called for this kind of series.
func (k SeriesKind) FulfilledStatus() OccurrenceStatus {
	if k == SeriesIncome {
		return OccurrenceReceived
	}
	return OccurrencePaid
}

// OccurrenceStatusOf is an occurrence's status on asOf. An unfulfilled one is
// past due from the day after it was due; income never is. calculations.md §8.
func OccurrenceStatusOf(kind SeriesKind, isFulfilled bool, dueOn, asOf Date) OccurrenceStatus {
	switch {
	case isFulfilled:
		return kind.FulfilledStatus()
	case kind != SeriesIncome && dueOn.Before(asOf):
		return OccurrencePastDue
	default:
		return OccurrenceUpcoming
	}
}
