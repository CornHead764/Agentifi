package api

import "strings"

// The Tax eXchange Format reference numbers behind the Taxes report. Codes like
// "USA_283" are the TXF spec's reference numbers. An unlisted code is rendered
// raw under "Other forms" so a mapping gap stays visible.
type txfLine struct{ Form, Item string }

var txfLines = map[string]txfLine{
	"257": {"Form 1040", "Other income, misc."},
	"260": {"Form 1040", "State and local tax refunds"},
	"273": {"Schedule A", "Medicine and drugs"},
	"276": {"Schedule A", "Real estate taxes"},
	"280": {"Schedule A", "Cash charity contributions"},
	"282": {"Schedule A", "Investment management fees"},
	"283": {"Schedule A", "Home mortgage interest"},
	"286": {"Schedule B", "Dividend income"},
	"287": {"Schedule B", "Interest income"},
	"401": {"Form 2441", "Qualifying child care expenses"},
	"460": {"W-2", "Salary or wages, self"},
	"461": {"W-2", "Federal withholding, self"},
	"462": {"W-2", "Social Security tax withholding, self"},
	"463": {"W-2", "Local withholding, self"},
	"464": {"W-2", "State withholding, self"},
	"476": {"1099-R", "Pension distribution"},
	"478": {"1099-R", "Taxable IRA distribution"},
	"480": {"W-2", "Medicare tax withholding, self"},
	"484": {"Schedule A", "Doctors, dentists, hospitals"},
	"521": {"Form 1040-ES", "Federal estimated tax, quarterly"},
	"535": {"Schedule A", "Personal property taxes"},
	"636": {"Form 1040", "Student loan interest"},
}

// txfLineFor resolves a category's stored code ("USA_283") to its form and line
// item. An unknown code comes back as itself under "Other forms".
func txfLineFor(code string) txfLine {
	number := strings.TrimPrefix(code, "USA_")
	if line, known := txfLines[number]; known {
		return line
	}
	return txfLine{Form: "Other forms", Item: code}
}
