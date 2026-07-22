package controlsettings

import (
	"context"
	"errors"
	"fmt"

	"github.com/hujinrun/flowspace/internal/controlprofile"
)

type SystemProfileSpec struct {
	CandidateID string
	FamilyID    string
	Kind        string
	Name        string
	Provider    string
	ConfigJSON  string
	Secret      []byte
	Mode        string
}

// ReconcileSystemDefaults imports deployment-owned resources as immutable
// profile versions. It returns concrete endpoint identities for provisioning;
// it never changes bindings that already exist.
func ReconcileSystemDefaults(ctx context.Context, profiles *controlprofile.Repository, specs []SystemProfileSpec) ([]WorkspaceDefaultBinding, error) {
	if profiles == nil || len(specs) != 4 {
		return nil, errors.New("four system profile defaults are required")
	}
	seen := make(map[string]bool, len(specs))
	result := make([]WorkspaceDefaultBinding, 0, len(specs))
	for _, spec := range specs {
		if seen[spec.Kind] || !validDefaultMode(spec.Kind, spec.Mode) {
			return nil, fmt.Errorf("invalid system default for %s", spec.Kind)
		}
		seen[spec.Kind] = true
		version, _, err := profiles.ReconcileSystemCandidate(ctx, controlprofile.ReconcileSystemInput{
			CandidateID: spec.CandidateID,
			FamilyID:    spec.FamilyID,
			Kind:        spec.Kind,
			Name:        spec.Name,
			Provider:    spec.Provider,
			ConfigJSON:  spec.ConfigJSON,
			Secret:      spec.Secret,
		})
		if err != nil {
			return nil, err
		}
		if version.State == "draft" {
			if err := profiles.MarkSystemVerified(ctx, spec.Kind, version.ID); err != nil {
				return nil, err
			}
		}
		result = append(result, WorkspaceDefaultBinding{
			Kind:             spec.Kind,
			Mode:             spec.Mode,
			EndpointID:       "system-" + spec.Kind + "-" + version.ID,
			ProfileVersionID: version.ID,
		})
	}
	return result, nil
}
