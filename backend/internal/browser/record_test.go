package browser

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestARecordingKeepsOnlyTheResponsesAtItsAddressInOrder(t *testing.T) {
	page := &StubPage{}
	recorded := RecordResponses(page, regexp.MustCompile(`/api/detail$`))

	page.Respond(StubResponse{Address: "https://api.example.test/api/other", Code: 200})
	page.Respond(StubResponse{Address: "https://api.example.test/api/detail", Code: 403})
	page.Respond(StubResponse{Address: "https://api.example.test/api/detail", Code: 200})

	responses := recorded.Responses()
	require.Len(t, responses, 2)
	require.Equal(t, 403, responses[0].Status())
	require.Equal(t, 200, responses[1].Status())
}

func TestAwaitAnswersARecordedResponseWithoutWaiting(t *testing.T) {
	page := &StubPage{}
	recorded := RecordResponses(page, regexp.MustCompile(`/detail`))
	page.Respond(StubResponse{Address: "https://example.test/detail", Code: 200, Payload: []byte(`{}`)})

	response, ok := recorded.Await(page, time.Second)
	require.True(t, ok)
	require.Equal(t, 200, response.Status())
	require.Zero(t, page.Slept)
}

func TestAwaitGivesUpAtItsDeadline(t *testing.T) {
	page := &StubPage{}
	recorded := RecordResponses(page, regexp.MustCompile(`/detail`))

	_, ok := recorded.Await(page, 2*time.Second)
	require.False(t, ok)
	require.Equal(t, 2*time.Second, page.Slept)
}

func TestAwaitWhereAnswersTheFirstResponseHoldingWhatIsWanted(t *testing.T) {
	page := &StubPage{}
	recorded := RecordResponses(page, regexp.MustCompile(`/graphql$`))
	for _, body := range []string{`{"a":1}`, `{"b":1}`, `{"b":2}`} {
		page.Respond(StubResponse{Address: "https://api.example.test/graphql", Code: 200, Payload: []byte(body)})
	}

	var offered []string
	response, ok := recorded.AwaitWhere(page, time.Second, func(response Response) bool {
		body, err := response.Body()
		require.NoError(t, err)
		offered = append(offered, string(body))
		return strings.HasPrefix(string(body), `{"b"`)
	})
	require.True(t, ok)
	body, err := response.Body()
	require.NoError(t, err)
	require.Equal(t, `{"b":1}`, string(body))
	require.Equal(t, []string{`{"a":1}`, `{"b":1}`}, offered)
	require.Zero(t, page.Slept)
}

func TestAwaitWhereOffersEachResponseOnceAndGivesUpAtItsDeadline(t *testing.T) {
	page := &StubPage{}
	recorded := RecordResponses(page, regexp.MustCompile(`/graphql$`))
	page.Respond(StubResponse{Address: "https://api.example.test/graphql", Code: 200, Payload: []byte(`{}`)})

	offers := 0
	_, ok := recorded.AwaitWhere(page, 2*time.Second, func(Response) bool {
		offers++
		return false
	})
	require.False(t, ok)
	require.Equal(t, 2*time.Second, page.Slept)
	require.Equal(t, 1, offers)
}
