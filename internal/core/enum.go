package core

import "fmt"

// Decoding for the API's integer enums.
//
// The API serialises every enum as its numeric value rather than its name, so a
// response says `"status": 50` and never `"status": "Ready"`. Decoding happens
// here, at the boundary, so that no command, no format and no user ever has to
// see a bare integer.
//
// The numbers are transcribed from the API's own enum declarations, which are
// the only definition of them that exists: there is no schema to generate this
// from. They are deliberately non-sequential, with gaps left so that a new
// state can be inserted between two existing ones, so they must be copied
// exactly and never renumbered to tidy them up.

// enumNames maps a wire value to the stable string the CLI prints.
type enumNames map[int]string

// name decodes a wire value, or describes it honestly if it is not known.
//
// An unrecognised value becomes "unknown:70" rather than an error. That is the
// whole point of this indirection: a server that has learned a new state must
// not break a CLI released before it, and the decoded string still tells a
// reader exactly what happened and what to search the release notes for.
// Erroring here would turn a purely additive server change into a broken
// client, which is the failure mode this type exists to prevent.
func (e enumNames) name(v int) string {
	if s, ok := e[v]; ok {
		return s
	}
	return fmt.Sprintf("unknown:%d", v)
}

// Deployment states, from AppDeploymentStatus.
//
// Canceled is the one that surprises people: a deployment superseded by a newer
// push ends as Canceled, not Failed. Nothing went wrong.
var deploymentStatusNames = enumNames{
	0:  "canceled",
	5:  "queued",
	10: "building",
	20: "failed",
	30: "deploying",
	50: "ready",
	60: "crashed",
}

// How an image is produced, from AppBuildMethodType.
var buildMethodNames = enumNames{
	10: "dockerfile",
	20: "buildpack",
	30: "prebuilt-image",
}

// How a port is served, from EndpointScheme.
var schemeNames = enumNames{
	10: "http",
	20: "h2c",
	30: "tcp",
}

// Where an app's source comes from, from SourceProvider.
//
// The gap between 10 and 200 is where two further providers are declared and
// commented out in the API. They are not supported, so they are not listed
// here; if one is ever enabled it arrives as "unknown:50" until this map
// catches up, which is the intended behaviour rather than a bug to pre-empt.
var sourceProviderNames = enumNames{
	10:  "github",
	200: SourceContainerRegistry,
}

// SourceContainerRegistry is the decoded provider that deploys a ready-made
// image rather than building one.
//
// Several commands branch on it: an image reference means something only for
// this source, a failed deployment of one has no build output to read, and its
// "repository" is an image name. It is a constant so that those three places
// cannot disagree about the spelling.
const SourceContainerRegistry = "container-registry"
