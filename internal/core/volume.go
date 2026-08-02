package core

import (
	"context"

	"github.com/outplane/cli/internal/api"
)

// Volume is a persistent disk, as far as anything outside the volume commands
// needs to know.
//
// Only the fields a reader identifies a disk by are here. Deleting an
// application is the first thing that needs them, because an attached volume
// stops the deletion and the reader has to be told which one.
type Volume struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SizeGB    int    `json:"sizeGb"`
	Status    string `json:"status"`
}

type volumeDTO struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	MountPath string `json:"mountPath"`
	SizeInGB  int    `json:"sizeInGb"`
	Status    int    `json:"status"`
}

// volumeStatusNames decodes VolumeStatus, which arrives as an integer like
// every other enum in this API.
var volumeStatusNames = enumNames{
	10: "detached",
	20: "attached",
}

// AppVolumes returns the volumes attached to one application.
func AppVolumes(ctx context.Context, c *api.Client, appID string) ([]Volume, error) {
	var dtos []volumeDTO
	if err := c.Get(ctx, "/Volume/GetVolumesByAppId/"+appID, &dtos); err != nil {
		return nil, err
	}

	volumes := make([]Volume, 0, len(dtos))
	for _, d := range dtos {
		volumes = append(volumes, Volume{
			ID:        d.ID,
			Name:      d.Name,
			MountPath: d.MountPath,
			SizeGB:    d.SizeInGB,
			Status:    volumeStatusNames.name(d.Status),
		})
	}
	return volumes, nil
}
