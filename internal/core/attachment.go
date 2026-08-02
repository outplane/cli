package core

import (
	"context"

	"github.com/outplane/cli/internal/api"
)

// What an application has attached to it: disks, and shared variable sets.
//
// Both are read back after a creation that asked for them, and the reason is
// the same for both. The server attaches each one inside a try/catch and
// carries on when it fails, so an application created with a volume the server
// could not attach is created successfully, without the volume, and reports
// nothing. Reading back is the only way to tell the caller what they actually
// got.

// Attachment is one volume mounted on an application.
type Attachment struct {
	VolumeID  string `json:"volumeId"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SizeGB    int    `json:"sizeGb"`
}

type attachmentDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SizeInGB  int    `json:"sizeInGb"`
}

// AppVolumes returns the volumes attached to an application.
func AppVolumes(ctx context.Context, c *api.Client, appID string) ([]Attachment, error) {
	var dtos []attachmentDTO
	if err := c.Get(ctx, "/Volume/GetVolumesByAppId/"+appID, &dtos); err != nil {
		return nil, err
	}

	out := make([]Attachment, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, Attachment{
			VolumeID:  d.ID,
			Name:      d.Name,
			MountPath: d.MountPath,
			SizeGB:    d.SizeInGB,
		})
	}
	return out, nil
}

// EnvGroup is a shared set of variables assigned to an application.
type EnvGroup struct {
	GroupID   string `json:"groupId"`
	Name      string `json:"name"`
	Variables int    `json:"variables"`
}

type envGroupDTO struct {
	// The row is an assignment and carries two ids. The group's is the one a
	// caller named when they asked for it, so that is the one reported back.
	EnvVariableGroupID   string `json:"envVariableGroupId"`
	EnvVariableGroupName string `json:"envVariableGroupName"`
	VariableCount        int    `json:"variableCount"`
}

// AppEnvGroups returns the variable groups assigned to an application.
func AppEnvGroups(ctx context.Context, c *api.Client, appID string) ([]EnvGroup, error) {
	var dtos []envGroupDTO
	if err := c.Get(ctx, "/EnvVariableGroup/GetAssignedGroupsByAppId/"+appID, &dtos); err != nil {
		return nil, err
	}

	out := make([]EnvGroup, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, EnvGroup{
			GroupID:   d.EnvVariableGroupID,
			Name:      d.EnvVariableGroupName,
			Variables: d.VariableCount,
		})
	}
	return out, nil
}
