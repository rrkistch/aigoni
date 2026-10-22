package sitebackup

// Inventory is a cheap conflict token for application-owned, atomically-written files.
// It is compared verbatim; it does not reread or hash large media files.
import (
	"encoding/json"
	"os"
	"path/filepath"
)

func Inventory(root string, targets []Target) ([]byte, error) {
	if err := ValidateTargets(root, targets); err != nil {
		return nil, err
	}
	type entry struct {
		Name     string `json:"name"`
		Size     int64  `json:"size"`
		Modified int64  `json:"modified"`
	}
	entries := []entry{}
	for _, target := range targets {
		err := filepath.Walk(target.Path, func(file string, info os.FileInfo, err error) error {
			if os.IsNotExist(err) {
				return nil
			}
			if err != nil {
				return err
			}
			if info.IsDir() {
				return nil
			}
			if !info.Mode().IsRegular() {
				return nil
			}
			rel, _ := filepath.Rel(target.Path, file)
			entries = append(entries, entry{Name: target.Name + "/" + filepath.ToSlash(rel), Size: info.Size(), Modified: info.ModTime().UnixNano()})
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return json.Marshal(struct {
		Targets []Target `json:"targets"`
		Files   []entry  `json:"files"`
	}{targets, entries})
}
