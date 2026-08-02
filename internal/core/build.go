package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// How an application is built and started.
//
// One endpoint writes all of it, and it writes the fields it is given rather
// than the fields that changed, which makes this another read-modify-write like
// ports. What the server does with each field is not uniform, and every rule
// below is why BuildSettings exists as a type rather than as six arguments:
//
//   - StartCommand is written unconditionally, empty included. Omitting it
//     clears it.
//   - BuildMethod is written unconditionally too, so a request that leaves it
//     out stores whatever the zero value decodes to. It has to be sent.
//   - Directory is written only when it is not empty, so it cannot be cleared.
//     The server says why: the column is not nullable and the build system
//     assumes a root.
//   - The two filters are the opposite: empty stores null, so omitting one
//     clears it.
//   - For an application built from a container registry the server ignores
//     everything except the image and the start command, silently.
//
// Apply is the whole of that, in one place, so a command changing one field
// cannot accidentally clear another.

// BuildMethods lives in appcreate.go, next to the other things a new
// application declares, and holds the same two names this command accepts.
// prebuilt-image is deliberately not one of them: it is what the platform
// stores for an application that runs somebody else's image, chosen by where
// the application comes from rather than by anybody. The console offers the
// same two.

// MaxBuildFilter is the length of one filter list, matching the console.
const MaxBuildFilter = 2000

// BuildSettings is everything the build settings endpoint writes.
type BuildSettings struct {
	// FromRegistry says the application runs an image somebody else built,
	// which changes what can be set at all.
	FromRegistry bool

	BuildMethod  string
	Directory    string
	StartCommand string
	IncludePaths string
	IgnorePaths  string

	// Image is the reference a registry application runs. It is the same field
	// the API calls SourceRepository, under the name a reader would use.
	Image string
}

// BuildChange is a request to change some of the settings. A nil field is one
// the caller did not mention and that has to survive untouched.
type BuildChange struct {
	BuildMethod  *string
	Directory    *string
	StartCommand *string
	IncludePaths *string
	IgnorePaths  *string
	Image        *string
}

// GetBuildSettings reads what an application is built with.
func GetBuildSettings(ctx context.Context, c *api.Client, appID string) (BuildSettings, error) {
	detail, err := GetApp(ctx, c, appID)
	if err != nil {
		return BuildSettings{}, err
	}
	return buildSettingsOf(detail), nil
}

func buildSettingsOf(d AppDetail) BuildSettings {
	return BuildSettings{
		FromRegistry: d.Source == SourceContainerRegistry,
		BuildMethod:  d.BuildMethod,
		Directory:    d.Directory,
		StartCommand: d.StartCommand,
		IncludePaths: d.IncludePaths,
		IgnorePaths:  d.IgnorePaths,
		Image:        d.ImageRef,
	}
}

// Apply returns the settings a change would produce, leaving everything it did
// not mention alone.
//
// Pure, so what is about to be sent can be shown by --dry-run and held to in a
// test. The rules it does not encode are the server's own asymmetries, which
// are checked by Check: this only decides values.
func (s BuildSettings) Apply(c BuildChange) BuildSettings {
	if c.BuildMethod != nil {
		s.BuildMethod = *c.BuildMethod
	}
	if c.Directory != nil {
		s.Directory = *c.Directory
	}
	if c.StartCommand != nil {
		s.StartCommand = *c.StartCommand
	}
	if c.IncludePaths != nil {
		s.IncludePaths = *c.IncludePaths
	}
	if c.IgnorePaths != nil {
		s.IgnorePaths = *c.IgnorePaths
	}
	if c.Image != nil {
		s.Image = *c.Image
	}
	return s
}

// Check rejects settings the server would refuse or silently drop.
//
// Silently drop is the reason for half of it. A registry application accepts no
// build method and no filters, and the server neither stores nor mentions them,
// so a caller who set one would be told nothing and believe something happened.
func (s BuildSettings) Check(c BuildChange) error {
	if s.FromRegistry {
		for _, f := range []struct {
			name  string
			given bool
		}{
			{"--method", c.BuildMethod != nil},
			{"--dir", c.Directory != nil},
			{"--include-paths", c.IncludePaths != nil},
			{"--ignore-paths", c.IgnorePaths != nil},
		} {
			if f.given {
				return usage(fmt.Sprintf("%s does not apply to an application that runs a prebuilt image", f.name),
					"build.not_built_here",
					"Nothing is built here: the image is built somewhere else and pulled. "+
						"Only --image and --start-command mean anything.")
			}
		}
	} else if c.Image != nil {
		return usage("--image does not apply to an application built from a repository",
			"build.not_an_image",
			"Its image is produced by the build. Change what is built with --method, "+
				"--dir and the filters.")
	}

	if !s.FromRegistry && !contains(BuildMethods, s.BuildMethod) {
		return usage(fmt.Sprintf("no build method called %q", s.BuildMethod),
			"build.method_invalid", "Use one of: %s.", strings.Join(BuildMethods, ", "))
	}
	if c.Directory != nil && strings.TrimSpace(*c.Directory) == "" {
		return usage("the build directory cannot be emptied", "build.directory_required",
			"The platform stores no empty directory and builds from the root when it is /. "+
				"Pass --dir / to build from the root.")
	}
	if c.Image != nil && strings.TrimSpace(*c.Image) == "" {
		return usage("the image cannot be emptied", "build.image_required",
			"An application that runs a prebuilt image has to have one.")
	}
	if len(s.IncludePaths) > MaxBuildFilter || len(s.IgnorePaths) > MaxBuildFilter {
		return usage(fmt.Sprintf("a build filter is longer than %d characters", MaxBuildFilter),
			"build.filter_too_long", "")
	}
	return nil
}

// SaveBuildSettings writes the whole set.
//
// Every field goes every time, because the endpoint writes what it is given and
// an omission is a change: no start command clears the start command, and no
// build method stores one that decodes to nothing.
func SaveBuildSettings(ctx context.Context, c *api.Client, appID string, s BuildSettings) error {
	body := map[string]any{
		"buildMethod":             buildMethodValue(s.BuildMethod),
		"directory":               emptyToNil(s.Directory),
		"startCommand":            emptyToNil(s.StartCommand),
		"buildFilterIncludePaths": emptyToNil(s.IncludePaths),
		"buildFilterIgnorePaths":  emptyToNil(s.IgnorePaths),
	}
	if s.FromRegistry {
		body["imageName"] = s.Image
	}
	return c.Put(ctx, "/AppSetting/UpdateBuildDeploySettings/"+appID, body, nil)
}

func emptyToNil(s string) any {
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return s
}

// SetDisplayName changes the label an application is shown under.
//
// Only the label. The name in the address is fixed at creation and no endpoint
// changes it, which is what the commands have to say plainly: somebody renaming
// an application usually wants the URL to follow, and it does not.
func SetDisplayName(ctx context.Context, c *api.Client, appID, displayName string) error {
	body := map[string]any{"displayName": displayName}
	return c.Put(ctx, "/AppSetting/UpdateDisplayName/"+appID, body, nil)
}

// MaxDisplayName is the server's limit.
const MaxDisplayName = 45

// CheckDisplayName mirrors the server's rule, which is stricter than it looks:
// letters, numbers, spaces and hyphens, and nothing else. An underscore or a
// dot is refused, which is worth saying before a round trip rather than after.
func CheckDisplayName(name string) error {
	n := strings.TrimSpace(name)
	switch {
	case n == "":
		return usage("a display name is needed", "app.display_name_required",
			"To remove one, set it to the application's own name.")
	case len(n) > MaxDisplayName:
		return usage(fmt.Sprintf("that display name is %d characters, and the limit is %d",
			len(n), MaxDisplayName), "app.display_name_invalid", "")
	case !isDisplayNameChars(n):
		return usage(fmt.Sprintf("%q has characters a display name cannot", n),
			"app.display_name_invalid",
			"Letters, numbers, spaces and hyphens only.")
	}
	return nil
}

func isDisplayNameChars(n string) bool {
	for _, r := range n {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == ' ', r == '-':
		default:
			return false
		}
	}
	return true
}
