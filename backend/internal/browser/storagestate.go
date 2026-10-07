package browser

import (
	"encoding/json"
	"fmt"

	"github.com/playwright-community/playwright-go"
)

// A browser's session as the backend seals it. For a connection with a kept
// profile this only seeds a profile the volume has lost; the profile is the
// session of record.

// StorageState is Playwright's own shape, read and written as JSON.
type StorageState struct {
	Cookies []StoredCookie `json:"cookies"`
	Origins []StoredOrigin `json:"origins"`
}

type StoredCookie struct {
	Name     string  `json:"name"`
	Value    string  `json:"value"`
	Domain   string  `json:"domain"`
	Path     string  `json:"path"`
	Expires  float64 `json:"expires"`
	HTTPOnly bool    `json:"httpOnly"`
	Secure   bool    `json:"secure"`
	SameSite string  `json:"sameSite"`
}

// StoredOrigin is one origin's local storage, and its IndexedDB when the state
// came from a browser asked for it (Playwright's `indexedDB: true`).
type StoredOrigin struct {
	Origin       string           `json:"origin"`
	LocalStorage []StoredEntry    `json:"localStorage"`
	IndexedDB    []StoredDatabase `json:"indexedDB,omitempty"`
}

// StoredDatabase is one IndexedDB database, in Playwright's shape. Firebase
// Auth keeps its refresh token in one (`firebaseLocalStorageDb`), so a session
// made in another browser does not reach a kept profile without it.
type StoredDatabase struct {
	Name    string        `json:"name"`
	Version int           `json:"version"`
	Stores  []StoredStore `json:"stores"`
}

// StoredStore's KeyPath is absent for a store whose records carry their keys
// out of line.
type StoredStore struct {
	Name          string          `json:"name"`
	KeyPath       string          `json:"keyPath,omitempty"`
	KeyPathArray  []string        `json:"keyPathArray,omitempty"`
	AutoIncrement bool            `json:"autoIncrement"`
	Indexes       json.RawMessage `json:"indexes,omitempty"`
	Records       []StoredRecord  `json:"records"`
}

// StoredRecord's Key and Value are plain JSON; a record Playwright could only
// write in its own encoding (`keyEncoded`, `valueEncoded`) is not restored.
type StoredRecord struct {
	Key          json.RawMessage `json:"key,omitempty"`
	Value        json.RawMessage `json:"value,omitempty"`
	KeyEncoded   json.RawMessage `json:"keyEncoded,omitempty"`
	ValueEncoded json.RawMessage `json:"valueEncoded,omitempty"`
}

func (o StoredOrigin) restorable() bool { return len(o.LocalStorage) > 0 || len(o.IndexedDB) > 0 }

type StoredEntry struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

func ReadStorageState(raw []byte) (StorageState, error) {
	var state StorageState
	if len(raw) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, fmt.Errorf("browser: the kept session is not a storage state: %w", err)
	}
	return state, nil
}

func (s StorageState) Seedable() bool { return len(s.Cookies) > 0 || len(s.Origins) > 0 }

type ProfilePlan struct {
	Seed bool
	Note string
}

// PlanProfile opens a profile that is there as it stands, ignoring the sealed
// state, and with no note: a pull stores its first note as the connection's
// last word, and a sentence true on every pull would bury the one that
// matters. A missing directory is seeded from the state when one was sent.
func PlanProfile(dir string, kept StorageState) ProfilePlan {
	if ProfileExists(dir) {
		return ProfilePlan{}
	}
	if kept.Seedable() {
		return ProfilePlan{
			Seed: true,
			Note: "there was no browser profile on disk; it was seeded from the kept session",
		}
	}
	return ProfilePlan{Note: "there was no browser profile on disk; a fresh one was started"}
}

func TakeStorageState(context playwright.BrowserContext) ([]byte, error) {
	state, err := context.StorageState()
	if err != nil {
		return nil, err
	}
	return json.Marshal(state)
}

// restoreIndexedDB writes Playwright-shaped databases into the page's origin.
// A missing store can be created only during an upgrade, which is IndexedDB's
// rule. A database the site holds at a newer version is opened at that
// version rather than refused.
const restoreIndexedDB = `async (databases) => {
  const opened = (name, version) => new Promise((resolve, reject) => {
    const request = version ? indexedDB.open(name, version) : indexedDB.open(name);
    request.onupgradeneeded = () => {
      const handle = request.result;
      const wanted = databases.find((d) => d.name === name);
      for (const store of (wanted && wanted.stores) || []) {
        if (handle.objectStoreNames.contains(store.name)) continue;
        const keyPath = store.keyPathArray || store.keyPath;
        const created = handle.createObjectStore(store.name,
          keyPath ? { keyPath, autoIncrement: store.autoIncrement } : { autoIncrement: store.autoIncrement });
        for (const index of store.indexes || []) {
          created.createIndex(index.name, index.keyPathArray || index.keyPath,
            { unique: !!index.unique, multiEntry: !!index.multiEntry });
        }
      }
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(request.error);
    request.onblocked = () => reject(new Error(name + " is held open at an older version"));
  });
  let written = 0;
  let skipped = 0;
  for (const database of databases) {
    let handle;
    try {
      handle = await opened(database.name, database.version || 1);
    } catch (error) {
      if (!error || error.name !== 'VersionError') throw error;
      handle = await opened(database.name);
    }
    for (const store of database.stores || []) {
      const records = store.records || [];
      if (!handle.objectStoreNames.contains(store.name)) {
        skipped += records.length;
        continue;
      }
      await new Promise((resolve, reject) => {
        const tx = handle.transaction(store.name, 'readwrite');
        const target = tx.objectStore(store.name);
        for (const record of records) {
          if (record.value === undefined) { skipped++; continue; }
          if (target.keyPath !== null) target.put(record.value);
          else if (record.key !== undefined) target.put(record.value, record.key);
          else if (target.autoIncrement) target.put(record.value);
          else { skipped++; continue; }
          written++;
        }
        tx.oncomplete = () => resolve();
        tx.onerror = () => reject(tx.error);
        tx.onabort = () => reject(tx.error);
      });
    }
    handle.close();
  }
  return { written, skipped };
}`

// SeedProfile writes each origin's storage by visiting it, as Playwright's own
// restore does. Not an init script: that would go on rewriting those keys over
// whatever the site stored later.
func SeedProfile(context playwright.BrowserContext, state StorageState, note func(string)) {
	if len(state.Cookies) > 0 {
		cookies := make([]playwright.OptionalCookie, 0, len(state.Cookies))
		for _, one := range state.Cookies {
			cookie := playwright.OptionalCookie{
				Name: one.Name, Value: one.Value,
				Domain: playwright.String(one.Domain), Path: playwright.String(one.Path),
				Expires:  playwright.Float(one.Expires),
				HttpOnly: playwright.Bool(one.HTTPOnly), Secure: playwright.Bool(one.Secure),
			}
			if one.SameSite != "" {
				same := playwright.SameSiteAttribute(one.SameSite)
				cookie.SameSite = &same
			}
			cookies = append(cookies, cookie)
		}
		if err := context.AddCookies(cookies); err != nil {
			note(fmt.Sprintf("the kept cookies could not be restored: %v", err))
		}
	}
	origins := make([]StoredOrigin, 0, len(state.Origins))
	for _, one := range state.Origins {
		if one.restorable() {
			origins = append(origins, one)
		}
	}
	if len(origins) == 0 {
		return
	}
	page, err := context.NewPage()
	if err != nil {
		note(fmt.Sprintf("the kept storage could not be restored: %v", err))
		return
	}
	defer page.Close()
	for _, origin := range origins {
		if _, err := page.Goto(origin.Origin, playwright.PageGotoOptions{
			WaitUntil: playwright.WaitUntilStateDomcontentloaded,
		}); err != nil {
			note(fmt.Sprintf("%s's kept storage could not be restored: %v", origin.Origin, err))
			continue
		}
		// Shaped by hand: this is the real page, not the Page façade, and a
		// slice of structs reaches the page as a list of `undefined`.
		if len(origin.LocalStorage) > 0 {
			items, err := jsonArg(origin.LocalStorage)
			if err != nil {
				note(fmt.Sprintf("%s's kept storage could not be restored: %v", origin.Origin, err))
				continue
			}
			if _, err := page.Evaluate(`(items) => { for (const item of items) window.localStorage.setItem(item.name, item.value); }`,
				items); err != nil {
				note(fmt.Sprintf("%s's kept storage could not be restored: %v", origin.Origin, err))
			}
		}
		if len(origin.IndexedDB) > 0 {
			databases, err := jsonArg(origin.IndexedDB)
			if err != nil {
				note(fmt.Sprintf("%s's kept databases could not be restored: %v", origin.Origin, err))
				continue
			}
			answered, err := page.Evaluate(restoreIndexedDB, databases)
			if err != nil {
				note(fmt.Sprintf("%s's kept databases could not be restored: %v", origin.Origin, err))
				continue
			}
			if counts, ok := answered.(map[string]any); ok {
				note(fmt.Sprintf("%s: restored %v IndexedDB record(s), left out %v",
					origin.Origin, counts["written"], counts["skipped"]))
			}
		}
	}
}
