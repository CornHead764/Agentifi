package provider

import (
	"context"
	"net"
	"strconv"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
	"github.com/stretchr/testify/require"
)

// A server that takes the connection and never answers is a half-open socket:
// the call ends with its context rather than blocking the mailbox pass.
func TestAnIMAPServerThatNeverAnswersDoesNotOutliveTheCall(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	defer listener.Close()
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
		}
	}()
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	portNumber, _ := strconv.Atoi(port)

	mailbox := &IMAPMailbox{
		Host: host, Port: portNumber, Username: "someone", Password: "invented",
		Dial: imapclient.DialInsecure,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	started := time.Now()
	require.Error(t, mailbox.Verify(ctx))
	require.Less(t, time.Since(started), 5*time.Second)
}
