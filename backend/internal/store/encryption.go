package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Encryption for credential columns: the SimpleFIN Access URL (Basic auth to
// every linked bank), bill and merchant connector sessions and passwords, a
// mailbox's secret, the assistant's API key, TOTP seeds and secret server
// settings. Sealed on the way into Postgres so a dump, backup or replica lacks
// the credential.
//
// A sealed value belongs to one row: GCM additional data binds each payload to
// its row (connection id, space, setting key, user). It is not stored but
// rebuilt from the row, so a payload pasted into another row does not open.
//
// A failure to open is loud. An empty credential would send a sync with no
// auth, get a 401 and tell the user to reconnect a working connection while a
// wrong key went unnamed; Open returns an error naming the key, and every
// caller propagates it.
//
// The key is Config.CredentialKey, not SECRET_KEY; see internal/config.

// The scheme version every payload carries, and the label its key is derived
// under. Changing either makes every stored value unreadable, so a change needs
// a read path for the old one until every install has rewritten its rows.
const (
	cipherVersion  = "v3"
	columnKeyLabel = "agentifi:column-encryption:v2:"
)

// The contexts every sealed column is bound to, gathered in one place because
// no two may ever produce the same string — that is what stops a sealed value
// moving between rows, and a single call site cannot check it.
func connectionCredentialContext(spaceID SpaceID, id uuid.UUID) string {
	return "connection.access_url:" + spaceID.UUID().String() + ":" + id.String()
}

func assistantKeyContext(spaceID SpaceID) string {
	return "assistant_connection.api_key:" + spaceID.UUID().String()
}

func serverSettingContext(key string) string {
	return "server_setting.value:" + key
}

func totpSecretContext(userID uuid.UUID) string {
	return "user.totp_secret:" + userID.String()
}

// billConnectionSessionContext binds a provider session to its connection, so
// a session lifted onto another connection cannot pull statements under
// another household's login.
func billConnectionSessionContext(spaceID SpaceID, id uuid.UUID) string {
	return "bill_connection.session_state:" + spaceID.UUID().String() + ":" + id.String()
}

func billConnectionCredentialContext(spaceID SpaceID, id uuid.UUID) string {
	return "bill_connection.credential:" + spaceID.UUID().String() + ":" + id.String()
}

// emailConnectionContext binds a mailbox's one secret — a refresh token or an
// app password — to its connection.
func emailConnectionContext(spaceID SpaceID, id uuid.UUID) string {
	return "email_connection.secret:" + spaceID.UUID().String() + ":" + id.String()
}

func merchantSessionContext(spaceID SpaceID, id uuid.UUID) string {
	return "amazon-session:" + spaceID.UUID().String() + ":" + id.String()
}

func merchantCredentialContext(spaceID SpaceID, id uuid.UUID) string {
	return "merchant_account.credential:" + spaceID.UUID().String() + ":" + id.String()
}

// ErrCredentialUnreadable is a sealed value that will not open with this
// process's key — almost always a rotated or mis-set CREDENTIAL_ENCRYPTION_KEY.
var ErrCredentialUnreadable = errors.New(
	"store: the stored credential will not decrypt with this CREDENTIAL_ENCRYPTION_KEY")

// Cipher seals and opens credential columns. Safe for concurrent use.
type Cipher struct{ aead cipher.AEAD }

// NewCipher derives an AES-256-GCM cipher from a key of any length. The
// domain-separated hash frees the length and keeps the same secret used
// elsewhere from yielding these bytes.
func NewCipher(key string) (*Cipher, error) {
	if strings.TrimSpace(key) == "" {
		return nil, errors.New("store: the credential encryption key is empty")
	}
	aead, err := newAEAD(columnKeyLabel, key)
	if err != nil {
		return nil, err
	}
	return &Cipher{aead: aead}, nil
}

// newAEAD derives one AES-256-GCM cipher. The label is part of the key, so
// changing one makes every value sealed under it unreadable.
func newAEAD(label, key string) (cipher.AEAD, error) {
	derived := sha256.Sum256([]byte(label + key))
	block, err := aes.NewCipher(derived[:])
	if err != nil {
		return nil, fmt.Errorf("store: credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("store: credential cipher: %w", err)
	}
	return aead, nil
}

// Seal encrypts one value into a payload of the scheme version, a fresh random
// nonce and the ciphertext. The nonce must never be reused — GCM's
// confidentiality rests on it.
//
// context names the row the value belongs to; it is authenticated but not
// stored, so a payload copied into another row does not open. Pass one of the
// context functions above.
func (c *Cipher) Seal(context, plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("store: credential nonce: %w", err)
	}
	sealed := c.aead.Seal(nonce, nonce, []byte(plaintext), []byte(context))
	return cipherVersion + ":" + base64.StdEncoding.EncodeToString(sealed), nil
}

// Open decrypts a payload Seal produced, naming what is wrong when it cannot.
func (c *Cipher) Open(context, payload string) (string, error) {
	version, encoded, ok := strings.Cut(payload, ":")
	if !ok {
		return "", fmt.Errorf("store: the stored credential carries no scheme version")
	}
	if version != cipherVersion {
		return "", fmt.Errorf("store: the stored credential uses scheme %q, which this build cannot read", version)
	}
	aead := c.aead
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", fmt.Errorf("store: the stored credential is not valid base64: %w", err)
	}
	if len(raw) <= aead.NonceSize() {
		return "", fmt.Errorf("store: the stored credential is truncated")
	}
	plaintext, err := aead.Open(nil, raw[:aead.NonceSize()], raw[aead.NonceSize():], []byte(context))
	if err != nil {
		return "", ErrCredentialUnreadable
	}
	return string(plaintext), nil
}

// WithCipher returns a store whose credential columns are sealed and opened
// with c. A copy rather than a setter because a *Store is shared across
// goroutines.
func (s *Store) WithCipher(c *Cipher) *Store {
	clone := *s
	clone.cipher = c
	return &clone
}

// requireCipher refuses a credential query on a store that was never given a
// key, rather than writing a plaintext Access URL into the column.
func (s *Store) requireCipher() (*Cipher, error) {
	if s.cipher == nil {
		return nil, errors.New("store: this store has no credential cipher; call WithCipher")
	}
	return s.cipher, nil
}

// readSealed opens one sealed column of a space's row; ErrNotFound when the
// row is missing or the column holds nothing. table and column are spliced
// into the SQL, so they must be constants, never input.
func (s *Store) readSealed(
	ctx context.Context, what, table, column, purpose string, spaceID SpaceID, id uuid.UUID,
) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	var sealed *string
	err = s.db.QueryRow(ctx,
		`SELECT `+column+` FROM `+table+` WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id).Scan(&sealed)
	if err != nil {
		return "", wrap(what, err)
	}
	if sealed == nil || *sealed == "" {
		return "", wrap(what, ErrNotFound)
	}
	return cipher.Open(purpose, *sealed)
}

// openJSON is readSealed for a column sealed by sealJSON.
func openJSON[T any](
	ctx context.Context, s *Store, what, table, column, purpose string, spaceID SpaceID, id uuid.UUID,
) (T, error) {
	var value T
	plain, err := s.readSealed(ctx, what, table, column, purpose, spaceID, id)
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal([]byte(plain), &value); err != nil {
		var zero T
		return zero, wrap(what, err)
	}
	return value, nil
}

// sealString seals plain against purpose, the context its row is opened with.
func (s *Store) sealString(purpose, plain string) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	return cipher.Seal(purpose, plain)
}

func (s *Store) sealJSON(what, purpose string, v any) (string, error) {
	plain, err := json.Marshal(v)
	if err != nil {
		return "", wrap(what, err)
	}
	return s.sealString(purpose, string(plain))
}
