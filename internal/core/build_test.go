package core

import "testing"

// Apply decides what is sent to an endpoint that writes every field it is
// given, so a field it fails to carry over is a field that gets cleared. These
// hold it to carrying everything nobody mentioned.

func ptr(s string) *string { return &s }

func gitSettings() BuildSettings {
	return BuildSettings{
		BuildMethod:  "dockerfile",
		Directory:    "/api",
		StartCommand: "node server.js",
		IncludePaths: "src/**",
		IgnorePaths:  "docs/**",
	}
}

func TestApplyKeepsWhatWasNotMentioned(t *testing.T) {
	before := gitSettings()

	t.Run("one field at a time", func(t *testing.T) {
		got := before.Apply(BuildChange{BuildMethod: ptr("buildpack")})
		if got.BuildMethod != "buildpack" {
			t.Errorf("build method is %q", got.BuildMethod)
		}
		want := before
		want.BuildMethod = "buildpack"
		if got != want {
			t.Errorf("something else changed:\n got %+v\nwant %+v", got, want)
		}
	})

	t.Run("nothing mentioned changes nothing", func(t *testing.T) {
		if got := before.Apply(BuildChange{}); got != before {
			t.Errorf("got %+v, want %+v", got, before)
		}
	})

	t.Run("an empty value is a value", func(t *testing.T) {
		got := before.Apply(BuildChange{StartCommand: ptr(""), IgnorePaths: ptr("")})
		if got.StartCommand != "" || got.IgnorePaths != "" {
			t.Errorf("clearing did not clear: %+v", got)
		}
		if got.IncludePaths != before.IncludePaths || got.Directory != before.Directory {
			t.Errorf("clearing cleared something else: %+v", got)
		}
	})

	t.Run("several at once", func(t *testing.T) {
		got := before.Apply(BuildChange{
			BuildMethod: ptr("buildpack"),
			Directory:   ptr("/"),
		})
		if got.BuildMethod != "buildpack" || got.Directory != "/" {
			t.Errorf("got %+v", got)
		}
		if got.StartCommand != before.StartCommand {
			t.Errorf("start command was lost: %q", got.StartCommand)
		}
	})
}

func TestCheckRejectsWhatTheServerWouldDrop(t *testing.T) {
	git := gitSettings()
	registry := BuildSettings{FromRegistry: true, BuildMethod: "prebuilt-image", Image: "nginx:1"}

	cases := []struct {
		name    string
		before  BuildSettings
		change  BuildChange
		refused bool
	}{
		{"a repository takes a build method", git, BuildChange{BuildMethod: ptr("buildpack")}, false},
		{"a repository takes no image", git, BuildChange{Image: ptr("nginx:1")}, true},
		{"an image takes no build method", registry, BuildChange{BuildMethod: ptr("dockerfile")}, true},
		{"an image takes no directory", registry, BuildChange{Directory: ptr("/api")}, true},
		{"an image takes no filters", registry, BuildChange{IncludePaths: ptr("src/**")}, true},
		{"an image takes a start command", registry, BuildChange{StartCommand: ptr("nginx -g daemon off;")}, false},
		{"an image takes an image", registry, BuildChange{Image: ptr("nginx:2")}, false},
		{"an unknown build method", git, BuildChange{BuildMethod: ptr("magic")}, true},
		{"prebuilt-image is not a choice for a repository", git, BuildChange{BuildMethod: ptr("prebuilt-image")}, true},
		{"the directory cannot be emptied", git, BuildChange{Directory: ptr("")}, true},
		{"the image cannot be emptied", registry, BuildChange{Image: ptr("")}, true},
		{"the start command can be emptied", git, BuildChange{StartCommand: ptr("")}, false},
		{"a filter can be emptied", git, BuildChange{IncludePaths: ptr("")}, false},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := c.before.Apply(c.change).Check(c.change)
			if c.refused && err == nil {
				t.Fatalf("accepted %+v", c.change)
			}
			if !c.refused && err != nil {
				t.Fatalf("refused: %v", err)
			}
		})
	}
}

func TestCheckFilterLength(t *testing.T) {
	long := make([]byte, MaxBuildFilter+1)
	for i := range long {
		long[i] = 'a'
	}
	change := BuildChange{IncludePaths: ptr(string(long))}
	if err := gitSettings().Apply(change).Check(change); err == nil {
		t.Fatal("a filter over the limit was accepted")
	}
}

func TestCheckDisplayName(t *testing.T) {
	valid := []string{"Checkout", "checkout api", "Checkout-API", "a", "A1 - b2"}
	for _, n := range valid {
		if err := CheckDisplayName(n); err != nil {
			t.Errorf("%q was refused: %v", n, err)
		}
	}

	invalid := []string{"", "   ", "checkout_api", "checkout.api", "çekirdek", "emoji 🚀",
		string(make([]byte, MaxDisplayName+1))}
	for _, n := range invalid {
		if err := CheckDisplayName(n); err == nil {
			t.Errorf("%q was accepted", n)
		}
	}
}
