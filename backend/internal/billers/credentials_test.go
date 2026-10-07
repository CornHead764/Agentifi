package billers

import (
	"bytes"
	"fmt"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCredentialsNeverPrintTheirSecrets(t *testing.T) {
	creds := Credentials{
		Username: "someone@example.test", Password: "invented-password",
		Code: "123456", Secret: "JBSWY3DPEHPK3PXP",
	}
	held := struct{ Creds Credentials }{creds}

	var logged bytes.Buffer
	slog.New(slog.NewTextHandler(&logged, nil)).Info("signing in", "credentials", creds)

	for _, printed := range []string{
		fmt.Sprintf("%v", creds), fmt.Sprintf("%+v", creds), fmt.Sprintf("%#v", creds),
		fmt.Sprintf("%v", held), fmt.Sprintf("%+v", &held), logged.String(),
	} {
		for _, secret := range []string{"someone@example.test", "invented-password", "123456", "JBSWY3DPEHPK3PXP"} {
			require.NotContains(t, printed, secret)
		}
		require.Contains(t, printed, "[redacted]")
	}
	require.Contains(t, Credentials{}.String(), `Password:""`)
}
