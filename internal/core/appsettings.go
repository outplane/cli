package core

import (
	"context"
	"fmt"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Changing how an application runs: how many instances, how large, and whether
// it runs at all.
//
// Both endpoints refresh the running workload, which makes them different from
// every other setting on the platform: an environment variable waits for the
// next deployment, while these take effect immediately.

// Pause stops an application, or starts it again.
//
// The replica count is untouched. Pausing derives the workload's replicas from
// the flag rather than writing zero, which is why resuming returns to whatever
// scale was configured rather than to one.
func Pause(ctx context.Context, c *api.Client, appID string, paused bool) error {
	body := map[string]any{"isPaused": paused}
	return c.Put(ctx, "/AppSetting/UpdatePauseState/"+appID, body, nil)
}

// Scale sets the replica count and the instance size together.
//
// Together is not a convenience: the endpoint replaces both, and its request
// has a default of one replica, so sending a size alone would quietly scale the
// application down to a single instance. Callers therefore have to supply the
// current value for whichever one they are not changing, which the application
// list already reports.
func Scale(ctx context.Context, c *api.Client, appID string, instances int, instanceType string) error {
	body := map[string]any{"minScale": instances, "instanceType": instanceType}
	return c.Put(ctx, "/AppSetting/UpdateScaleSettings/"+appID, body, nil)
}

// Instance is one running copy of an application.
type Instance struct {
	Name string `json:"name"`

	// Phase is the runtime's own word for what the instance is doing:
	// Pending, Running, Succeeded, Failed or Unknown. It is passed through
	// rather than translated, because it is a term a reader will find in every
	// other tool they use.
	Phase string `json:"phase"`

	// Ready is whether it is accepting traffic. An instance can be Running and
	// not ready, which is exactly the state worth seeing during a rollout.
	Ready bool `json:"ready"`

	// State is the platform's own reading of the lifecycle, and the field to
	// branch on. Phase is the runtime's raw word; this is what the platform
	// makes of it, and it distinguishes "starting" from "failing", which phase
	// alone cannot.
	State string `json:"state"`

	// RestartCount is how many times the container has restarted since the
	// instance was created. It is the only field that shows a crash loop which
	// has since recovered: everything else about that instance reads healthy.
	RestartCount int `json:"restartCount"`

	// Reason says why it is not up, in one sentence. Empty while it is running
	// normally or simply still starting.
	Reason string `json:"reason"`

	Container string `json:"container"`

	// CreatedAt is when the instance came into being, and StartedAt is when the
	// container running now started. They are seconds apart on a healthy
	// instance and hours apart on one that has restarted, which is the whole
	// reason both are reported: "up for" measures from the second.
	CreatedAt string `json:"createdAt"`
	StartedAt string `json:"startedAt"`

	// LastExitCode is what the previous container exited with, when there was
	// one. It is the application's own code, so 137 means it was killed for
	// using more memory than it was allowed.
	LastExitCode *int `json:"lastExitCode"`

	// DeploymentID ties an instance back to the row in `deploy list` that put
	// it there. Zero on an instance older than the platform variables.
	DeploymentID int `json:"deploymentId"`
}

type instanceDTO struct {
	Name          string `json:"name"`
	Phase         string `json:"phase"`
	Ready         bool   `json:"ready"`
	ContainerName string `json:"containerName"`
	CreatedAt     string `json:"createdAt"`
	StartedAt     string `json:"startedAt"`
	State         int    `json:"state"`
	RestartCount  int    `json:"restartCount"`
	Reason        string `json:"reason"`
	LastExitCode  *int   `json:"lastExitCode"`
	DeploymentID  int    `json:"deploymentId"`
}

// instanceStates is the platform's lifecycle vocabulary, by the number the API
// sends. Spelled out rather than derived, because the numbers are a contract
// and a gap in them is deliberate room to insert a state later.
var instanceStates = map[int]string{
	0:  "unknown",
	10: "pending",
	20: "starting",
	30: "running",
	40: "failing",
	50: "terminating",
	60: "terminated",
}

// instanceState names a state, and says plainly when it cannot.
//
// A number this release has never seen becomes "unknown:N" rather than a guess
// or a blank. That is how a state added on the server reaches an older CLI: as
// something visibly unrecognised, carrying the number somebody can look up.
func instanceState(code int) string {
	if name, ok := instanceStates[code]; ok {
		return name
	}
	return fmt.Sprintf("unknown:%d", code)
}

// AppInstances lists what is currently running.
//
// This is the live answer, from the cluster rather than from the database, so
// it disagrees with the configured replica count exactly when something is
// wrong: during a rollout, while an instance is restarting, or when one cannot
// be scheduled.
func AppInstances(ctx context.Context, c *api.Client, appID string) ([]Instance, error) {
	var dtos []instanceDTO
	// The action is GetInstances, not GetAppInstances: the route is named after
	// the controller method, and the service method it calls has a longer name.
	if err := c.Get(ctx, "/App/GetInstances/"+appID, &dtos); err != nil {
		return nil, err
	}

	out := make([]Instance, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, Instance{
			Name:         d.Name,
			Phase:        d.Phase,
			Ready:        d.Ready,
			State:        instanceState(d.State),
			RestartCount: d.RestartCount,
			Reason:       d.Reason,
			Container:    d.ContainerName,
			CreatedAt:    serverInstant(d.CreatedAt),
			StartedAt:    serverInstant(d.StartedAt),
			LastExitCode: d.LastExitCode,
			DeploymentID: d.DeploymentID,
		})
	}
	return out, nil
}

// CheckScale rejects a request the server would refuse, naming the field.
func CheckScale(instances int, instanceType string) error {
	if instances < 1 || instances > 5 {
		return usage(fmt.Sprintf("%d instances, and the range is 1 to 5", instances),
			"app.instances_invalid", "")
	}
	if !contains(InstanceTypes, instanceType) {
		return usage(fmt.Sprintf("no instance size called %q", instanceType), "app.size_invalid",
			"Use one of: %s.", strings.Join(InstanceTypes, ", "))
	}
	return nil
}
