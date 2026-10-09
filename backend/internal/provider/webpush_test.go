package provider

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	webpush "github.com/SherClockHolmes/webpush-go"
	"github.com/stretchr/testify/require"
)

func generateVapidKeys() (VapidKeys, error) {
	private, public, err := webpush.GenerateVAPIDKeys()
	return VapidKeys{PrivateKey: private, PublicKey: public}, err
}

// browserKeys stands in for a browser's subscription keypair.
type browserKeys struct {
	private *ecdh.PrivateKey
	p256dh  string
	auth    []byte
	authB64 string
}

func newBrowserKeys(t *testing.T) browserKeys {
	t.Helper()
	private, err := ecdh.P256().GenerateKey(rand.Reader)
	require.NoError(t, err)
	auth := make([]byte, 16)
	_, err = rand.Read(auth)
	require.NoError(t, err)
	return browserKeys{
		private: private,
		p256dh:  base64.RawURLEncoding.EncodeToString(private.PublicKey().Bytes()),
		auth:    auth,
		authB64: base64.RawURLEncoding.EncodeToString(auth),
	}
}

// decryptAsBrowser decrypts per RFC 8291/8188 with a keypair the test made,
// the only proof the library's bytes are readable.
func decryptAsBrowser(t *testing.T, keys browserKeys, body []byte) []byte {
	t.Helper()
	require.Greater(t, len(body), 86)

	salt := body[:16]
	recordSize := binary.BigEndian.Uint32(body[16:20])
	idLen := int(body[20])
	require.Equal(t, 65, idLen, "the server key must be a raw uncompressed point")
	serverPublicRaw := body[21 : 21+idLen]
	ciphertext := body[21+idLen:]
	require.LessOrEqual(t, len(ciphertext), int(recordSize))

	serverPublic, err := ecdh.P256().NewPublicKey(serverPublicRaw)
	require.NoError(t, err)
	shared, err := keys.private.ECDH(serverPublic)
	require.NoError(t, err)

	info := append([]byte("WebPush: info\x00"), keys.private.PublicKey().Bytes()...)
	info = append(info, serverPublicRaw...)
	ikm, err := hkdf.Key(sha256.New, shared, keys.auth, string(info), 32)
	require.NoError(t, err)

	contentKey, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: aes128gcm\x00", 16)
	require.NoError(t, err)
	nonce, err := hkdf.Key(sha256.New, ikm, salt, "Content-Encoding: nonce\x00", 12)
	require.NoError(t, err)

	block, err := aes.NewCipher(contentKey)
	require.NoError(t, err)
	gcm, err := cipher.NewGCM(block)
	require.NoError(t, err)
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	require.NoError(t, err)

	// A single record is padded out to the record size; the last non-zero byte
	// is the RFC 8188 final-record delimiter.
	plaintext = bytes.TrimRight(plaintext, "\x00")
	require.Equal(t, byte(0x02), plaintext[len(plaintext)-1])
	return plaintext[:len(plaintext)-1]
}

func pushToTestServer(t *testing.T, status int, payload any, sub PushSubscription) (body []byte, headers http.Header, sendErr error) {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ = io.ReadAll(r.Body)
		headers = r.Header.Clone()
		w.WriteHeader(status)
	}))
	defer server.Close()

	keys, err := generateVapidKeys()
	require.NoError(t, err)
	push := &Push{
		Keys:       keys,
		Subject:    "mailto:admin@example.com",
		HTTPClient: server.Client(),
		// The real guard rightly rejects a loopback HTTP server.
		endpointGuard: func(context.Context, string) error { return nil },
	}
	sub.Endpoint = server.URL + sub.Endpoint
	return body, headers, push.Send(context.Background(), sub, payload)
}

func TestAPayloadDecryptsTheWayABrowserWould(t *testing.T) {
	keys := newBrowserKeys(t)
	payload := map[string]string{"title": "Over budget", "body": "Groceries is 12.00 over."}

	body, headers, err := pushToTestServer(t, http.StatusCreated, payload,
		PushSubscription{Endpoint: "/wpush/v2/abc", P256dh: keys.p256dh, Auth: keys.authB64})
	require.NoError(t, err)
	require.Equal(t, "aes128gcm", headers.Get("Content-Encoding"))
	require.Equal(t, "application/octet-stream", headers.Get("Content-Type"))
	require.Equal(t, "86400", headers.Get("TTL"))

	var decoded map[string]string
	require.NoError(t, json.Unmarshal(decryptAsBrowser(t, keys, body), &decoded))
	require.Equal(t, payload, decoded)
}

func TestEachMessageUsesAFreshSaltAndEphemeralKey(t *testing.T) {
	keys := newBrowserKeys(t)
	sub := PushSubscription{Endpoint: "/wpush/v2/abc", P256dh: keys.p256dh, Auth: keys.authB64}

	first, _, err := pushToTestServer(t, http.StatusCreated, map[string]string{}, sub)
	require.NoError(t, err)
	second, _, err := pushToTestServer(t, http.StatusCreated, map[string]string{}, sub)
	require.NoError(t, err)

	require.NotEqual(t, first[:16], second[:16], "salt was reused")
	require.NotEqual(t, first[21:86], second[21:86], "ephemeral key was reused")
}

func TestTheVapidHeaderIsScopedToThePushService(t *testing.T) {
	keys := newBrowserKeys(t)
	_, headers, err := pushToTestServer(t, http.StatusCreated, map[string]string{},
		PushSubscription{Endpoint: "/wpush/v2/abc", P256dh: keys.p256dh, Auth: keys.authB64})
	require.NoError(t, err)

	authorization := headers.Get("Authorization")
	require.True(t, strings.HasPrefix(authorization, "vapid t="))

	token := strings.SplitN(strings.TrimPrefix(authorization, "vapid t="), ",", 2)[0]
	segments := strings.Split(token, ".")
	require.Len(t, segments, 3)
	raw, err := base64.RawURLEncoding.DecodeString(segments[1])
	require.NoError(t, err)

	var claims struct {
		Aud string `json:"aud"`
		Sub string `json:"sub"`
	}
	require.NoError(t, json.Unmarshal(raw, &claims))
	// The audience is the endpoint's origin, so a token is not replayable
	// against another push service.
	require.NotContains(t, claims.Aud, "/wpush")
	require.True(t, strings.HasPrefix(claims.Aud, "http://127.0.0.1:"))
	require.Equal(t, "mailto:admin@example.com", claims.Sub)
}

func TestADeadSubscriptionIsReportedSoTheCallerPrunesIt(t *testing.T) {
	keys := newBrowserKeys(t)
	for _, status := range []int{http.StatusNotFound, http.StatusGone} {
		_, _, err := pushToTestServer(t, status, map[string]string{},
			PushSubscription{Endpoint: "/wpush/v2/abc", P256dh: keys.p256dh, Auth: keys.authB64})
		require.ErrorIs(t, err, ErrPushSubscriptionGone)
	}
}

func TestAPushServiceFailureIsNotMistakenForADeadSubscription(t *testing.T) {
	keys := newBrowserKeys(t)
	_, _, err := pushToTestServer(t, http.StatusInternalServerError, map[string]string{},
		PushSubscription{Endpoint: "/wpush/v2/abc", P256dh: keys.p256dh, Auth: keys.authB64})
	require.Error(t, err)
	require.NotErrorIs(t, err, ErrPushSubscriptionGone)
}

func TestThePublicKeyIsTheRawPointABrowserExpects(t *testing.T) {
	keys, err := generateVapidKeys()
	require.NoError(t, err)
	raw, err := base64.RawURLEncoding.DecodeString(keys.PublicKey)
	require.NoError(t, err)
	require.Len(t, raw, 65)
}

func TestSendRefusesALoopbackEndpoint(t *testing.T) {
	keys := newBrowserKeys(t)
	push := &Push{Subject: "mailto:a@b.c"}

	err := push.Send(context.Background(),
		PushSubscription{Endpoint: "https://127.0.0.1/push", P256dh: keys.p256dh, Auth: keys.authB64},
		map[string]string{"title": "x"})
	require.ErrorIs(t, err, ErrPushEndpointRejected)
}

func TestSendRefusesAPlainHTTPEndpoint(t *testing.T) {
	keys := newBrowserKeys(t)
	push := &Push{Subject: "mailto:a@b.c"}

	err := push.Send(context.Background(),
		PushSubscription{Endpoint: "http://updates.push.services.example/wpush", P256dh: keys.p256dh, Auth: keys.authB64},
		map[string]string{"title": "x"})
	require.ErrorIs(t, err, ErrPushEndpointRejected)
	require.Contains(t, err.Error(), "https")
}

func TestAnEndpointWithNoHostIsRefused(t *testing.T) {
	require.ErrorIs(t,
		AssertEndpointAllowed(context.Background(), "https:///push", nil),
		ErrPushEndpointRejected)
}

func TestTheReservedRangesThatLookRoutableAreStillRefused(t *testing.T) {
	for _, addr := range []string{
		"127.0.0.1", "10.1.2.3", "192.168.1.1", "172.16.0.1", "169.254.169.254",
		"100.64.0.1", // CGNAT, inside a hosting provider's own network
		"198.18.0.1", // benchmarking
		"::1", "fc00::1", "fe80::1", "::ffff:127.0.0.1",
	} {
		require.False(t, isPubliclyRoutable(netip.MustParseAddr(addr)), "%s was allowed", addr)
	}
	for _, addr := range []string{"8.8.8.8", "34.107.221.82", "2606:4700:4700::1111"} {
		require.True(t, isPubliclyRoutable(netip.MustParseAddr(addr)), "%s was refused", addr)
	}
}

func TestAPushIsSignedWithTheGeneratedKey(t *testing.T) {
	private, public, err := GenerateVapidKeys()
	require.NoError(t, err)

	var authorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authorization = r.Header.Get("Authorization")
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	browser := newBrowserKeys(t)
	push := &Push{
		Keys:          VapidKeys{PrivateKey: private, PublicKey: public},
		Subject:       "https://money.example.test",
		HTTPClient:    server.Client(),
		endpointGuard: func(context.Context, string) error { return nil },
	}
	require.NoError(t, push.Send(context.Background(),
		PushSubscription{Endpoint: server.URL + "/wpush/v2/abc", P256dh: browser.p256dh, Auth: browser.authB64},
		map[string]string{"title": "hello"}))

	require.Contains(t, authorization, "k="+public)
	token := strings.SplitN(strings.TrimPrefix(authorization, "vapid t="), ",", 2)[0]
	segments := strings.Split(token, ".")
	require.Len(t, segments, 3)

	raw, err := base64.RawURLEncoding.DecodeString(public)
	require.NoError(t, err)
	require.Len(t, raw, 65)
	key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(raw[1:33]), Y: new(big.Int).SetBytes(raw[33:])}
	signature, err := base64.RawURLEncoding.DecodeString(segments[2])
	require.NoError(t, err)
	require.Len(t, signature, 64)
	digest := sha256.Sum256([]byte(segments[0] + "." + segments[1]))
	require.True(t, ecdsa.Verify(key, digest[:],
		new(big.Int).SetBytes(signature[:32]), new(big.Int).SetBytes(signature[32:])))

	claims, err := base64.RawURLEncoding.DecodeString(segments[1])
	require.NoError(t, err)
	require.Contains(t, string(claims), `"sub":"https://money.example.test"`)
}

func TestAnUnreachablePushServiceIsNamedByHostOnly(t *testing.T) {
	keys := newBrowserKeys(t)
	server := httptest.NewServer(http.NotFoundHandler())
	endpoint := server.URL + "/wpush/v2/secret-capability"
	server.Close()

	vapid, err := generateVapidKeys()
	require.NoError(t, err)
	push := &Push{
		Keys:          vapid,
		Subject:       "mailto:admin@example.com",
		HTTPClient:    &http.Client{},
		endpointGuard: func(context.Context, string) error { return nil },
	}
	err = push.Send(context.Background(), PushSubscription{Endpoint: endpoint, P256dh: keys.p256dh, Auth: keys.authB64}, map[string]string{})
	require.Error(t, err)
	require.NotContains(t, err.Error(), "secret-capability")
	require.Contains(t, err.Error(), strings.TrimPrefix(server.URL, "http://"))
}
