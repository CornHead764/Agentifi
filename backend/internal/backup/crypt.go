package backup

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"golang.org/x/crypto/ssh"
)

// Every part of a set is sealed with age to the saved recipients. The host
// holds only the public half, so a copy of the backups directory, or of the
// whole machine, cannot be read without the identity kept elsewhere.
//
// A recipient is an age X25519 key or an SSH key (ssh-ed25519, or ssh-rsa of
// at least 2048 bits) through age's own SSH recipient types, so a key pair
// someone already keeps can open the sets. Each part stays one age file,
// which the age CLI opens with either kind of key.

// Recipient is one public key a set is sealed to.
type Recipient struct {
	age.Recipient
	// Key is the key as it is stored and recorded in a manifest: age1…, or
	// the SSH type and key without the comment.
	Key string
	// Kind is "age", "ssh-ed25519" or "ssh-rsa".
	Kind string
	// Comment is an SSH key's trailing comment, often user@host.
	Comment string
	// Fingerprint is an SSH key's SHA256:… as ssh-keygen -l prints it.
	Fingerprint string
}

// ParseRecipient reads one recipient: an age X25519 key (age1…) or an SSH
// public key line as authorized_keys holds it.
func ParseRecipient(text string) (Recipient, error) {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "ssh-") {
		return parseSSHRecipient(text)
	}
	recipient, err := age.ParseX25519Recipient(text)
	if err != nil {
		return Recipient{}, fmt.Errorf(
			"backup: %q is not an age public key (age1…) or an SSH public key (ssh-ed25519, ssh-rsa): %w", text, err)
	}
	return Recipient{Recipient: recipient, Key: recipient.String(), Kind: "age"}, nil
}

func parseSSHRecipient(text string) (Recipient, error) {
	key, comment, _, rest, err := ssh.ParseAuthorizedKey([]byte(text))
	if err != nil {
		return Recipient{}, fmt.Errorf("backup: %q is not an SSH public key: %w", text, err)
	}
	if len(bytes.TrimSpace(rest)) > 0 {
		return Recipient{}, errors.New("backup: one SSH public key per recipient")
	}
	var recipient age.Recipient
	switch key.Type() {
	case ssh.KeyAlgoED25519:
		recipient, err = agessh.NewEd25519Recipient(key)
	case ssh.KeyAlgoRSA:
		recipient, err = agessh.NewRSARecipient(key)
	default:
		return Recipient{}, fmt.Errorf("backup: an SSH key of type %s cannot be encrypted to; use ssh-ed25519 or ssh-rsa",
			key.Type())
	}
	if err != nil {
		return Recipient{}, fmt.Errorf("backup: %s key: %w", key.Type(), err)
	}
	return Recipient{
		Recipient:   recipient,
		Key:         strings.TrimSpace(string(ssh.MarshalAuthorizedKey(key))),
		Kind:        key.Type(),
		Comment:     comment,
		Fingerprint: ssh.FingerprintSHA256(key),
	}, nil
}

// parseRecipients returns the recipients and their keys as a manifest
// records them.
func parseRecipients(texts []string) ([]age.Recipient, []string, error) {
	out := make([]age.Recipient, 0, len(texts))
	keys := make([]string, 0, len(texts))
	for _, text := range texts {
		recipient, err := ParseRecipient(text)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, recipient)
		keys = append(keys, recipient.Key)
	}
	return out, keys, nil
}

// Identity is an age identity, the private half that opens a set.
type Identity = age.Identity

// ReadIdentities reads an identity: an age identity file, one
// AGE-SECRET-KEY-1… per line with # comments as age-keygen and the settings
// screen write it, or an SSH private key (ed25519 or RSA, OpenSSH or PEM).
// passphrase is called only for an encrypted SSH key; a nil one, or an empty
// passphrase, is ErrPassphraseRequired.
func ReadIdentities(r io.Reader, passphrase func() ([]byte, error)) ([]age.Identity, error) {
	text, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("backup: reading the identity: %w", err)
	}
	if bytes.Contains(text, []byte("-----BEGIN")) {
		identity, err := readSSHIdentity(repairPEM(text), passphrase)
		if err != nil {
			return nil, err
		}
		return []age.Identity{identity}, nil
	}
	identities, err := age.ParseIdentities(bytes.NewReader(text))
	if err != nil {
		return nil, fmt.Errorf("backup: the identity is not an age secret key (AGE-SECRET-KEY-1…) "+
			"or an SSH private key: %w", err)
	}
	return identities, nil
}

func readSSHIdentity(pemBytes []byte, passphrase func() ([]byte, error)) (age.Identity, error) {
	key, err := ssh.ParseRawPrivateKey(pemBytes)
	var missing *ssh.PassphraseMissingError
	if errors.As(err, &missing) {
		if passphrase == nil {
			return nil, ErrPassphraseRequired
		}
		secret, readErr := passphrase()
		if readErr != nil {
			return nil, fmt.Errorf("backup: reading the passphrase: %w", readErr)
		}
		if len(secret) == 0 {
			return nil, ErrPassphraseRequired
		}
		key, err = ssh.ParseRawPrivateKeyWithPassphrase(pemBytes, secret)
		if errors.Is(err, x509.IncorrectPasswordError) {
			return nil, ErrWrongPassphrase
		}
	}
	if err != nil {
		return nil, fmt.Errorf("backup: the SSH private key is unreadable: %w", err)
	}
	switch key := key.(type) {
	case *ed25519.PrivateKey:
		return agessh.NewEd25519Identity(*key)
	case ed25519.PrivateKey:
		return agessh.NewEd25519Identity(key)
	case *rsa.PrivateKey:
		return agessh.NewRSAIdentity(key)
	}
	return nil, fmt.Errorf("backup: an SSH key of type %T cannot open a set; use ed25519 or RSA", key)
}

var pemBlock = regexp.MustCompile(`(?s)(-----BEGIN [A-Z0-9 ]+-----)(.*?)(-----END [A-Z0-9 ]+-----)`)

// repairPEM puts back the line breaks of a private key pasted through a
// single-line field, such as a password input or a password manager's
// password, which keeps the armour and the base64 and drops the newlines. A
// key with its line breaks comes back as it was.
func repairPEM(text []byte) []byte {
	match := pemBlock.FindSubmatch(text)
	if match == nil || bytes.ContainsAny(match[2], "\r\n") {
		return text
	}
	body := strings.Join(strings.Fields(string(match[2])), "")
	var out bytes.Buffer
	out.Write(match[1])
	out.WriteByte('\n')
	for len(body) > 70 {
		out.WriteString(body[:70] + "\n")
		body = body[70:]
	}
	if body != "" {
		out.WriteString(body + "\n")
	}
	out.Write(match[3])
	out.WriteByte('\n')
	return out.Bytes()
}

// seal returns a writer that encrypts to the recipients, or passes through
// when there are none. Close must be called to finish the age stream.
func seal(w io.Writer, recipients []age.Recipient) (io.WriteCloser, error) {
	if len(recipients) == 0 {
		return nopCloser{w}, nil
	}
	return age.Encrypt(w, recipients...)
}

// open returns the plaintext of one part.
func open(r io.Reader, encrypted bool, identities []age.Identity) (io.Reader, error) {
	if !encrypted {
		return r, nil
	}
	if len(identities) == 0 {
		return nil, ErrIdentityRequired
	}
	plain, err := age.Decrypt(r, identities...)
	if err != nil {
		var mismatch *age.NoIdentityMatchError
		if errors.As(err, &mismatch) {
			return nil, ErrWrongIdentity
		}
		return nil, fmt.Errorf("backup: decrypting: %w", err)
	}
	return plain, nil
}

var (
	ErrIdentityRequired = errors.New("backup: this set is encrypted; an identity is required to read it")
	ErrWrongIdentity    = errors.New("backup: the identity given is not one this set was encrypted to")
	// ErrPassphraseRequired is an encrypted SSH private key read without its
	// passphrase.
	ErrPassphraseRequired = errors.New("backup: the SSH private key is protected by a passphrase")
	// ErrWrongPassphrase is an encrypted SSH private key the passphrase given
	// does not open.
	ErrWrongPassphrase = errors.New("backup: the passphrase does not open the SSH private key")
)

type nopCloser struct{ io.Writer }

func (nopCloser) Close() error { return nil }

// KeyID names the credential key a set's stored connections were sealed with,
// without revealing it, so a restore can tell before it starts whether those
// connections will open.
func KeyID(credentialKey string) string {
	sum := sha256.Sum256([]byte("agentifi:backup-key-id:" + credentialKey))
	return hex.EncodeToString(sum[:8])
}
