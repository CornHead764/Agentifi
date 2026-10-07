package billers

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestADayPrintedWithItsTimeIsStillThatDay(t *testing.T) {
	require.Equal(t, "2025-12-31", ISODate("12/31/2025 12:00:00 AM"))
	require.Equal(t, "2026-03-04", ISODate("3/4/2026 4:05 PM"))
	require.Equal(t, "2026-03-04", ISODate("03-04-2026 16:05"))
	require.Equal(t, "2026-03-04", ISODate("3/4/2026"))
}

func TestADayFollowedByAnythingButATimeIsNotADay(t *testing.T) {
	require.Equal(t, "", ISODate("3/4/2026 due"))
	require.Equal(t, "", ISODate("3/4/20261"))
}
