package pgimport

import (
	"fmt"

	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/moneycolumns"
)

func decimals(code string) (int, error)           { return minorunits.Decimals(code) }
func minor(text string, scale int) (int64, error) { return minorunits.Minor(text, scale) }
func (s *source) denomination(table, col string, r row, primary string) (string, error) {
	d, ok := moneycolumns.Columns[table+"."+col]
	if !ok {
		return "", fmt.Errorf("unmapped money column %s.%s", table, col)
	}
	switch {
	case d.Own:
		return val(r, "currency"), nil
	case d.Primary:
		return primary, nil
	case d.Fixed != "":
		return d.Fixed, nil
	}
	p := s.byID[d.Parent][val(r, d.Via)]
	if p == nil {
		return "", fmt.Errorf("missing currency parent %s:%s for %s.%s id %s", d.Parent, d.Via, table, col, val(r, "id"))
	}
	return val(p, "currency"), nil
}
