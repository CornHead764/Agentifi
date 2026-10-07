package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"strings"
)

// One call made by the page itself, with the page's cookies and whatever
// token the site's app left in storage: endpoints that accept only the token
// the signed-in app holds are called from inside the page, and the token
// never leaves the browser. Every caller that has a page make a request goes
// through this one script: the fetcher, the bill providers, the merchants and
// the developer steer.
//
// The script itself only reads what the page stores and never hooks what it
// sends: in Camoufox it runs in an isolated world, where a hook on the app's
// fetch patches a window the app never uses. A module running in Chrome
// installs its own init-script hook where it needs the bearer the app sends,
// ahead of a call this script then makes with it.
//
// The body is read as a data URL rather than walked as a typed array: in the
// isolated world a Uint8Array over the page's ArrayBuffer trips Firefox's
// cross-world wrappers ("Permission denied to access property constructor").
//
// The abort covers the body too. Evaluate has no timeout of its own, so a
// stalled socket would hold the page, the profile lock and the serial pass.

// PageRead is how a page call hands its body back.
type PageRead string

const (
	// ReadJSON parses the body, with its start as an excerpt for a note.
	ReadJSON PageRead = "json"
	// ReadText is the body as text, up to Limit characters.
	ReadText PageRead = "text"
	// ReadBytes is the body as base64, so bytes survive the boundary.
	ReadBytes PageRead = "bytes"
	// ReadMatches is each match of Find with a little context around it,
	// for a body too large to hand back (an app bundle).
	ReadMatches PageRead = "matches"
)

// PageCall is one call's options. It is handed to the page as JSON.
type PageCall struct {
	URL     string            `json:"url"`
	Method  string            `json:"method,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Body is sent as is when a string and as JSON otherwise.
	Body any `json:"body,omitempty"`
	// Credentials is fetch's mode; empty is the browser's default, which sends
	// cookies only to the page's own origin. A cross-origin call that carries
	// cookies needs the service to allow credentials explicitly.
	Credentials string `json:"credentials,omitempty"`
	// Redirect is fetch's redirect mode; "manual" answers a redirect as
	// Redirected rather than following it.
	Redirect string   `json:"redirect,omitempty"`
	Read     PageRead `json:"read,omitempty"`
	// Limit caps ReadText; zero is 60,000 characters.
	Limit int `json:"limit,omitempty"`
	// Find is a case-insensitive pattern, and Around the characters kept
	// after each match, for ReadMatches.
	Find   string `json:"find,omitempty"`
	Around int    `json:"around,omitempty"`
	// Token, when set, is looked for in storage before the call; the call is
	// not made without one.
	Token *StorageToken `json:"token,omitempty"`
	// Extra is further fields a prepare reads.
	Extra map[string]any `json:"extra,omitempty"`
}

// StorageToken says where a site's app keeps its bearer and where the call
// carries it.
type StorageToken struct {
	// Stores are "localStorage" and "sessionStorage", searched in order.
	Stores []string `json:"stores"`
	// Key is a case-insensitive pattern over the keys.
	Key string `json:"key"`
	// Path leads into the entry's JSON to the token. Empty takes the longest
	// JWT anywhere in the entry.
	Path []string `json:"path,omitempty"`
	// Expires leads to a millisecond timestamp; a token within a minute of it
	// counts as none.
	Expires []string `json:"expires,omitempty"`
	// Headers carry the token, after Scheme ("Bearer ").
	Headers []string `json:"headers,omitempty"`
	Scheme  string   `json:"scheme,omitempty"`
}

// PageCallAnswer is what one page call came back with.
type PageCallAnswer struct {
	// Status is 0 when no answer arrived: see TimedOut, NoToken and Error.
	Status int `json:"status"`
	// URL is where the fetch ended after redirects.
	URL     string            `json:"url"`
	Type    string            `json:"type"`
	Headers map[string]string `json:"headers"`
	JSON    json.RawMessage   `json:"json"`
	// Excerpt is the start of the body, or what went wrong when there was none.
	Excerpt string `json:"excerpt"`
	Text    string `json:"text"`
	Base64  string `json:"base64"`
	// Length is the whole body's, for ReadText and ReadMatches.
	Length  int      `json:"length"`
	Matches []string `json:"matches"`
	// TimedOut is the endpoint having hung rather than refused.
	TimedOut bool `json:"timed_out"`
	// NoToken is the page holding nothing to ask with: the call was not made.
	// Keys then names what the page does hold, for the note.
	NoToken bool     `json:"no_token"`
	Keys    []string `json:"keys"`
	// Error is the fetch's own failure, when it threw.
	Error string `json:"error"`
	// Redirected is a redirect a "manual" call was answered with; its status
	// is 0 and its target unreadable.
	Redirected bool `json:"redirected"`
}

func (a PageCallAnswer) Bytes() ([]byte, error) { return base64.StdEncoding.DecodeString(a.Base64) }

// storageEntries answers every entry whose key matches, as {store, key, value},
// and every key it saw.
const storageEntries = `((stores, pattern) => {
  const found = [];
  const keys = [];
  const matches = new RegExp(pattern, 'i');
  for (const name of stores) {
    let store = null;
    try { store = window[name]; } catch {}
    if (!store) continue;
    for (let i = 0; i < store.length; i += 1) {
      const key = store.key(i);
      if (!key) continue;
      keys.push(name + ':' + key);
      if (matches.test(key)) found.push({ store: name, key, value: store.getItem(key) || '' });
    }
  }
  return { found, keys: keys.slice(0, 40) };
})`

// storageToken answers {value, keys} for a StorageToken.
const storageToken = `((spec) => {
  const read = ` + storageEntries + `(spec.stores || [], spec.key);
  const walk = (node, path) => { for (const step of path || []) node = node == null ? undefined : node[step]; return node; };
  let value = '';
  for (const entry of read.found) {
    let found = '';
    if (spec.path && spec.path.length) {
      let parsed;
      try { parsed = JSON.parse(entry.value); } catch { continue; }
      const expires = spec.expires && spec.expires.length ? walk(parsed, spec.expires) : 0;
      if (expires && expires <= Date.now() + 60000) continue;
      const token = walk(parsed, spec.path);
      if (typeof token === 'string') found = token;
    } else {
      const jwt = entry.value.match(/eyJ[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+/);
      if (jwt) found = jwt[0];
    }
    if (found.length > value.length) value = found;
  }
  return { value, keys: read.keys };
})`

// PageCallScript is the page-call script. prepare is the body of a function run
// in the page after the token is read: handed the argument, it answers what to
// add to the request (`{headers, body}`), or null when the page holds nothing
// to ask with, in which case no call is made. Empty adds nothing.
func PageCallScript(prepare string) string {
	if prepare == "" {
		prepare = "return {};"
	}
	return `async (arg) => {
  const answer = (fields) => Object.assign({
    status: 0, url: '', type: '', headers: {}, json: null, excerpt: '', text: '', base64: '',
    length: 0, matches: [], timed_out: false, no_token: false, keys: [], error: '', redirected: false,
  }, fields);
  const headers = Object.assign({}, arg.headers || {});
  if (arg.token) {
    const token = ` + storageToken + `(arg.token);
    if (!token.value) {
      return answer({ excerpt: 'this page holds no session token', no_token: true, keys: token.keys });
    }
    for (const name of arg.token.headers || []) headers[name] = (arg.token.scheme || '') + token.value;
  }
  const prepared = ((arg) => { ` + prepare + ` })(arg);
  if (prepared === null) return answer({ excerpt: 'this page holds no session token', no_token: true });
  Object.assign(headers, prepared.headers || {});
  let body = prepared.body !== undefined ? prepared.body : arg.body;
  if (body !== undefined && body !== null && typeof body !== 'string') body = JSON.stringify(body);
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), arg.timeoutMs);
  try {
    const init = {
      method: arg.method || 'GET', headers, signal: controller.signal,
      body: body === undefined || body === null ? undefined : body,
    };
    if (arg.credentials) init.credentials = arg.credentials;
    if (arg.redirect) init.redirect = arg.redirect;
    const response = await fetch(arg.url, init);
    const answered = {};
    response.headers.forEach((value, key) => { answered[key] = value; });
    const got = {
      status: response.status, url: response.url, type: response.headers.get('content-type') || '', headers: answered,
      redirected: response.type === 'opaqueredirect',
    };
    if (arg.read === 'bytes') {
      const blob = await response.blob();
      const dataURL = await new Promise((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve(reader.result);
        reader.onerror = () => reject(reader.error);
        reader.readAsDataURL(blob);
      });
      return answer(Object.assign(got, { base64: dataURL.slice(dataURL.indexOf(',') + 1) }));
    }
    const text = await response.text();
    if (arg.read === 'text') return answer(Object.assign(got, { text: text.slice(0, arg.limit || 60000), length: text.length }));
    if (arg.read === 'matches') {
      const re = new RegExp(arg.find, 'gi');
      const matches = [];
      let m;
      while ((m = re.exec(text)) && matches.length < 60) {
        matches.push(text.slice(Math.max(0, m.index - 200), m.index + (arg.around || 400)));
        if (m.index === re.lastIndex) re.lastIndex += 1;
      }
      return answer(Object.assign(got, { matches, length: text.length }));
    }
    let json = null;
    try { json = JSON.parse(text); } catch {}
    return answer(Object.assign(got, { json, excerpt: text.slice(0, 300) }));
  } catch (error) {
    if (controller.signal.aborted) {
      return answer({ excerpt: 'the call did not answer in time', timed_out: true, error: 'the call did not answer in time' });
    }
    const message = String(error && error.message || error);
    return answer({ excerpt: message, error: message });
  } finally {
    clearTimeout(timer);
  }
}`
}

var plainPageCall = PageCallScript("")

// Argument is the call as the script takes it, armed to abort at ctx's
// deadline (see FetchTimeoutMS).
func (c PageCall) Argument(ctx context.Context) (map[string]any, error) {
	if c.Read == "" {
		c.Read = ReadJSON
	}
	c.Method = strings.ToUpper(c.Method)
	shaped, err := jsonArg(c)
	if err != nil {
		return nil, err
	}
	arg := shaped.(map[string]any)
	arg["timeoutMs"] = FetchTimeoutMS(ctx)
	return arg, nil
}

// CallFromPage makes one call from the page and answers when it does or when
// ctx ends. A fetch that threw is an answer with Error set, not an error.
func CallFromPage(ctx context.Context, page Page, call PageCall) (PageCallAnswer, error) {
	var answer PageCallAnswer
	err := EvaluatePageCall(ctx, page, plainPageCall, call, &answer)
	return answer, err
}

// EvaluatePageCall runs a PageCallScript with the call and decodes the answer
// into out, for a caller with a prepare or an answer type of its own.
func EvaluatePageCall(ctx context.Context, page Page, script string, call PageCall, out any) error {
	arg, err := call.Argument(ctx)
	if err != nil {
		return err
	}
	return EvaluateIntoContext(ctx, page, script, arg, out)
}

// StorageEntry is one entry ReadStorage found.
type StorageEntry struct {
	Store string `json:"store"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ReadStorage answers every entry in the named stores whose key matches the
// case-insensitive pattern. What an entry means is the caller's to decide.
func ReadStorage(page Page, stores []string, pattern string) ([]StorageEntry, error) {
	var read struct {
		Found []StorageEntry `json:"found"`
	}
	script := `({ stores, pattern }) => ` + storageEntries + `(stores, pattern)`
	listed := make([]any, len(stores))
	for i, store := range stores {
		listed[i] = store
	}
	err := EvaluateInto(page, script, map[string]any{"stores": listed, "pattern": pattern}, &read)
	return read.Found, err
}

// StorageTokenHeld is a script expression, true while the page holds the
// token, for a wait.
func StorageTokenHeld(token StorageToken) string {
	spec, _ := json.Marshal(token)
	return `Boolean(` + storageToken + `(` + string(spec) + `).value)`
}
