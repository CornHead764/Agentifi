package importer

// What the importer has been taught about each store, field by field.
//
// Two lists per store: the fields that reach a column, and the fields that
// deliberately do not, each with the reason. Anything in neither list is an
// error naming the store, the record and the field; silently ignoring it would
// decide by accident whether the schema needs it.

// universalDropped are the fields on every record that the envelope adds or
// that identify the record to Simplifi's own sync, not to us.
var universalDropped = map[string]string{
	"clientId":     "Simplifi's optimistic-write id; ours is the primary key",
	"dbVersion":    "Simplifi's own row version",
	"createdAt":    "our timestamps are written by the database on insert",
	"modifiedAt":   "our timestamps are written by the database on insert",
	"lastSyncDate": "a fact about Simplifi's sync, not about the money",
}

var (
	mappedFields  = map[string]map[string]bool{}
	droppedFields = map[string]map[string]string{}
	knownFields   = map[string]map[string]bool{}
)

func defineStore(name string, mapped []string, dropped map[string]string) {
	full := map[string]string{}
	for field, why := range universalDropped {
		full[field] = why
	}
	for field, why := range dropped {
		full[field] = why
	}
	mappedFields[name] = set(mapped...)
	droppedFields[name] = full
	known := set(mapped...)
	for field := range full {
		known[field] = true
	}
	knownFields[name] = known
}

func set(names ...string) map[string]bool {
	out := make(map[string]bool, len(names))
	for _, name := range names {
		out[name] = true
	}
	return out
}

// filterItemFields is one condition inside filterItems. Kept separately
// because the element is not a store record and has its own vocabulary.
var filterItemFields = set(
	"type", "ids", "values", "text", "negated", "isNegated", "groupIndex", "operator",
	"state", "min", "max", "minAmount", "maxAmount", "startDate", "endDate", "from", "to",
	"datePreset",
	// The per-facet id lists the export actually writes.
	"chartOfAccounts", "payees",
	// A filter item is a stored record of its own, with the same envelope
	// every other record carries.
	"id", "createdAt", "modifiedAt", "dbVersion",
)

// allocationFields is one element of allocations, a split line. Simplifi
// repeats several transaction-level fields on each allocation; ours live on
// the transaction.
var allocationFields = set(
	"id", "amount", "coa", "categoryId", "knownCategoryId", "memo", "tags", "position", "payee",
	"clientId", "type", "rate", "isRatePercentage", "isOverflow", "isTaxable", "isBillable",
	"isExcludedFromReports", "isExcludedFromF2S",
)

// attachmentFields is one element of attachments, when Simplifi inlines the
// document record.
var attachmentFields = set(
	"id", "name", "fileName", "filename", "contentType", "mimeType", "size", "sizeBytes", "url",
)

var recurrenceFields = set("interval", "frequency", "byMonthDay", "byDay", "byMonth", "alias", "count", "until", "endOn")

var plannedSpendingItemFields = set(
	"id", "name", "targetAmount", "overwrittenTargetAmount", "calculatedSpentAmount", "filterId",
	"recurring", "txnIds", "excludedTxnIds", "txnItems", "isHideExcludedFromUi", "rolloverAmount",
	"isAutoRelease", "createdAt", "modifiedAt", "dbVersion",
	// rolloverAmount is the figure the envelope carries forward and is mapped.
	// The three beside it are Simplifi's own bookkeeping for how that figure
	// was reached, which our rollover cascade recomputes.
	"cumulativeRolloverAmount", "resetRolloverAmount", "rolloverType",
	"clientId", "excludedTxnItems",
)

// balancePointFields is one point of a balance series.
var balancePointFields = set("date", "on", "asOf", "balance", "amount", "normalizedBalance")

func init() {
	defineStore("datasetsStore",
		[]string{"id", "name", "displayName", "currency", "timeZone", "timezone"},
		map[string]string{
			"userId":    "identity, not finance",
			"ownerId":   "identity, not finance",
			"isDefault": "a client preference",
			"status":    "Quicken subscription state",
			"type":      "Quicken product tier",

			"platform":            "Quicken's own product code",
			"subscriptionTier":    "Quicken subscription state",
			"entitlements":        "Quicken subscription state",
			"createdByClientId":   "which Quicken client created the dataset",
			"disableRemoteAccess": "a Quicken account setting",
			"headless":            "a Quicken account setting",
			"lastAccessedAt":      "a fact about Simplifi's sync, not about the money",
			"lastSyncedAt":        "a fact about Simplifi's sync, not about the money",
			"zipcode":             "address; not carried",
			"isDeleted":           "the dataset being imported is by definition live",
			"grantorFirstName":    "identity, not finance",
			"grantorLastName":     "identity, not finance",
			"grantorUsername":     "identity, not finance",
			"permissions":         "Quicken's own sharing model; ours is memberships",
			"itemAccessControl":   "Quicken's own sharing model; ours is memberships",
			"shared":              "Quicken's own sharing model; ours is memberships",
		})

	defineStore("institutionsStore",
		[]string{"id", "name", "domain", "url", "logoUrl"},
		map[string]string{
			"aggregators": "Quicken's aggregator routing; SimpleFIN is our only one",
			"isDeleted":   "an institution is reference data; nothing points at a dead one",
			"status":      "Quicken's own connectivity state",
			"phone":       "not carried",
			"address":     "not carried",

			"aggregator": "Quicken's aggregator routing; SimpleFIN is our only one",
			"plaid":      "Quicken's aggregator routing; SimpleFIN is our only one",
			"finicity":   "Quicken's aggregator routing; SimpleFIN is our only one",
			"quicken":    "Quicken's aggregator routing; SimpleFIN is our only one",
			"ofx":        "Quicken's aggregator routing; SimpleFIN is our only one",
			"fdp":        "Quicken's aggregator routing; SimpleFIN is our only one",
			"fds":        "Quicken's aggregator routing; SimpleFIN is our only one",
			"fiIssue":    "Quicken's own connectivity state",
			"logo":       "logo cache; the URL is taken instead",
			"logoStyle":  "logo cache; the URL is taken instead",
		})

	defineStore("accountsStore",
		[]string{
			"id", "institutionLoginId", "name", "description", "notes", "type", "subType",
			"usageType", "normalizedBalance", "onlineBalance", "onlineBalanceAt", "balanceAsOf",
			"goalBalance",
			"currency", "accountNumberMasked", "isClosed", "closedOn", "isDeleted", "isConnected",
			"isIgnored", "isExcludedFromAccountBar", "isExcludedFromBudgets",
			"isExcludedFromReports", "isBankPendingTxnsExcluded",
			"creditLimit", "interestRate",
		},
		map[string]string{
			"shadowId":             "Simplifi's pre-link placeholder id",
			"balanceAsOfOn":        "redundant with balanceAsOf",
			"currentBalanceAsOf":   "redundant with the normalized balance's timestamp",
			"currentBalanceAsOfOn": "redundant with the normalized balance's timestamp",
			"fundingAccountId":     "real ACH rails; out of scope",
			"aggregators":          "no imported account arrives connected",
			"disconnect":           "no imported account arrives connected",
			"line1":                "address; not carried",
			"line2":                "address; not carried",
			"city":                 "address; not carried",
			"state":                "address; not carried",
			"country":              "address; not carried",
			"zipCode":              "address; not carried",
			"logoUrl":              "taken from the institution instead",

			// Credit-card and loan terms with no column, and statement
			// figures, which are one cycle's number with nothing to refresh
			// them afterwards.
			"cashInterestRate":           "no column; the card's cash-advance rate",
			"annualFee":                  "no column; card terms",
			"minPayment":                 "no column; card terms",
			"cardType":                   "no column; card terms",
			"rewardsType":                "no column; card terms",
			"maturityDateOn":             "no column; loan terms",
			"monthlyFee":                 "no column; bank-account terms",
			"minimumNoFeeBalance":        "no column; bank-account terms",
			"freeBillPay":                "Quicken Bill Pay; out of scope",
			"atmFeeReimbursement":        "no column; bank-account terms",
			"statementCloseAt":           "no column; statement_close_day is set by hand after the import",
			"statementCloseBalance":      "a statement figure; the balance history carries the amounts",
			"statementDueAmount":         "a statement figure; the bill feed carries what is owed",
			"statementDueAt":             "a statement figure; the bill feed carries when",
			"statementEndAt":             "a statement figure",
			"statementMinPayment":        "a statement figure",
			"statementPastDueAmount":     "a statement figure",
			"statementLastPaymentAmount": "a statement figure; the ledger carries the payment",
			"statementLastPaymentAt":     "a statement figure; the ledger carries the payment",
			"make":                       "vehicle description; Agentifi prices vehicles from its own valuation source",
			"model":                      "vehicle description; Agentifi prices vehicles from its own valuation source",
			"trim":                       "vehicle description; Agentifi prices vehicles from its own valuation source",
			"year":                       "vehicle description; Agentifi prices vehicles from its own valuation source",
			"mileage":                    "vehicle description; Agentifi prices vehicles from its own valuation source",
			"condition":                  "vehicle description; Agentifi prices vehicles from its own valuation source",
			"makeId":                     "Quicken's own vehicle catalogue id",
			"modelId":                    "Quicken's own vehicle catalogue id",
			"trimId":                     "Quicken's own vehicle catalogue id",
			"autoUpdateMiles":            "a Quicken client preference",
			"customMilesPerMonth":        "a Quicken client preference",
			"useCalculatedMilesPerMonth": "a Quicken client preference",
			"isA2ABannerDismissed":       "a Quicken client preference",
			"businessId":                 "Quicken Business (a non-goal)",
		})

	defineStore("categoryStore",
		[]string{
			"id", "parentId", "name", "description", "type", "knownCategoryId", "txfId", "txfIds",
			"isNotEditable", "isNotUserAssignable", "isExcludedFromReports",
			"isExcludedFromCategoryList", "isExcludedFromBudgets", "isDeleted", "logoUrl",
			"usageType",
		},
		map[string]string{
			"parentClientId": "Simplifi's optimistic-write id for the parent",
			"sortOrder":      "our ordering is the tree walk",
		})

	defineStore("tagStore",
		[]string{"id", "name", "color", "colour", "isDeleted"},
		map[string]string{
			"description": "tags carry a name and a colour only",
			"type":        "Simplifi marks a goal's own tag; the goal already names it",
		})

	defineStore("filterStore",
		[]string{"id", "type", "filterItems", "isDeleted", "name", "queryText"},
		map[string]string{
			"parentIds": "the reference direction is already an FK from the envelope, " +
				"watchlist or rule (data-model.md §9)",
		})

	defineStore("transactionStore",
		[]string{
			"id", "accountId", "postedOn", "payee", "renamedPayee", "memo", "knownCategoryId",
			"coa", "amount", "split", "allocations", "balance", "tags", "attachments", "check",
			"state", "isReviewed", "transfer", "stModelId", "stDueOn", "isBill",
			"isSubscription", "isExcludedFromReports", "isExcludedFromF2S", "userFlag",
			"userFlagNote", "transactionRuleId", "estimateStatus", "acceptedOn", "expireOn",
			"isDeleted", "currency", "type", "subtype",
			// Read only to tell a forecast from a pending charge; see
			// transactionSources.
			"source",
		},
		map[string]string{
			"matchState": "Simplifi's downloaded-vs-manual reconciliation state",
			"matchedTxn": "the downloaded row this entry was merged with, which the " +
				"merge then consumed — not a transfer leg; see mapTransactions",
			"subtypeRelatedTxn": "the second leg of an investment action; no ledger column",
			"linkedItem":        "Simplifi's link to a bill or e-bill record; out of scope",
			"isBillable":        "Quicken Business",
			"acceptedBy":        "which user accepted an estimate; single-user import",
			"coordinates":       "the merchant's location; not carried into the ledger",
			"paymentProvider":   "Quicken Bill Pay; out of scope",
			"paymentSentStatus": "Quicken Bill Pay; out of scope",
			"paymentId":         "Quicken Bill Pay; out of scope",
			"ebillId":           "Bill Connect e-bill presentment; out of scope",
			"logoId":            "merchant logo cache",
			"logoUrl":           "merchant logo cache",
			"cpData":            "Quicken's connected-partner blob",
			"businessId":        "Quicken Business (a non-goal)",
			"customerId":        "Quicken Business (a non-goal)",
			"projectId":         "Quicken Business (a non-goal)",

			// The investment leg of a transaction. Holdings carry the
			// position; the ledger row carries only the cash side.
			"units":             "the investment leg; holdings carry the position",
			"costBasis":         "the investment leg; holdings carry the basis",
			"commission":        "the investment leg; no ledger column",
			"investmentTxnType": "the investment action; no ledger column",
			"securityId":        "the investment leg; holdings name the security",
			"securityIdType":    "the investment leg; holdings name the security",
			"localSecurityId":   "the brokerage's own security id",
			"securityName":      "the investment leg; the security master carries the name",
			"symbol":            "the investment leg; the security master carries the symbol",
		})

	defineStore("scheduledTransactionsStore",
		[]string{
			"id", "type", "dueOn", "recurrence", "transaction", "overrideNextDueOn",
			"overrideNextAmount", "reminderDays", "autoAcceptDays",
			"autoAdjustDueOn", "isDeleted", "isCompleted", "isUserVerified", "name", "displayName",
			"matchCriteria",
			"matchAmountMin", "matchAmountMax", "learnedDescriptions", "endOn", "startOn",
			"isActive",
		},
		map[string]string{
			"autoSendDays": "Quicken Bill Pay; out of scope",
			"autoAdjustAmount": "an e-bill amount source (EBILL_LAST_STATEMENT_BALANCE); " +
				"a linked bill always sets its own slot's amount",
			"nextDueOn": "derived from the rule and the fulfilled occurrences",

			"excludeF2S":               "the exclusion is set per transaction, not per series",
			"excludeReports":           "the exclusion is set per transaction, not per series",
			"providerBillerId":         "Bill Connect e-bill presentment; out of scope",
			"billPresentmentAccountId": "Bill Connect e-bill presentment; out of scope",
			"billPresentmentBillerId":  "Bill Connect e-bill presentment; out of scope",
			"billPresentmentModelId":   "Bill Connect e-bill presentment; out of scope",
			"billPresentmentProvider":  "Bill Connect e-bill presentment; out of scope",
			"presentmentAccountId":     "Bill Connect e-bill presentment; out of scope",
			"billerEnrollmentStatus":   "Bill Connect e-bill presentment; out of scope",
			"billerType":               "Bill Connect e-bill presentment; out of scope",
			"billSnapshot":             "Bill Connect e-bill presentment; out of scope",
			"paymentModelId":           "Quicken Bill Pay; out of scope",
			"paymentProvider":          "Quicken Bill Pay; out of scope",
			"printCheck":               "Quicken Bill Pay; out of scope",
			"relatedWebsite":           "a link the biller page shows; no column",
			"averagePaidCount":         "derived from the fulfilled occurrences",
			"matchAmountVariance":      "read through matchAmountMin and matchAmountMax",
			"resetMatchAmountVariance": "a variance the user cleared; the columns are nullable instead",
			"matchAnyAmount":           "read through matchCriteria",
		})

	defineStore("renameRuleStore",
		[]string{
			"id", "name", "filterId", "priority", "sortOrder", "isActive", "isDeleted",
			"renamedPayee", "setPayee", "coa", "categoryId", "tags", "memo", "notes",
			"isExcludedFromReports", "isExcludedFromF2S", "isReviewed", "conditions",
			"filterItems",
			// Simplifi's rename rule carries no filter; these three are it.
			"renamePayeeFrom", "renamePayeeTo", "matchCriteria",
		},
		map[string]string{
			"appliesTo":           "which existing rows the rule was run over; a one-off action",
			"applyToExistingTxns": "which existing rows the rule was run over; a one-off action",
			"isDoNotRename":       "a rule that renames nothing simply sets no payee",
		})

	defineStore(transactionRulesStore,
		[]string{
			"id", "isDeleted", "prevTransactionRuleId", "nextTransactionRuleId",
			"payeeSourceCondition", "amountSourceCondition", "coaSourceConditions",
			"accountSourceCondition", "usageSourceType",
			"payeeTarget", "coaTargetCondition", "tagsTargetIds", "memoTarget",
			"isExcludedFromF2STarget", "isExcludedFromReportsTarget", "isReviewedTarget",
			// Read only to switch a rule off that sets them.
			"businessCoaTargetCondition", "businessTargetId",
		},
		map[string]string{
			"hashKey":                     "the server's change detection",
			"applyToExistingTransactions": "which existing rows the rule was run over when saved; a one-off action",
			"clientIdFromEntityClientId":  "client plumbing",
			"useDefaultAccountUsage":      "Simplifi's business usage; a space here has no business side",
		})

	defineStore("goalStore",
		[]string{
			"id", "accountId", "name", "description", "notes", "targetAmount", "targetOn",
			"startOn", "completedOn", "contributionAmount", "contributionFrequency",
			"contributedThisMonth", "savedSoFar", "spent", "txnIds", "isTakenFromF2S", "tagId",
			"imageId", "type", "isDeleted", "initialAccount",
		},
		map[string]string{})

	freeToSpend := []string{
		"id", "date", "calculatedRolloverAmount",
		// The Goals bucket names goal ids, not transaction ids — see the
		// deviation recorded in data-model.md §9.
		"goalsIds", "excludedGoalsIds",
		"calculatedPlannedSpendingAmount", "plannedSpendingItems",
		"spentAmount", "spentTxnIds", "excludedSpentTxnIds",
		"setAside", "totalToSpendAmount", "leftToSpendAmount",
		"projectedOtherSpending", "projectionType", "projectionStartDate", "projectionEndDate",
		"projectionBuffer", "isClosedOut", "showClosedOut", "newMonthViewed",
		"isOtherSpendingHideExcludedFromUi",
	}
	for _, bucket := range buckets {
		freeToSpend = append(freeToSpend,
			"calculated"+bucket.simplifi+"Amount",
			bucket.lowerSimplifi()+"TxnIds",
			"excluded"+bucket.simplifi+"TxnIds",
			"overwritten"+bucket.simplifi+"Amount",
			"resetOverwritten"+bucket.simplifi+"Amount",
		)
	}
	defineStore("freeToSpendStore", freeToSpend, map[string]string{
		"isDeleted": "a month is never deleted; it is closed out",
	})

	defineStore("spendingWatchListStore",
		[]string{
			"id", "name", "emoji", "filterId", "targetAmount", "period", "startDate", "endDate",
			"isDeleted",
		},
		map[string]string{
			"resetTargetAmount":       "a target the user cleared; the column is nullable instead",
			"thisMonth":               "derived (data-model.md §10)",
			"yearToDate":              "derived (data-model.md §10)",
			"spentSoFar":              "derived (data-model.md §10)",
			"monthlyTrend":            "derived (data-model.md §10)",
			"numMonthlyTrendMonths":   "derived (data-model.md §10)",
			"monthlyCalculations":     "derived (data-model.md §10)",
			"monthSpendingProjection": "derived (data-model.md §10)",
		})

	defineStore("securitiesStore",
		[]string{
			"id", "symbol", "ticker", "name", "type", "exchange", "currency", "cusip", "isin",
			"isDeleted",
			// The brokerage's own id, which is how holdings name a security.
			"securityId",
		},
		map[string]string{
			"logoUrl": "logo cache",
			"sector":  "no column; not used in any calculation",

			"securityIdType":   "which scheme securityId is in",
			"securityType":     "read through type",
			"currentUnitPrice": "the quote store carries the price",
			"ownerId":          "identity, not finance",
		})

	defineStore("investmentHoldingsV2Store",
		[]string{
			"id", "accountId", "securityId", "shares", "quantity", "costBasis", "averageCost",
			"isCostBasisComplete", "marketValue", "asOf", "asOfOn",
		},
		map[string]string{
			"gainLoss":  "derived from market value and basis",
			"dayChange": "derived from the quote",
			"price":     "lives on the security, not the position",

			"currentPrice":                 "lives on the security, not the position",
			"currentPriceAt":               "lives on the security, not the position",
			"symbol":                       "the security master carries the symbol",
			"securityName":                 "the security master carries the name",
			"localSecurityId":              "the brokerage's own security id",
			"securityIdType":               "which scheme the security id is in",
			"isCash":                       "a sweep position; the account's own balance carries the cash",
			"userCostBasis":                "read through costBasis",
			"userCostBasisOn":              "when the user last set the basis by hand",
			"quantityWhenUserSetCostBasis": "Simplifi's own check that a hand-set basis is still current",
		})

	defineStore("investmentQuotesV2Store",
		[]string{"id", "securityId", "symbol", "ticker", "price", "lastPrice", "priorClose", "asOf"},
		map[string]string{
			"dayChange":        "derived from price and prior close",
			"dayChangePercent": "derived from price and prior close",
			"volume":           "no column; not used in any calculation",

			// Market data Agentifi does not store; it keeps only the last
			// price and the prior close.
			"amtChange":            "derived from price and prior close",
			"pctChange":            "derived from price and prior close",
			"openPrice":            "market data; no column",
			"closePrice":           "read through lastPrice",
			"previousClosePrice":   "read through priorClose",
			"dayHigh":              "market data; no column",
			"dayLow":               "market data; no column",
			"yearHigh":             "market data; no column",
			"yearLow":              "market data; no column",
			"fiftyTwoWeekHigh":     "market data; no column",
			"fiftyTwoWeekLow":      "market data; no column",
			"fiftyTwoWeekHighDate": "market data; no column",
			"fiftyTwoWeekLowDate":  "market data; no column",
			"mktCap":               "market data; no column",
			"peRatio":              "market data; no column",
			"divYield":             "market data; no column",
			"name":                 "the security master carries the name",
			"relatedSymbol":        "the quote provider's cross-reference",
			"realTime":             "whether the quote was live or delayed",
			"quoteTime":            "read through asOf",
		})

	defineStore("accountsBalancesStore",
		[]string{
			"id", "accountId", "balances", "points", "history",
			"balanceAmount", "balanceOn", "balanceAt", "balanceType",
		},
		map[string]string{
			"currency":  "the account's currency governs",
			"isDeleted": "a superseded balance; the surviving row for the day is the one kept",
		})

	defineStore("alertRulesStore",
		[]string{
			"id", "type", "alertType", "accountId", "isEnabled", "enabled", "isPaused", "channels",
			"email", "push", "inApp", "threshold", "thresholdAmount", "thresholdCount",
			"thresholdPercent", "isDeleted", "preferences", "inputValues", "inputVariables",
		},
		map[string]string{
			"userId":      "the import writes one owner and attaches every rule to it",
			"title":       "the catalog on our side carries the wording",
			"description": "the catalog on our side carries the wording",
			"group":       "the catalog on our side carries the grouping",
			"product":     "which Quicken product ships the alert",
			"icon":        "presentation",
			"iconBgColor": "presentation",
			"isHidden":    "presentation",
		})
}
