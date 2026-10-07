package domain

// DefaultCategory is one category of the starter tree, with the subcategories
// filed beneath it. A subcategory takes its group's kind and exclusion pair.
//
// KnownCategoryID and TxfID are Simplifi's catalog markers, the values its
// export carries for the category of the same name; empty where Simplifi has
// none. NotEditable mirrors Simplifi's system categories, which a person
// cannot rename or move.
type DefaultCategory struct {
	Name     string
	Kind     CategoryKind
	Children []DefaultCategory

	KnownCategoryID string
	TxfID           string
	NotEditable     bool

	ExcludedFromReports      bool
	ExcludedFromSpendingPlan bool
}

// DefaultCategories is the tree every new space starts with. Opening Balance,
// Balance Adjustment and Uncategorized are absent on purpose: the first two
// are marked by source, and uncategorized is the absence of a category.
// Transfer and Credit Card Payment are here because a matched pair's legs are
// filed under them; their kind keeps them out of every total, and their
// exclusion pair says the same.
var DefaultCategories = []DefaultCategory{
	{Name: "Auto & Transport", Kind: CategoryExpense, KnownCategoryID: "1000000000", Children: []DefaultCategory{
		{Name: "Car Insurance", KnownCategoryID: "1000100000"},
		{Name: "Car Payment", KnownCategoryID: "1000150000"},
		{Name: "Car Wash", KnownCategoryID: "1000200000"},
		{Name: "Gas & Fuel", KnownCategoryID: "1000300000"},
		{Name: "Parking", KnownCategoryID: "1000400000"},
		{Name: "Public Transportation", KnownCategoryID: "1000500000"},
		{Name: "Registration", KnownCategoryID: "1000600000"},
		{Name: "Service & Parts", KnownCategoryID: "1000800000"},
		{Name: "Tolls", KnownCategoryID: "1000900000"},
	}},
	{Name: "Cash & ATM", Kind: CategoryExpense, KnownCategoryID: "1750000000"},
	{Name: "Charity & Donations", Kind: CategoryExpense, KnownCategoryID: "2000000000", TxfID: "USA_280"},
	{Name: "Food & Dining", Kind: CategoryExpense, Children: []DefaultCategory{
		{Name: "Bars", KnownCategoryID: "2250200000"},
		{Name: "Coffee Shops", KnownCategoryID: "2250400000"},
		{Name: "Fast Food", KnownCategoryID: "2250600000"},
		{Name: "Restaurants", KnownCategoryID: "2250000000"},
		{Name: "Groceries", KnownCategoryID: "4000000000"},
	}},
	{Name: "Education", Kind: CategoryExpense, KnownCategoryID: "2500000000", Children: []DefaultCategory{
		{Name: "Books & Supplies", KnownCategoryID: "2500250000"},
		{Name: "Student Loan", KnownCategoryID: "2500500000", TxfID: "USA_636"},
		{Name: "Tuition", KnownCategoryID: "2500750000"},
	}},
	{Name: "Entertainment", Kind: CategoryExpense, KnownCategoryID: "2750000000"},
	{Name: "Fees & Charges", Kind: CategoryExpense, KnownCategoryID: KnownCategoryFeesAndCharges, Children: []DefaultCategory{
		{Name: "Finance Charge", KnownCategoryID: "3000350000"},
		{Name: "Service Fee", KnownCategoryID: "3000700000"},
	}},
	{Name: "Financial", Kind: CategoryExpense, KnownCategoryID: "3250000000", Children: []DefaultCategory{
		{Name: "Financial Advisor", KnownCategoryID: "3250350000", TxfID: "USA_282"},
		{Name: "Life Insurance", KnownCategoryID: "3250700000"},
		{Name: "Investment"},
	}},
	{Name: "Fitness", Kind: CategoryExpense, KnownCategoryID: "3500000000", Children: []DefaultCategory{
		{Name: "Gym", KnownCategoryID: "3500250000"},
		{Name: "Workout Classes", KnownCategoryID: "3500750000"},
	}},
	{Name: "Gifts", Kind: CategoryExpense, KnownCategoryID: "1250000000"},
	{Name: "Health", Kind: CategoryExpense, KnownCategoryID: "4250000000", Children: []DefaultCategory{
		{Name: "Dentist", KnownCategoryID: "4250150000", TxfID: "USA_484"},
		{Name: "Doctor", KnownCategoryID: "4250300000", TxfID: "USA_484"},
		{Name: "Eyecare", KnownCategoryID: "4250450000", TxfID: "USA_484"},
		{Name: "Pharmacy", KnownCategoryID: "4250750000", TxfID: "USA_273"},
	}},
	{Name: "Home", Kind: CategoryExpense, KnownCategoryID: "4500000000", Children: []DefaultCategory{
		{Name: "Furnishings", KnownCategoryID: "4500100000"},
		{Name: "HOA Dues", KnownCategoryID: "4500200000"},
		{Name: "Home Improvement", KnownCategoryID: "4500300000"},
		{Name: "Home Insurance", KnownCategoryID: "4500400000"},
		{Name: "Home Services", KnownCategoryID: "4500500000"},
		{Name: "Home Supplies", KnownCategoryID: "4500600000"},
		{Name: "Mortgage", KnownCategoryID: "4500800000"},
		{Name: "Tools"},
	}},
	{Name: "Kids", Kind: CategoryExpense, KnownCategoryID: "5000000000", Children: []DefaultCategory{
		{Name: "Toys", KnownCategoryID: "5000900000"},
	}},
	{Name: "Loans", Kind: CategoryExpense, KnownCategoryID: "5250000000", Children: []DefaultCategory{
		{Name: "Loan Payment", KnownCategoryID: "5250525000"},
	}},
	{Name: "Personal Care", Kind: CategoryExpense, KnownCategoryID: "5500000000", Children: []DefaultCategory{
		{Name: "Hair", KnownCategoryID: "5500020000"},
		{Name: "Laundry", KnownCategoryID: "5500040000"},
		{Name: "Spa", KnownCategoryID: "5500080000"},
		{Name: "Body Care"},
	}},
	{Name: "Personal Income", Kind: CategoryIncome, KnownCategoryID: "5750000000", TxfID: "USA_257", Children: []DefaultCategory{
		{Name: "Bonus", KnownCategoryID: "5750200000", TxfID: "USA_460"},
		{Name: "Dividend Income", KnownCategoryID: "5750400000", TxfID: "USA_286"},
		{Name: "Interest Earned", KnownCategoryID: "5750600000", TxfID: "USA_287"},
		{Name: "Paycheck", KnownCategoryID: "5750700000", TxfID: "USA_460"},
		{Name: "Tax Refund", KnownCategoryID: "5750900000", TxfID: "USA_260"},
		{Name: "Side Work"},
		{Name: "Credit Card Reward"},
		{Name: "Gift Income"},
		{Name: "Sold Items"},
		{Name: "Cash Back"},
		{Name: "Other Income", TxfID: "USA_257"},
	}},
	{Name: "Pets", Kind: CategoryExpense, KnownCategoryID: "6000000000", Children: []DefaultCategory{
		{Name: "Pet Food & Supplies", KnownCategoryID: "6000025000"},
		{Name: "Pet Grooming", KnownCategoryID: "6000050000"},
		{Name: "Veterinary", KnownCategoryID: "6000075000"},
		{Name: "Toys"},
	}},
	{Name: "Shopping", Kind: CategoryExpense, KnownCategoryID: "6750000000", Children: []DefaultCategory{
		{Name: "Books", KnownCategoryID: "6750175000"},
		{Name: "Clothing", KnownCategoryID: "6750350000"},
		{Name: "Electronics", KnownCategoryID: "2750150000"},
		{Name: "In-App Purchases"},
	}},
	{Name: "Taxes", Kind: CategoryExpense, KnownCategoryID: "7000000000", Children: []DefaultCategory{
		{Name: "Federal Tax", KnownCategoryID: "7000100000", TxfID: "USA_461"},
		{Name: "Property Tax", KnownCategoryID: "7000400000", TxfID: "USA_276"},
		{Name: "State Tax", KnownCategoryID: "7000800000", TxfID: "USA_464"},
		{Name: "Federal Estimated Tax Payment", KnownCategoryID: "7000050000", TxfID: "USA_521", NotEditable: true},
	}},
	{Name: "Transfer", Kind: CategoryTransfer, KnownCategoryID: KnownCategoryTransfer,
		ExcludedFromReports: true, ExcludedFromSpendingPlan: true, Children: []DefaultCategory{
			{Name: "Credit Card Payment", KnownCategoryID: KnownCategoryCreditCardPayment},
		}},
	{Name: "Travel", Kind: CategoryExpense, KnownCategoryID: "1250750000", Children: []DefaultCategory{
		{Name: "Airfare", KnownCategoryID: "7250250000"},
		{Name: "Hotel", KnownCategoryID: "7250500000"},
		{Name: "Transportation", KnownCategoryID: "7250750000"},
	}},
	{Name: "Utilities", Kind: CategoryExpense, KnownCategoryID: "7500000000", Children: []DefaultCategory{
		{Name: "Gas & Electric", KnownCategoryID: "7500125000"},
		{Name: "Internet & Cable", KnownCategoryID: "7500375000"},
		{Name: "Phone", KnownCategoryID: "7500500000"},
		{Name: "Trash", KnownCategoryID: "7500625000"},
		{Name: "Water", KnownCategoryID: "7500875000"},
	}},
	{Name: "Work Expenses", Kind: CategoryExpense, KnownCategoryID: "7750000000"},
	{Name: "Shipping", Kind: CategoryExpense, KnownCategoryID: "1250700000"},
	{Name: "Membership Fees", Kind: CategoryExpense},
}
