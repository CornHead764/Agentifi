package browser

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// A session made in another browser reaches this one through IndexedDB.
//
// The shape is Firebase Auth's: a `firebaseLocalStorageDb` whose one store
// keys its records in line on `fbase_key`, and whose one record is the
// signed-in user. Nothing else in a Firebase portal's storage says who is
// signed in, so a restore that wrote local storage and dropped this would
// hand the profile a session the app cannot see. The second database keys
// out of line, which is the other shape Playwright writes.
//
// Every value here is invented.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/browser/
func TestSeedProfileRestoresIndexedDB(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><title>App</title><body>app</body>`)
	}))
	t.Cleanup(site.Close)

	var state StorageState
	require.NoError(t, json.Unmarshal([]byte(`{
	  "cookies": [],
	  "origins": [{
	    "origin": "`+site.URL+`",
	    "localStorage": [{"name": "idle.expiry", "value": "1"}],
	    "indexedDB": [
	      {"name": "firebaseLocalStorageDb", "version": 1, "stores": [{
	        "name": "firebaseLocalStorage", "keyPath": "fbase_key", "autoIncrement": false, "indexes": [],
	        "records": [{"value": {"fbase_key": "firebase:authUser:test-key:[DEFAULT]",
	          "value": {"uid": "u-1", "stsTokenManager": {"refreshToken": "invented-refresh"}}}}]
	      }]},
	      {"name": "out-of-line", "version": 2, "stores": [{
	        "name": "things", "autoIncrement": false, "indexes": [],
	        "records": [{"key": "one", "value": {"n": 1}}, {"keyEncoded": {"d": 1}, "valueEncoded": {"d": 2}}]
	      }]}
	    ]
	  }]
	}`), &state))

	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	context, err := engine.NewContext("", DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })

	var notes []string
	SeedProfile(context, state, func(line string) { notes = append(notes, line) })
	require.Contains(t, notes, site.URL+": restored 2 IndexedDB record(s), left out 1")

	opened, err := OpenPage(context)
	require.NoError(t, err)
	page := Wrap(opened)
	require.NoError(t, page.Goto(site.URL))
	read, err := page.Evaluate(`async () => {
	  const get = (db, store, key) => new Promise((resolve, reject) => {
	    const open = indexedDB.open(db);
	    open.onsuccess = () => {
	      const request = open.result.transaction(store).objectStore(store).get(key);
	      request.onsuccess = () => resolve(request.result);
	      request.onerror = () => reject(request.error);
	    };
	    open.onerror = () => reject(open.error);
	  });
	  const user = await get('firebaseLocalStorageDb', 'firebaseLocalStorage', 'firebase:authUser:test-key:[DEFAULT]');
	  const thing = await get('out-of-line', 'things', 'one');
	  return [user.value.stsTokenManager.refreshToken, thing.n, localStorage.getItem('idle.expiry')];
	}`, nil)
	require.NoError(t, err)
	require.Equal(t, []any{"invented-refresh", 1, "1"}, read)
}
