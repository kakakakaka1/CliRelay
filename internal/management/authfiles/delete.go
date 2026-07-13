package authfiles

import (
	"context"
	"errors"
	"fmt"
	"os"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

var ErrAuthFileNotFound = errors.New("auth file not found")

type DeleteService struct {
	AuthDir        string
	TenantID       string
	Manager        *coreauth.Manager
	Repository     Repository
	RemoveChannels func([]string) error
}

type DeleteResult struct {
	Deleted int
}

func (s DeleteService) DeleteAll(ctx context.Context) (DeleteResult, error) {
	authDir := TenantAuthDir(s.AuthDir, s.TenantID)
	entries, err := os.ReadDir(authDir)
	if err != nil {
		return DeleteResult{}, fmt.Errorf("failed to read auth dir: %w", err)
	}
	result := DeleteResult{}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !IsJSONFileName(name) {
			continue
		}
		full := FilePath(authDir, name)
		target := FindByNameOrIDForTenant(s.Manager, s.TenantID, name)
		deletedChannels := DeletedChannelIdentifiers(target)
		if errRemove := os.Remove(full); errRemove != nil {
			continue
		}
		if errDelete := s.Repository.Delete(ctx, full); errDelete != nil {
			return result, errDelete
		}
		result.Deleted++
		if target != nil {
			_, _ = s.Manager.Delete(coreauth.WithSkipPersist(ctx), target.ID)
		}
		if errCleanup := s.removeChannelReferences(deletedChannels); errCleanup != nil {
			return result, errCleanup
		}
	}
	return result, nil
}

func (s DeleteService) DeleteOne(ctx context.Context, name string) (DeleteResult, error) {
	full := ExistingTenantFilePath(s.AuthDir, s.TenantID, name)
	target := FindByNameOrIDForTenant(s.Manager, s.TenantID, name)
	deletedChannels := DeletedChannelIdentifiers(target)
	if err := os.Remove(full); err != nil {
		if os.IsNotExist(err) {
			return DeleteResult{}, ErrAuthFileNotFound
		}
		return DeleteResult{}, fmt.Errorf("failed to remove file: %w", err)
	}
	if err := s.Repository.Delete(ctx, full); err != nil {
		return DeleteResult{}, err
	}
	if target != nil {
		_, _ = s.Manager.Delete(coreauth.WithSkipPersist(ctx), target.ID)
	}
	if err := s.removeChannelReferences(deletedChannels); err != nil {
		return DeleteResult{}, err
	}
	return DeleteResult{Deleted: 1}, nil
}

func (s DeleteService) removeChannelReferences(channels []string) error {
	if len(channels) == 0 || s.RemoveChannels == nil {
		return nil
	}
	return s.RemoveChannels(channels)
}
