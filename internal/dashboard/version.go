package dashboard

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"net/http"
)

// The production build supplies its Git revision. Local UI edits are identified
// by the embedded files even when no revision is supplied.
var buildRevision = "development"

func applicationVersion(files fs.FS, revision string) (string, error) {
	digest := sha256.New()
	digest.Write([]byte(revision + "\x00"))
	err := fs.WalkDir(files, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		data, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		digest.Write([]byte(path + "\x00"))
		digest.Write(data)
		digest.Write([]byte{0})
		return nil
	})
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(digest.Sum(nil)[:12]), nil
}

func (a *App) applicationVersion(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(map[string]string{"version": a.version})
}
