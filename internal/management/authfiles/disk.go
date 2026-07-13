package authfiles

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"time"

	"github.com/tidwall/gjson"
)

func ListDiskEntries(authDir string, now time.Time) ([]map[string]any, error) {
	entries, err := os.ReadDir(authDir)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now()
	}
	files := make([]map[string]any, 0)
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !IsJSONFileName(name) {
			continue
		}
		info, errInfo := entry.Info()
		if errInfo != nil {
			continue
		}
		fileData := map[string]any{
			"name":    name,
			"size":    info.Size(),
			"modtime": info.ModTime(),
		}

		full := FilePath(authDir, name)
		if data, errRead := os.ReadFile(full); errRead == nil {
			fileData["type"] = gjson.GetBytes(data, "type").String()
			fileData["email"] = gjson.GetBytes(data, "email").String()
			metadata := make(map[string]any)
			if errJSON := json.Unmarshal(data, &metadata); errJSON == nil {
				AddSubscriptionFields(fileData, metadata, now)
			}
		}

		files = append(files, fileData)
	}
	return files, nil
}

func ListTenantDiskEntries(authDir, tenantID string, now time.Time) ([]map[string]any, error) {
	files, err := ListDiskEntries(TenantAuthDir(authDir, tenantID), now)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if NormalizeTenantID(tenantID) != systemTenantID {
		return files, nil
	}
	legacy, legacyErr := ListDiskEntries(authDir, now)
	if legacyErr != nil && !errors.Is(legacyErr, fs.ErrNotExist) {
		return nil, legacyErr
	}
	return append(files, legacy...), nil
}
