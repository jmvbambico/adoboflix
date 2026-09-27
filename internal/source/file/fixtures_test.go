package file

import (
	"os"
	"path/filepath"
	"testing"
)

// newFixtureAdapter loads the canonical testdata playlist. It goes through the
// real New path, so every test using it also proves the file reads and parses.
func newFixtureAdapter(t *testing.T) *Adapter {
	t.Helper()
	adapter, err := New(filepath.Join("testdata", "library.json"))
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return adapter
}

// newRawAdapter writes contents to a temp file and opens it, so a test can
// exercise any malformed or id-less playlist without a committed fixture.
func newRawAdapter(t *testing.T, contents string) (*Adapter, error) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "library.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	return New(path)
}

// derivedFixture has no ids anywhere, so the adapter must synthesise stable
// ones for the channel and the entry.
const derivedFixture = `{
  "channels": [
    {"name": "No Id TV", "category": "News", "status": "active"}
  ],
  "entries": [
    {"name": "No Id Movie", "type": "Movie", "category": "Films", "provider": "local", "status": "active"}
  ]
}`
