package authfiles

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

var (
	ErrNameRequired     = errors.New("name is required")
	ErrDisabledRequired = errors.New("disabled is required")
)

type StatusPatch struct {
	Name     string
	Disabled *bool
}

type StatusPatchResult struct {
	Disabled bool
}

type PatchService struct {
	Manager        *coreauth.Manager
	TenantID       string
	Repository     Repository
	Now            time.Time
	ValidateLabel  func(label, excludeAuthID string) (string, error)
	RenameChannels func(oldNames []string, newName string) error
}

type internalPatchError struct {
	err error
}

func (e internalPatchError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e internalPatchError) Unwrap() error {
	return e.err
}

func IsInternalPatchError(err error) bool {
	var target internalPatchError
	return errors.As(err, &target)
}

func (s PatchService) Available() bool {
	return s.Manager != nil
}

func (s PatchService) PatchStatus(ctx context.Context, patch StatusPatch) (StatusPatchResult, error) {
	if !s.Available() {
		return StatusPatchResult{}, ErrAuthManagerUnavailable
	}
	name := strings.TrimSpace(patch.Name)
	if name == "" {
		return StatusPatchResult{}, ErrNameRequired
	}
	if patch.Disabled == nil {
		return StatusPatchResult{}, ErrDisabledRequired
	}
	targetAuth := FindByNameOrIDForTenant(s.Manager, s.TenantID, name)
	if targetAuth == nil {
		return StatusPatchResult{}, ErrAuthFileNotFound
	}
	if errPatch := ApplyStatusPatch(targetAuth, *patch.Disabled, s.Now); errPatch != nil {
		return StatusPatchResult{}, errPatch
	}
	if _, errUpdate := s.Manager.Update(ctx, targetAuth); errUpdate != nil {
		return StatusPatchResult{}, internalPatchError{err: fmt.Errorf("failed to update auth: %w", errUpdate)}
	}
	return StatusPatchResult{Disabled: *patch.Disabled}, nil
}

func (s PatchService) PatchFields(ctx context.Context, patch FieldPatch) error {
	if !s.Available() {
		return ErrAuthManagerUnavailable
	}
	name := strings.TrimSpace(patch.Name)
	if name == "" {
		return ErrNameRequired
	}
	targetAuth := FindByNameOrIDForTenant(s.Manager, s.TenantID, name)
	if targetAuth == nil {
		return ErrAuthFileNotFound
	}
	patchResult, errPatch := ApplyFieldPatch(targetAuth, patch, FieldPatchOptions{
		Now:           s.Now,
		ValidateLabel: s.ValidateLabel,
	})
	if errPatch != nil {
		return errPatch
	}
	if _, errUpdate := s.Manager.Update(ctx, targetAuth); errUpdate != nil {
		return internalPatchError{err: fmt.Errorf("failed to update auth: %w", errUpdate)}
	}
	if path := strings.TrimSpace(Attribute(targetAuth, "path")); path != "" {
		if errPersist := s.Repository.PersistChange(ctx, "Update auth "+targetAuth.FileName, path); errPersist != nil {
			return internalPatchError{err: errPersist}
		}
	}
	if len(patchResult.OldChannelIdentifiers) > 0 && s.RenameChannels != nil {
		if errRename := s.RenameChannels(patchResult.OldChannelIdentifiers, patchResult.NewChannelLabel); errRename != nil {
			return internalPatchError{err: errRename}
		}
	}
	return nil
}
