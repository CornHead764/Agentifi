package domain

import "testing"

func TestSuspectBalanceReset(t *testing.T) {
	const established = SuspectResetMinDays + 30

	cases := []struct {
		name             string
		prior            string
		hadPrior         bool
		incoming         string
		providerReported bool
		establishedDays  int
		acceptZero       bool
		want             bool
	}{
		{
			name:             "established liability drops to zero is withheld",
			prior:            "-400000.00",
			hadPrior:         true,
			incoming:         "0",
			providerReported: true,
			establishedDays:  established,
			want:             true,
		},
		{
			name:             "established asset drops to zero is withheld",
			prior:            "42000.00",
			hadPrior:         true,
			incoming:         "0",
			providerReported: true,
			establishedDays:  established,
			want:             true,
		},
		{
			name:             "provider reported nothing is withheld",
			prior:            "42000.00",
			hadPrior:         true,
			incoming:         "0",
			providerReported: false,
			establishedDays:  established,
			want:             true,
		},
		{
			name:             "small prior balance is applied",
			prior:            "-42.00",
			hadPrior:         true,
			incoming:         "0",
			providerReported: true,
			establishedDays:  established,
			want:             false,
		},
		{
			name:             "newly established balance is applied",
			prior:            "42000.00",
			hadPrior:         true,
			incoming:         "0",
			providerReported: true,
			establishedDays:  SuspectResetMinDays - 1,
			want:             false,
		},
		{
			name:             "account accepting zeros is applied",
			prior:            "-400000.00",
			hadPrior:         true,
			incoming:         "0",
			providerReported: true,
			establishedDays:  established,
			acceptZero:       true,
			want:             false,
		},
		{
			name:             "incoming non-zero is applied",
			prior:            "-400000.00",
			hadPrior:         true,
			incoming:         "-388500.00",
			providerReported: true,
			establishedDays:  established,
			want:             false,
		},
		{
			name:             "manual account with no prior figure is applied",
			prior:            "0",
			hadPrior:         false,
			incoming:         "0",
			providerReported: true,
			establishedDays:  established,
			want:             false,
		},
		{
			name:             "threshold boundary is withheld",
			prior:            "-100.00",
			hadPrior:         true,
			incoming:         "0",
			providerReported: true,
			establishedDays:  established,
			want:             true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := SuspectBalanceReset(
				MustFromString(tc.prior), tc.hadPrior,
				MustFromString(tc.incoming), tc.providerReported,
				tc.establishedDays, tc.acceptZero,
			)
			if got != tc.want {
				t.Fatalf("SuspectBalanceReset = %v, want %v", got, tc.want)
			}
		})
	}
}

func ledgerRow(id, amount string, pending bool) Transaction {
	return Transaction{ID: ID(id), Amount: MustFromString(amount), IsPending: pending}
}

func TestExplainReportedBalance(t *testing.T) {
	charges := []Transaction{
		ledgerRow("charge-1", "-800.00", false),
		ledgerRow("charge-2", "-434.56", false),
	}
	withRow := func(rows []Transaction, extra ...Transaction) []Transaction {
		return append(append([]Transaction{}, rows...), extra...)
	}

	cases := []struct {
		name          string
		previous      string
		reported      string
		hasReported   bool
		before, after []Transaction
		feed          BalanceFeed
		wantExplained bool
		wantChanges   int
		wantMovement  string
		wantExpected  string
	}{
		{
			// A card owing 1,234.56 untouched for a month, then paid in full: the
			// payment is the only row since the last sync.
			name:          "card paid in full after a quiet month is explained",
			previous:      "-1234.56",
			reported:      "0.00",
			hasReported:   true,
			before:        charges,
			after:         withRow(charges, ledgerRow("payment", "1234.56", false)),
			feed:          FeedRead,
			wantExplained: true,
			wantChanges:   1,
			wantMovement:  "1234.56",
			wantExpected:  "0.00",
		},
		{
			name:          "zero with no transactions since is not explained",
			previous:      "-1234.56",
			reported:      "0.00",
			hasReported:   true,
			before:        charges,
			after:         charges,
			feed:          FeedRead,
			wantExplained: false,
			wantChanges:   0,
			wantMovement:  "0.00",
			wantExpected:  "-1234.56",
		},
		{
			name:          "a payment short of the balance is not explained",
			previous:      "-1234.56",
			reported:      "0.00",
			hasReported:   true,
			after:         []Transaction{ledgerRow("payment", "1000.00", false)},
			feed:          FeedRead,
			wantExplained: false,
			wantChanges:   1,
			wantMovement:  "1000.00",
			wantExpected:  "-234.56",
		},
		{
			name:          "a non-zero figure the rows reach is explained",
			previous:      "5310.00",
			reported:      "5187.50",
			hasReported:   true,
			after:         []Transaction{ledgerRow("grocer", "-98.50", false), ledgerRow("fuel", "-24.00", true)},
			feed:          FeedRead,
			wantExplained: true,
			wantChanges:   2,
			wantMovement:  "-122.50",
			wantExpected:  "5187.50",
		},
		{
			// The payment arrived pending last sync, with the balance unchanged;
			// it posts in this sync and the balance follows. Counted with pending rows the
			// ledger did not move, counted posted-only it changed by the payment.
			name:          "a pending payment posting explains a balance that excludes pending rows",
			previous:      "-1234.56",
			reported:      "0.00",
			hasReported:   true,
			before:        []Transaction{ledgerRow("payment", "1234.56", true)},
			after:         []Transaction{ledgerRow("payment", "1234.56", false)},
			feed:          FeedRead,
			wantExplained: true,
			wantChanges:   0,
			wantMovement:  "0.00",
			wantExpected:  "-1234.56",
		},
		{
			name:        "a pending row reposted under a new id moves nothing",
			previous:    "-1234.56",
			reported:    "0.00",
			hasReported: true,
			before:      []Transaction{ledgerRow("auth", "-61.00", true)},
			after: []Transaction{
				{ID: "auth", Amount: MustFromString("-61.00"), IsPending: true, IsDeleted: true},
				ledgerRow("posted", "-61.00", false),
			},
			feed:          FeedRead,
			wantExplained: false,
			wantChanges:   2,
			wantMovement:  "0.00",
			wantExpected:  "-1234.56",
		},
		{
			name:          "rows that would explain it do not when the feed was not read",
			previous:      "-1234.56",
			reported:      "0.00",
			hasReported:   true,
			after:         []Transaction{ledgerRow("payment", "1234.56", false)},
			feed:          FeedUnread,
			wantExplained: false,
			wantChanges:   1,
			wantMovement:  "1234.56",
			wantExpected:  "0.00",
		},
		{
			name:          "a balance-only account cannot be explained",
			previous:      "-150000.00",
			reported:      "0.00",
			hasReported:   true,
			feed:          FeedAbsent,
			wantExplained: false,
			wantChanges:   0,
			wantMovement:  "0.00",
			wantExpected:  "-150000.00",
		},
		{
			name:          "no reported figure is never explained",
			previous:      "-1234.56",
			reported:      "0.00",
			hasReported:   false,
			after:         []Transaction{ledgerRow("payment", "1234.56", false)},
			feed:          FeedRead,
			wantExplained: false,
			wantChanges:   1,
			wantMovement:  "1234.56",
			wantExpected:  "0.00",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExplainReportedBalance(
				MustFromString(tc.previous), MustFromString(tc.reported), tc.hasReported,
				tc.before, tc.after, tc.feed)
			if got.Explained != tc.wantExplained {
				t.Fatalf("Explained = %v, want %v", got.Explained, tc.wantExplained)
			}
			if got.Changes != tc.wantChanges {
				t.Fatalf("Changes = %d, want %d", got.Changes, tc.wantChanges)
			}
			if got.Movement.String() != tc.wantMovement {
				t.Fatalf("Movement = %s, want %s", got.Movement, tc.wantMovement)
			}
			if got.Expected.String() != tc.wantExpected {
				t.Fatalf("Expected = %s, want %s", got.Expected, tc.wantExpected)
			}
		})
	}
}

func TestTrustedBalance(t *testing.T) {
	established := MustFromString("-1234.56")
	cases := []struct {
		name           string
		stored         string
		hasStored      bool
		held           bool
		want           string
		wantFromStored bool
	}{
		{name: "a stored non-zero figure", stored: "-1180.00", hasStored: true, want: "-1180.00", wantFromStored: true},
		{name: "a stored zero is the dropped figure", stored: "0", hasStored: true, want: "-1234.56"},
		{name: "a held figure carries on", stored: "-1180.00", hasStored: true, held: true, want: "-1180.00", wantFromStored: true},
		{name: "no stored figure", stored: "0", want: "-1234.56"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, fromStored := TrustedBalance(MustFromString(tc.stored), tc.hasStored, tc.held, established)
			if got.String() != tc.want || fromStored != tc.wantFromStored {
				t.Fatalf("TrustedBalance = %s, %v; want %s, %v", got, fromStored, tc.want, tc.wantFromStored)
			}
		})
	}
}

func TestBalanceExplanationReason(t *testing.T) {
	cases := []struct {
		name string
		e    BalanceExplanation
		want string
	}{
		{
			name: "rows read but short",
			e: BalanceExplanation{
				Feed: FeedRead, HasReported: true, Reported: Zero,
				Previous: MustFromString("-1234.56"), Changes: 1,
				Movement: MustFromString("1000.00"), Expected: MustFromString("-234.56"),
			},
			want: "SimpleFIN reported $0.00; the last balance -$1,234.56 plus 1 new transaction (+$1,000.00) " +
				"comes to -$234.56. Keeping -$234.56 until you confirm the change.",
		},
		{
			name: "nothing arrived",
			e: BalanceExplanation{
				Feed: FeedRead, HasReported: true, Reported: Zero,
				Previous: MustFromString("42000.00"), Expected: MustFromString("42000.00"),
			},
			want: "SimpleFIN reported $0.00; the last balance $42,000.00 plus 0 new transactions ($0.00) " +
				"comes to $42,000.00. Keeping $42,000.00 until you confirm the change.",
		},
		{
			name: "balance-only account",
			e: BalanceExplanation{
				Feed: FeedAbsent, HasReported: true, Reported: Zero,
				Previous: MustFromString("-150000.00"), Expected: MustFromString("-150000.00"),
			},
			want: "SimpleFIN reported $0.00 for an account whose feed carries no transactions, so nothing " +
				"can explain the change from -$150,000.00. Keeping -$150,000.00 until you confirm the change.",
		},
		{
			name: "transactions not read",
			e: BalanceExplanation{
				Feed: FeedUnread, HasReported: true, Reported: Zero,
				Previous: MustFromString("42000.00"), Expected: MustFromString("42000.00"),
			},
			want: "SimpleFIN reported $0.00 without this account's transactions, so nothing explains the " +
				"change from $42,000.00. Keeping $42,000.00 until you confirm the change.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.e.Reason(); got != tc.want {
				t.Fatalf("Reason =\n%s\nwant\n%s", got, tc.want)
			}
		})
	}
}
