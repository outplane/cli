package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Persistent disks.
//
// Three platform facts shape every command over them:
//
//   - A disk belongs to one application at one path. There is no sharing and no
//     second mount, which is why attaching is a single field rather than a list.
//   - Attaching and detaching are the same endpoint, told apart by whether an
//     application is named. The CLI keeps them as two commands anyway: nobody
//     detaches by leaving an argument out on purpose.
//   - Moving a disk to another path on the same application is refused. It has
//     to be detached first, which is the server making the data's owner say
//     twice that it should move.

// Volume is a persistent disk.
type Volume struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	SizeGB int    `json:"sizeGb"`

	// Status is "attached" or "detached", and App and MountPath are set only
	// for the first. They are reported separately rather than derived from one
	// another so that a caller can branch on the state without parsing a name.
	Status    string `json:"status"`
	App       string `json:"app"`
	AppID     string `json:"appId"`
	MountPath string `json:"mountPath"`

	// AccessMode is how many instances may mount it at once. Every volume the
	// platform creates is ReadWriteOnce, so this is reported and never chosen.
	AccessMode string `json:"accessMode"`
	CreatedAt  string `json:"createdAt"`
}

type volumeDTO struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	SizeInGB   int    `json:"sizeInGb"`
	Status     int    `json:"status"`
	AccessMode int    `json:"accessMode"`
	AppID      string `json:"appId"`
	AppName    string `json:"appName"`
	MountPath  string `json:"mountPath"`

	CreatedDate string `json:"createdDate"`
}

// Volume states and access modes, as integers on the wire. See enum.go.
var (
	volumeStatusNames = enumNames{10: "detached", 20: "attached"}
	accessModeNames   = enumNames{10: "ReadWriteOnce", 20: "ReadOnlyMany", 30: "ReadWriteMany"}
)

// Volume size bounds, which the server enforces and this restates so that a
// wrong one is refused before a disk is created at the wrong size.
const (
	MinVolumeGB = 10
	MaxVolumeGB = 50
)

// ListVolumes returns every volume in the team, sorted by name.
func ListVolumes(ctx context.Context, c *api.Client) ([]Volume, error) {
	var dtos []volumeDTO
	if err := c.Get(ctx, "/Volume/GetVolumesByTeamId", &dtos); err != nil {
		return nil, err
	}
	return decodeVolumes(dtos), nil
}

// AppVolumes returns the volumes attached to one application.
func AppVolumes(ctx context.Context, c *api.Client, appID string) ([]Volume, error) {
	var dtos []volumeDTO
	if err := c.Get(ctx, "/Volume/GetVolumesByAppId/"+appID, &dtos); err != nil {
		return nil, err
	}
	return decodeVolumes(dtos), nil
}

func decodeVolumes(dtos []volumeDTO) []Volume {
	volumes := make([]Volume, 0, len(dtos))
	for _, d := range dtos {
		volumes = append(volumes, Volume{
			ID:         d.ID,
			Name:       d.Name,
			SizeGB:     d.SizeInGB,
			Status:     volumeStatusNames.name(d.Status),
			App:        d.AppName,
			AppID:      d.AppID,
			MountPath:  d.MountPath,
			AccessMode: accessModeNames.name(d.AccessMode),
			CreatedAt:  serverInstant(d.CreatedDate),
		})
	}
	sort.Slice(volumes, func(i, j int) bool { return volumes[i].Name < volumes[j].Name })
	return volumes
}

// CreateVolume makes a disk, optionally attaching it in the same call.
//
// The server creates it detached and then attaches, so a failure to attach
// leaves a disk that exists and is not mounted. That is worth knowing before
// retrying: the retry would create a second one.
func CreateVolume(ctx context.Context, c *api.Client, name string, sizeGB int, appID, mountPath string) (Volume, error) {
	body := map[string]any{"name": name, "sizeInGb": sizeGB}
	if appID != "" {
		body["appId"] = appID
		body["mountPath"] = mountPath
	}

	var dto volumeDTO
	if err := c.Post(ctx, "/Volume/CreateVolume", body, &dto); err != nil {
		return Volume{}, err
	}
	return decodeVolumes([]volumeDTO{dto})[0], nil
}

// AttachVolume mounts a disk on an application.
func AttachVolume(ctx context.Context, c *api.Client, volumeID, appID, mountPath string) (Volume, error) {
	return updateVolume(ctx, c, volumeID, map[string]any{"appId": appID, "mountPath": mountPath})
}

// DetachVolume unmounts a disk, keeping the disk and its contents.
func DetachVolume(ctx context.Context, c *api.Client, volumeID string) (Volume, error) {
	// Both fields null is what the server reads as "detach". Omitting them
	// entirely would be the same request, and being explicit is what makes this
	// call readable next to AttachVolume.
	return updateVolume(ctx, c, volumeID, map[string]any{"appId": nil, "mountPath": nil})
}

func updateVolume(ctx context.Context, c *api.Client, volumeID string, body map[string]any) (Volume, error) {
	var dto volumeDTO
	if err := c.Put(ctx, "/Volume/UpdateVolume/"+volumeID, body, &dto); err != nil {
		return Volume{}, err
	}
	return decodeVolumes([]volumeDTO{dto})[0], nil
}

// DeleteVolume destroys a disk and everything on it.
func DeleteVolume(ctx context.Context, c *api.Client, volumeID string) error {
	return c.Delete(ctx, "/Volume/DeleteVolume/"+volumeID, nil)
}

// FindVolume resolves a reference into exactly one volume.
//
// The same exact-match rule as applications, for the same reason: resolving a
// near miss would work until two disks share a prefix, and the command being
// resolved for might be a deletion. Names are unique in practice but not
// enforced, so a tie is reported rather than guessed.
func FindVolume(ctx context.Context, c *api.Client, ref string) (Volume, error) {
	volumes, err := ListVolumes(ctx, c)
	if err != nil {
		return Volume{}, err
	}

	for _, v := range volumes {
		if v.ID == ref {
			return v, nil
		}
	}

	var byName []Volume
	for _, v := range volumes {
		if v.Name == ref {
			byName = append(byName, v)
		}
	}

	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return Volume{}, &VolumeNotFoundError{Ref: ref, Available: volumeNames(volumes)}
	default:
		return Volume{}, &AmbiguousVolumeError{Ref: ref, Count: len(byName)}
	}
}

// VolumeNotFoundError carries what does exist, so the correction is one read
// away rather than another command away.
type VolumeNotFoundError struct {
	Ref       string
	Available []string
}

func (e *VolumeNotFoundError) Error() string {
	return fmt.Sprintf("no volume called %q in this team", e.Ref)
}

// AmbiguousVolumeError means two disks share a name and only an id can say
// which was meant.
type AmbiguousVolumeError struct {
	Ref   string
	Count int
}

func (e *AmbiguousVolumeError) Error() string {
	return fmt.Sprintf("%d volumes are called %q", e.Count, e.Ref)
}

func volumeNames(volumes []Volume) []string {
	names := make([]string, 0, len(volumes))
	for _, v := range volumes {
		names = append(names, v.Name)
	}
	return names
}

// CheckVolumeName rejects a name the server would refuse.
func CheckVolumeName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return usage("a volume needs a name", "volume.name_required", "")
	case len(name) > 100:
		return usage(fmt.Sprintf("%q is longer than 100 characters", short(name)),
			"volume.name_invalid", "")
	case !isVolumeName(name):
		return usage(fmt.Sprintf("%q is not a valid volume name", short(name)),
			"volume.name_invalid",
			"It has to start with a letter or a number, and may then contain letters, "+
				"numbers, hyphens and underscores.")
	}
	return nil
}

// CheckVolumeSize rejects a size outside what the platform provisions.
func CheckVolumeSize(sizeGB int) error {
	if sizeGB < MinVolumeGB || sizeGB > MaxVolumeGB {
		return usage(fmt.Sprintf("%d GB, and the range is %d to %d",
			sizeGB, MinVolumeGB, MaxVolumeGB), "volume.size_invalid", "")
	}
	return nil
}

// CheckMountPath rejects a path the server would refuse.
func CheckMountPath(path string) error {
	switch {
	case path == "":
		return usage("a mount needs a path", "volume.mount_required",
			"The path is where the disk appears inside the container, such as /data.")
	case !strings.HasPrefix(path, "/"):
		return usage(fmt.Sprintf("%q is not an absolute path", path), "volume.mount_invalid", "")
	case len(path) > 255:
		return usage("the mount path is longer than 255 characters", "volume.mount_invalid", "")
	}
	return nil
}

func isVolumeName(name string) bool {
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case (r == '-' || r == '_') && i > 0:
		default:
			return false
		}
	}
	return true
}
