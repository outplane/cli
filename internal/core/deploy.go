package core

import (
	"context"
	"fmt"

	"github.com/outplane/cli/internal/api"
)

// Deployment is one build-and-release of an application.
type Deployment struct {
	ID    int    `json:"deploymentId"`
	AppID string `json:"appId"`

	// Status is the decoded AppDeploymentStatus. An unrecognised value arrives
	// here as "unknown:N" and is never interpreted as either success or
	// failure; see WaitUntilFinished.
	Status string `json:"status"`

	Branch        string `json:"branch"`
	ImageRef      string `json:"imageRef"`
	CommitMessage string `json:"commitMessage"`
	StartedAt     string `json:"startedAt"`
	Duration      string `json:"duration"`
}

// deploymentDetailDTO is the wire shape of GET /AppDeployment/GetAppDeploymentById.
type deploymentDetailDTO struct {
	ID     int    `json:"id"`
	AppID  string `json:"appId"`
	Status int    `json:"status"`
	Branch string `json:"branch"`

	ImageName     string `json:"imageName"`
	CommitMessage string `json:"commitMessage"`
	CreatedDate   string `json:"createdDate"`

	// The server has already humanised this ("1m 20s"); there is no numeric
	// field beside it, so it is passed through rather than reformatted.
	DurationSecondsText string `json:"durationSecondsText"`
}

// triggerDTO is the request body. ImageName is omitted entirely when empty,
// because the server treats an absent value as "use the app's current source"
// and an empty string would be a different, invalid, statement.
type triggerDTO struct {
	ImageName string `json:"imageName,omitempty"`
}

// CreateDeployment starts a build and returns its id.
//
// The id is all the server returns: the create endpoint's result is a bare
// integer. Everything a caller wants to show about the new deployment, its
// status and when it started, comes from GetDeployment. That is one extra
// request, and it is worth it: reporting a status the CLI assumed rather than
// read is how "queued" becomes a false "ready".
func CreateDeployment(ctx context.Context, c *api.Client, appID, imageRef string) (int, error) {
	var id int
	path := "/AppDeployment/CreateAppDeployment/" + appID
	if err := c.Post(ctx, path, triggerDTO{ImageName: imageRef}, &id); err != nil {
		return 0, err
	}
	return id, nil
}

// GetDeployment reads one deployment's current state.
func GetDeployment(ctx context.Context, c *api.Client, appID string, id int) (Deployment, error) {
	var dto deploymentDetailDTO
	path := fmt.Sprintf("/AppDeployment/GetAppDeploymentById/%s/%d", appID, id)
	if err := c.Get(ctx, path, &dto); err != nil {
		return Deployment{}, err
	}

	return Deployment{
		ID:            dto.ID,
		AppID:         dto.AppID,
		Status:        deploymentStatusNames.name(dto.Status),
		Branch:        dto.Branch,
		ImageRef:      dto.ImageName,
		CommitMessage: dto.CommitMessage,
		StartedAt:     serverInstant(dto.CreatedDate),
		Duration:      dto.DurationSecondsText,
	}, nil
}

// LogChunk is one slice of a build log, plus where to resume from.
type LogChunk struct {
	Text string

	// Offset is the byte position to send with the next request. Build logs
	// are polled by offset rather than streamed, so this is what makes the
	// next call return only what is new.
	Offset int64

	// ProcessStatus is the build process's own state, reported alongside the
	// text. It is not the deployment status and is not decoded here.
	ProcessStatus string
}

type buildLogDTO struct {
	Logs          string `json:"logs"`
	BytePosition  int64  `json:"bytePosition"`
	ProcessStatus string `json:"processStatus"`
}

// BuildLogs fetches the part of a build log after offset.
//
// Pass the Offset from the previous chunk to get only what is new; pass zero
// to start from the beginning.
func BuildLogs(ctx context.Context, c *api.Client, id int, offset int64) (LogChunk, error) {
	path := fmt.Sprintf("/AppDeployment/GetAppDeploymentBuildLogs/%d/%d", id, offset)

	var dto buildLogDTO
	if err := c.Get(ctx, path, &dto); err != nil {
		return LogChunk{}, err
	}
	return LogChunk{Text: dto.Logs, Offset: dto.BytePosition, ProcessStatus: dto.ProcessStatus}, nil
}

// finishedStates are the deployment statuses that will not change again.
//
// Deliberately a list of what IS final rather than what is not. Anything else,
// including a status this release has never heard of, counts as still running,
// so a server that learns a new state makes the CLI wait rather than declare a
// result it guessed. Waiting too long fails loudly with a timeout; guessing
// wrong turns a red pipeline green.
var finishedStates = map[string]bool{
	"ready":    true,
	"failed":   true,
	"crashed":  true,
	"canceled": true,
}

// Finished reports whether a deployment has reached a state it will not leave.
func Finished(status string) bool { return finishedStates[status] }

// Succeeded reports whether a deployment finished well.
//
// Separate from Finished because the two questions have different wrong
// answers: treating an unknown state as finished stops a wait early, and
// treating one as successful reports a deploy that may have failed.
func Succeeded(status string) bool { return status == "ready" }
