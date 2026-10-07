package backup

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io"
	"strings"
	"testing"

	"filippo.io/age"
	"github.com/stretchr/testify/require"
	"golang.org/x/crypto/ssh"
)

// An invented key made for these tests by ssh-keygen, whose fingerprint is
// the one `ssh-keygen -lf` printed for it.
const (
	referenceSSHKey         = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIHtfBk58AasAD2LR2xeQ/qM6+2BhtSPUDwJP25s93+Bw"
	referenceSSHComment     = "backup-test@example.invalid"
	referenceSSHFingerprint = "SHA256:zeupREKRB/xQzRoKxYO2h+MbTWn/XoCwc0PDgie88Zk"
)

func TestAnSSHPublicKeyIsARecipient(t *testing.T) {
	recipient, err := ParseRecipient("  " + referenceSSHKey + " " + referenceSSHComment + "\n")
	require.NoError(t, err)
	require.Equal(t, "ssh-ed25519", recipient.Kind)
	require.Equal(t, referenceSSHKey, recipient.Key, "the comment is not part of the stored key")
	require.Equal(t, referenceSSHComment, recipient.Comment)
	require.Equal(t, referenceSSHFingerprint, recipient.Fingerprint)

	age, err := ParseRecipient(referenceRecipient)
	require.NoError(t, err)
	require.Equal(t, "age", age.Kind)
	require.Equal(t, referenceRecipient, age.Key)
	require.Empty(t, age.Fingerprint)
}

func sshPublicLine(t *testing.T, key crypto.PublicKey) string {
	t.Helper()
	public, err := ssh.NewPublicKey(key)
	require.NoError(t, err)
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(public)))
}

func TestOnlyEd25519AndLargeEnoughRSAKeysAreRecipients(t *testing.T) {
	small, err := rsa.GenerateKey(rand.Reader, 1024)
	require.NoError(t, err)
	_, err = ParseRecipient(sshPublicLine(t, &small.PublicKey))
	require.ErrorContains(t, err, "too small", "age refuses RSA under 2048 bits")

	curve, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	_, err = ParseRecipient(sshPublicLine(t, &curve.PublicKey))
	require.ErrorContains(t, err, "ecdsa-sha2-nistp256")

	for _, bad := range []string{
		"ssh-ed25519",
		"ssh-ed25519 not-base64",
		"ssh-rsa " + strings.Fields(referenceSSHKey)[1],
		referenceSSHKey + "\n" + referenceSSHKey,
	} {
		_, err := ParseRecipient(bad)
		require.Error(t, err, bad)
	}
}

type sshPair struct {
	name    string
	public  crypto.PublicKey
	private crypto.PrivateKey
}

func sshPairs(t *testing.T) []sshPair {
	t.Helper()
	edPublic, edPrivate, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	rsaPrivate, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return []sshPair{
		{name: "ed25519", public: edPublic, private: edPrivate},
		{name: "rsa", public: &rsaPrivate.PublicKey, private: rsaPrivate},
	}
}

func sealTo(t *testing.T, plaintext string, keys ...string) []byte {
	t.Helper()
	recipients, recorded, err := parseRecipients(keys)
	require.NoError(t, err)
	require.Len(t, recorded, len(keys))
	var sealed bytes.Buffer
	w, err := seal(&sealed, recipients)
	require.NoError(t, err)
	_, err = io.WriteString(w, plaintext)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	return sealed.Bytes()
}

func openWith(t *testing.T, sealed []byte, identities []age.Identity) (string, error) {
	t.Helper()
	plain, err := open(bytes.NewReader(sealed), true, identities)
	if err != nil {
		return "", err
	}
	out, err := io.ReadAll(plain)
	return string(out), err
}

func TestASetSealedToAnSSHKeyOpensWithItsPrivateKey(t *testing.T) {
	for _, pair := range sshPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			sealed := sealTo(t, "an invented dump", sshPublicLine(t, pair.public)+" someone@example.invalid")

			block, err := ssh.MarshalPrivateKey(pair.private, "")
			require.NoError(t, err)
			identities, err := ReadIdentities(bytes.NewReader(pem.EncodeToMemory(block)), nil)
			require.NoError(t, err, "an unencrypted key asks for no passphrase")
			got, err := openWith(t, sealed, identities)
			require.NoError(t, err)
			require.Equal(t, "an invented dump", got)

			other, err := ReadIdentities(strings.NewReader(referenceIdentity), nil)
			require.NoError(t, err)
			_, err = openWith(t, sealed, other)
			require.ErrorIs(t, err, ErrWrongIdentity)
		})
	}
}

func TestAPassphraseProtectedSSHKeyOpensWithItsPassphrase(t *testing.T) {
	for _, pair := range sshPairs(t) {
		t.Run(pair.name, func(t *testing.T) {
			sealed := sealTo(t, "an invented dump", sshPublicLine(t, pair.public))
			block, err := ssh.MarshalPrivateKeyWithPassphrase(pair.private, "", []byte("correct horse"))
			require.NoError(t, err)
			key := pem.EncodeToMemory(block)
			given := func(secret string) func() ([]byte, error) {
				return func() ([]byte, error) { return []byte(secret), nil }
			}

			_, err = ReadIdentities(bytes.NewReader(key), nil)
			require.ErrorIs(t, err, ErrPassphraseRequired)
			_, err = ReadIdentities(bytes.NewReader(key), given(""))
			require.ErrorIs(t, err, ErrPassphraseRequired)
			_, err = ReadIdentities(bytes.NewReader(key), given("wrong horse"))
			require.ErrorIs(t, err, ErrWrongPassphrase)

			identities, err := ReadIdentities(bytes.NewReader(key), given("correct horse"))
			require.NoError(t, err)
			got, err := openWith(t, sealed, identities)
			require.NoError(t, err)
			require.Equal(t, "an invented dump", got)
		})
	}
}

func TestAnUnencryptedKeyNeverAsksForAPassphrase(t *testing.T) {
	_, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(private, "")
	require.NoError(t, err)
	asked := false
	_, err = ReadIdentities(bytes.NewReader(pem.EncodeToMemory(block)), func() ([]byte, error) {
		asked = true
		return nil, nil
	})
	require.NoError(t, err)
	require.False(t, asked)
}

func TestAPKCS1RSAKeyIsAnIdentity(t *testing.T) {
	private, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	sealed := sealTo(t, "an invented dump", sshPublicLine(t, &private.PublicKey))
	key := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(private)})
	identities, err := ReadIdentities(bytes.NewReader(key), nil)
	require.NoError(t, err)
	got, err := openWith(t, sealed, identities)
	require.NoError(t, err)
	require.Equal(t, "an invented dump", got)
}

func TestAKeyPastedOnOneLineStillOpens(t *testing.T) {
	public, private, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)
	sealed := sealTo(t, "an invented dump", sshPublicLine(t, public))
	block, err := ssh.MarshalPrivateKey(private, "")
	require.NoError(t, err)
	armoured := string(pem.EncodeToMemory(block))

	for name, flattened := range map[string]string{
		"newlines dropped":   strings.ReplaceAll(armoured, "\n", ""),
		"newlines as spaces": strings.ReplaceAll(armoured, "\n", " "),
	} {
		t.Run(name, func(t *testing.T) {
			require.NotContains(t, flattened, "\n")
			identities, err := ReadIdentities(strings.NewReader(flattened), nil)
			require.NoError(t, err)
			got, err := openWith(t, sealed, identities)
			require.NoError(t, err)
			require.Equal(t, "an invented dump", got)
		})
	}
	require.Equal(t, armoured, string(repairPEM([]byte(armoured))), "a key with its line breaks is left alone")
}

func TestAnUnsupportedPrivateKeyIsRefused(t *testing.T) {
	curve, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)
	block, err := ssh.MarshalPrivateKey(curve, "")
	require.NoError(t, err)
	_, err = ReadIdentities(bytes.NewReader(pem.EncodeToMemory(block)), nil)
	require.ErrorContains(t, err, "ed25519 or RSA")

	garbage := pem.EncodeToMemory(&pem.Block{Type: "OPENSSH PRIVATE KEY", Bytes: []byte("not a key")})
	_, err = ReadIdentities(bytes.NewReader(garbage), nil)
	require.Error(t, err)
}
