package commands

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"github.com/outplane/cli/internal/clierr"
	"github.com/outplane/cli/internal/core"
	"github.com/outplane/cli/internal/output"
)

func init() {
	register("volume list", volumeList)
	register("volume get", volumeGet)
	register("volume create", volumeCreate)
	register("volume attach", volumeAttach)
	register("volume detach", volumeDetach)
	register("volume delete", volumeDelete)
}

// volumeList reports the team's disks, or one application's.
func volumeList(ctx context.Context, req Request) (output.Table, error) {
	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	var volumes []core.Volume
	if ref := req.Flags.String("app"); strings.TrimSpace(ref) != "" {
		app, err := resolveApp(ctx, req, ref)
		if err != nil {
			return output.Table{}, err
		}
		volumes, err = core.AppVolumes(ctx, client, app.ID)
		if err != nil {
			return output.Table{}, err
		}
	} else {
		volumes, err = core.ListVolumes(ctx, client)
		if err != nil {
			return output.Table{}, err
		}
	}

	table := output.Table{
		Columns: []string{"name", "sizeGb", "status", "app", "mountPath"},
		Units:   map[string]output.Unit{},
		Total:   len(volumes),
	}
	for _, v := range volumes {
		table.Rows = append(table.Rows, volumeRow(v))
	}
	return table, nil
}

// volumeGet reports one disk.
func volumeGet(ctx context.Context, req Request) (output.Table, error) {
	volume, err := targetVolume(ctx, req, "get")
	if err != nil {
		return output.Table{}, err
	}
	return output.Table{
		Single:  true,
		Columns: []string{"name", "id", "sizeGb", "status", "app", "mountPath", "accessMode", "createdAt"},
		Total:   1,
		Rows:    []map[string]any{volumeRow(volume)},
	}, nil
}

// volumeCreate makes a disk, and attaches it when told where to.
//
// Creating and attaching are one call because the server offers one, but they
// are two steps inside it: the disk is created detached and then mounted, so a
// failure to mount leaves a disk that exists. The message says so, because the
// obvious reaction to a failure is to run the command again, and that would
// make a second disk.
func volumeCreate(ctx context.Context, req Request) (output.Table, error) {
	if len(req.Args) == 0 {
		return output.Table{}, clierr.New(clierr.KindUsage, "no name given").
			WithCode("usage.missing_argument").
			WithStep("create a disk", "outplane", "volume", "create", "<NAME>", "--size", "10")
	}

	name := req.Args[0]
	if err := core.CheckVolumeName(name); err != nil {
		return output.Table{}, err
	}

	size, err := volumeSize(req)
	if err != nil {
		return output.Table{}, err
	}

	appID, appName, mountPath, err := attachTarget(ctx, req)
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		describeNewVolume(req, name, size, appName, mountPath)
		return output.Table{
			Single:  true,
			Columns: []string{"name", "sizeGb", "status", "app", "mountPath", "changed"},
			Total:   1,
			Rows: []map[string]any{{
				"name": name, "sizeGb": size, "status": "detached",
				"app": nilIfEmpty(appName), "mountPath": nilIfEmpty(mountPath), "changed": false,
			}},
		}, nil
	}

	volume, err := core.CreateVolume(ctx, client, name, size, appID, mountPath)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Created %s, %d GB.", volume.Name, volume.SizeGB)
	if appName != "" {
		if volume.Status == "attached" {
			req.CLI.Out.Note("Attached to %s at %s. It appears on the next deployment.", appName, volume.MountPath)
		} else {
			req.CLI.Out.Note("The disk exists but was not attached. Attach it rather than "+
				"creating another: `outplane volume attach %s --app %s --mount %s`.",
				volume.Name, appName, mountPath)
		}
	}

	return volumeSingle(volume, true), nil
}

// volumeAttach mounts an existing disk.
func volumeAttach(ctx context.Context, req Request) (output.Table, error) {
	volume, err := targetVolume(ctx, req, "attach")
	if err != nil {
		return output.Table{}, err
	}

	appID, appName, mountPath, err := attachTarget(ctx, req)
	if err != nil {
		return output.Table{}, err
	}
	if appID == "" {
		return output.Table{}, clierr.New(clierr.KindUsage, "no application given").
			WithCode("usage.missing_argument").
			WithHint("Attaching needs both an application and a path.").
			WithStep("attach it", "outplane", "volume", "attach", volume.Name,
				"--app", "<APP_NAME>", "--mount", "/data")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would attach %s to %s at %s. Nothing was sent.", volume.Name, appName, mountPath)
		return volumeSingle(volume, false), nil
	}

	updated, err := core.AttachVolume(ctx, client, volume.ID, appID, mountPath)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Attached %s to %s at %s.", updated.Name, appName, updated.MountPath)
	req.CLI.Out.Note("The application picks it up on its next deployment.")
	return volumeSingle(updated, true), nil
}

// volumeDetach unmounts a disk without destroying it.
//
// It is a separate command from attach even though the server has one endpoint,
// because detaching by omitting an argument is how somebody detaches by
// accident.
func volumeDetach(ctx context.Context, req Request) (output.Table, error) {
	volume, err := targetVolume(ctx, req, "detach")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if volume.Status != "attached" {
		req.CLI.Out.Note("%s is not attached to anything.", volume.Name)
		return volumeSingle(volume, false), nil
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("Would detach %s from %s. The disk and its contents stay.", volume.Name, volume.App)
		return volumeSingle(volume, false), nil
	}

	updated, err := core.DetachVolume(ctx, client, volume.ID)
	if err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Detached %s from %s. The disk and its contents are kept.", volume.Name, volume.App)
	return volumeSingle(updated, true), nil
}

// volumeDelete destroys a disk.
//
// The same confirmation protocol as `app delete`, because the consequence is
// the same shape: an application can be recreated from its source, and the data
// on a disk cannot be recreated from anything.
func volumeDelete(ctx context.Context, req Request) (output.Table, error) {
	volume, err := targetVolume(ctx, req, "delete")
	if err != nil {
		return output.Table{}, err
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return output.Table{}, err
	}

	if req.CLI.DryRun {
		req.CLI.Out.Note("%s (%d GB) and everything on it would be destroyed.", volume.Name, volume.SizeGB)
		if volume.Status == "attached" {
			req.CLI.Out.Note("It is attached to %s, which the platform refuses to delete around.", volume.App)
		}
		return volumeSingle(volume, false), nil
	}

	if err := checkVolumeConfirmed(req, volume); err != nil {
		return output.Table{}, err
	}

	if err := core.DeleteVolume(ctx, client, volume.ID); err != nil {
		return output.Table{}, err
	}

	req.CLI.Out.Note("Deleted %s.", volume.Name)
	return volumeSingle(volume, true), nil
}

// checkVolumeConfirmed is app delete's protocol, applied to data.
func checkVolumeConfirmed(req Request, volume core.Volume) error {
	if harness := req.CLI.Exec.AgentHarness; harness != "" {
		return volumeConfirmation(volume,
			"This is running under %s, where the CLI cannot be the thing that approves "+
				"destroying data. Hand the command below to whoever is accountable for it.", harness)
	}

	if !req.Flags.Bool("yes") || req.Flags.String("confirm-name") == "" {
		return volumeConfirmation(volume,
			"Deleting %s destroys its contents, and nothing restores them. Both --yes and "+
				"--confirm-name are required.", volume.Name)
	}

	if given := req.Flags.String("confirm-name"); given != volume.Name {
		return clierr.New(clierr.KindUsage,
			"--confirm-name says %q and the volume is called %q", given, volume.Name).
			WithCode("volume.confirm_name_mismatch").
			WithDetail("expected", volume.Name).
			WithDetail("given", given)
	}
	return nil
}

func volumeConfirmation(volume core.Volume, hint string, args ...any) error {
	return clierr.New(clierr.KindConfirmation, "deleting %s needs confirmation", volume.Name).
		WithCode("confirmation.required").
		WithHint(hint, args...).
		WithConfirmCommand("outplane", "volume", "delete", volume.Name,
			"--yes", "--confirm-name", volume.Name)
}

// targetVolume resolves the volume argument, translating the domain errors.
func targetVolume(ctx context.Context, req Request, verb string) (core.Volume, error) {
	if len(req.Args) == 0 {
		return core.Volume{}, clierr.New(clierr.KindUsage, "no volume given").
			WithCode("usage.missing_argument").
			WithStep("see the team's disks", "outplane", "volume", "list").
			WithStep(verb+" one", "outplane", "volume", verb, "<VOLUME_NAME>")
	}
	if strings.TrimSpace(req.Args[0]) == "" {
		return core.Volume{}, clierr.New(clierr.KindUsage, "the volume argument is empty").
			WithCode("usage.empty_argument").
			WithHint("This is what an unset variable looks like.")
	}

	client, err := req.CLI.APIClient()
	if err != nil {
		return core.Volume{}, err
	}

	volume, err := core.FindVolume(ctx, client, req.Args[0])
	if err == nil {
		return volume, nil
	}

	var notFound *core.VolumeNotFoundError
	if errors.As(err, &notFound) {
		e := clierr.New(clierr.KindNotFound, "%v", notFound).
			WithCode("volume.not_found").
			WithStep("see the team's disks", "outplane", "volume", "list")
		if len(notFound.Available) > 0 {
			e = e.WithHint("This team has: %s.", strings.Join(notFound.Available, ", ")).
				WithDetail("availableVolumes", notFound.Available)
		} else {
			e = e.WithHint("This team has no volumes yet.")
		}
		return core.Volume{}, e
	}

	var ambiguous *core.AmbiguousVolumeError
	if errors.As(err, &ambiguous) {
		return core.Volume{}, clierr.New(clierr.KindUsage, "%v", ambiguous).
			WithCode("volume.ambiguous").
			WithHint("Names are not unique. Use the id, which `outplane volume list --json` reports.")
	}

	return core.Volume{}, err
}

// attachTarget reads --app and --mount, which are given together or not at all.
func attachTarget(ctx context.Context, req Request) (appID, appName, mountPath string, err error) {
	ref := strings.TrimSpace(req.Flags.String("app"))
	mountPath = strings.TrimSpace(req.Flags.String("mount"))

	if ref == "" && mountPath == "" {
		return "", "", "", nil
	}
	if ref == "" || mountPath == "" {
		return "", "", "", clierr.New(clierr.KindUsage,
			"--app and --mount go together").
			WithCode("volume.attach_incomplete").
			WithHint("A disk is mounted on an application at a path, so both are needed.")
	}
	if err := core.CheckMountPath(mountPath); err != nil {
		return "", "", "", err
	}

	app, err := resolveApp(ctx, req, ref)
	if err != nil {
		return "", "", "", err
	}
	return app.ID, app.Name, mountPath, nil
}

func volumeSize(req Request) (int, error) {
	raw := req.Flags.String("size")
	if strings.TrimSpace(raw) == "" {
		return 0, clierr.New(clierr.KindUsage, "no size given").
			WithCode("usage.missing_argument").
			WithHint("Disks are between %d and %d GB.", core.MinVolumeGB, core.MaxVolumeGB).
			WithStep("create a disk", "outplane", "volume", "create", "<NAME>", "--size", "10")
	}

	size, err := strconv.Atoi(raw)
	if err != nil {
		return 0, clierr.New(clierr.KindUsage, "--size is not a number: %q", raw).
			WithCode("usage.bad_size")
	}
	return size, core.CheckVolumeSize(size)
}

func describeNewVolume(req Request, name string, size int, appName, mountPath string) {
	req.CLI.Out.Note("Would create %s, %d GB.", name, size)
	if appName != "" {
		req.CLI.Out.Note("  attached to %s at %s", appName, mountPath)
	}
	req.CLI.Out.Note("Nothing was sent.")
}

func volumeSingle(v core.Volume, changed bool) output.Table {
	row := volumeRow(v)
	row["changed"] = changed
	return output.Table{
		Single:  true,
		Columns: []string{"name", "sizeGb", "status", "app", "mountPath", "changed"},
		Total:   1,
		Rows:    []map[string]any{row},
	}
}

func volumeRow(v core.Volume) map[string]any {
	return map[string]any{
		"id":         v.ID,
		"name":       v.Name,
		"sizeGb":     v.SizeGB,
		"status":     v.Status,
		"app":        nilIfEmpty(v.App),
		"appId":      nilIfEmpty(v.AppID),
		"mountPath":  nilIfEmpty(v.MountPath),
		"accessMode": v.AccessMode,
		"createdAt":  nilIfEmpty(v.CreatedAt),
	}
}
