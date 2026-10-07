package browser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type countedSession struct{ shut int }

func (s *countedSession) Shut() { s.shut++ }

func TestEveryWayASessionLeavesShutsIt(t *testing.T) {
	start := time.Now()
	var sessions Sessions[*countedSession]
	closed, reaped, busy, left := &countedSession{}, &countedSession{}, &countedSession{}, &countedSession{}
	sessions.Add("closed", closed, start)
	sessions.Add("reaped", reaped, start)
	sessions.Add("busy", busy, start)
	sessions.Add("left", left, start)

	sessions.Close("closed", closed)
	require.Equal(t, 1, closed.shut)
	_, found := sessions.Find("closed", start)
	require.False(t, found)

	later := start.Add(SessionTTL + time.Minute)
	sessions.Find("left", later)
	sessions.Reap(later, func(s *countedSession, _ time.Time) bool { return s == busy })
	require.Equal(t, 1, reaped.shut)
	require.Zero(t, busy.shut)
	require.Zero(t, left.shut)
	require.Equal(t, 2, sessions.Len())

	sessions.CloseAll()
	require.Equal(t, 1, busy.shut)
	require.Equal(t, 1, left.shut)
	require.Zero(t, sessions.Len())
}
