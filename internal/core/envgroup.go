package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
)

// Shared sets of environment variables.
//
// A group is written once and assigned to any number of applications, which is
// the difference from the variables set on an application directly. The two
// coexist: an application sees its own variables and those of every group
// assigned to it, and its own win a clash.
//
// Two properties of the API shape the commands over them:
//
//   - Updating a group replaces it whole, meta and entries together. There is
//     no partial update, so changing one variable means sending all of them.
//     That is a read-modify-write, and unlike the per-application case there is
//     no merging endpoint to use instead.
//   - An assignment is its own record with its own id. Removing one needs that
//     id rather than the pair it joins, so the CLI looks it up from the
//     application.

// EnvGroup is a shared set of variables.
type EnvGroup struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`

	// UseInBuild and UseInRuntime say where the variables are injected. A
	// runtime group reaches the running container; a build group reaches the
	// build that produces its image. They are independent, and a group that is
	// neither reaches nothing.
	UseInBuild   bool `json:"useInBuild"`
	UseInRuntime bool `json:"useInRuntime"`

	Variables   int `json:"variables"`
	Assignments int `json:"assignments"`

	// Entries are the variables themselves. Only the detail endpoint fills
	// them in, and commands decide whether to show the values.
	Entries []EnvVar `json:"entries"`
}

type envGroupDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Description     string `json:"description"`
	UseInBuild      bool   `json:"useInBuild"`
	UseInRuntime    bool   `json:"useInRuntime"`
	VariableCount   int    `json:"variableCount"`
	AssignmentCount int    `json:"assignmentCount"`

	Entries []envVarDTO `json:"entries"`
}

// EnvGroupAssignment is one group attached to one application.
type EnvGroupAssignment struct {
	// ID is the assignment's own identifier, which is what removing one needs.
	ID string `json:"id"`

	GroupID   string `json:"groupId"`
	GroupName string `json:"groupName"`
	AppID     string `json:"appId"`
	AppName   string `json:"appName"`
	Variables int    `json:"variables"`
}

type assignmentDTO struct {
	AssignmentID         string `json:"assignmentId"`
	EnvVariableGroupID   string `json:"envVariableGroupId"`
	EnvVariableGroupName string `json:"envVariableGroupName"`
	AppID                string `json:"appId"`
	AppName              string `json:"appName"`
	VariableCount        int    `json:"variableCount"`
}

// ListEnvGroups returns the team's groups, sorted by name.
func ListEnvGroups(ctx context.Context, c *api.Client) ([]EnvGroup, error) {
	var dtos []envGroupDTO
	if err := c.Get(ctx, "/EnvVariableGroup/GetByTeamId", &dtos); err != nil {
		return nil, err
	}

	groups := make([]EnvGroup, 0, len(dtos))
	for _, d := range dtos {
		groups = append(groups, decodeEnvGroup(d))
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	return groups, nil
}

// GetEnvGroup returns one group, with its variables.
func GetEnvGroup(ctx context.Context, c *api.Client, groupID string) (EnvGroup, error) {
	var dto envGroupDTO
	if err := c.Get(ctx, "/EnvVariableGroup/GetById/"+groupID, &dto); err != nil {
		return EnvGroup{}, err
	}
	return decodeEnvGroup(dto), nil
}

func decodeEnvGroup(d envGroupDTO) EnvGroup {
	entries := make([]EnvVar, 0, len(d.Entries))
	for _, e := range d.Entries {
		entries = append(entries, EnvVar{ID: e.ID, Key: e.Key, Value: e.Value})
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Key < entries[j].Key })

	return EnvGroup{
		ID:           d.ID,
		Name:         d.Name,
		Description:  d.Description,
		UseInBuild:   d.UseInBuild,
		UseInRuntime: d.UseInRuntime,
		Variables:    d.VariableCount,
		Assignments:  d.AssignmentCount,
		Entries:      entries,
	}
}

// SaveEnvGroup creates a group, or replaces one whole.
//
// The same request body serves both, which is the API's shape and not a
// convenience: an update replaces the name, the description, the scope flags
// and every variable together. A caller changing one variable therefore has to
// send the rest, and the command that does it reads them first.
func SaveEnvGroup(ctx context.Context, c *api.Client, groupID string, g EnvGroup, values map[string]string) (EnvGroup, error) {
	body := map[string]any{
		"name":                 g.Name,
		"description":          g.Description,
		"useInBuild":           g.UseInBuild,
		"useInRuntime":         g.UseInRuntime,
		"environmentVariables": values,
	}

	var dto envGroupDTO
	if groupID == "" {
		if err := c.Post(ctx, "/EnvVariableGroup/Create", body, &dto); err != nil {
			return EnvGroup{}, err
		}
		return decodeEnvGroup(dto), nil
	}

	if err := c.Put(ctx, "/EnvVariableGroup/Update/"+groupID, body, &dto); err != nil {
		return EnvGroup{}, err
	}
	return decodeEnvGroup(dto), nil
}

// DeleteEnvGroup removes a group and every assignment of it.
func DeleteEnvGroup(ctx context.Context, c *api.Client, groupID string) error {
	return c.Delete(ctx, "/EnvVariableGroup/Delete/"+groupID, nil)
}

// AssignEnvGroup attaches a group to an application.
func AssignEnvGroup(ctx context.Context, c *api.Client, groupID, appID string) error {
	body := map[string]any{"envVariableGroupId": groupID, "appId": appID}
	return c.Post(ctx, "/EnvVariableGroup/Assign", body, nil)
}

// UnassignEnvGroup removes one assignment, by the assignment's own id.
func UnassignEnvGroup(ctx context.Context, c *api.Client, assignmentID string) error {
	return c.Delete(ctx, "/EnvVariableGroup/Unassign/"+assignmentID, nil)
}

// AppEnvGroups lists the groups assigned to an application.
//
// It is also how an assignment id is found: the pair of group and application
// does not identify one, and removing an assignment needs the record itself.
func AppEnvGroups(ctx context.Context, c *api.Client, appID string) ([]EnvGroupAssignment, error) {
	var dtos []assignmentDTO
	if err := c.Get(ctx, "/EnvVariableGroup/GetAssignedGroupsByAppId/"+appID, &dtos); err != nil {
		return nil, err
	}

	out := make([]EnvGroupAssignment, 0, len(dtos))
	for _, d := range dtos {
		out = append(out, EnvGroupAssignment{
			ID:        d.AssignmentID,
			GroupID:   d.EnvVariableGroupID,
			GroupName: d.EnvVariableGroupName,
			AppID:     d.AppID,
			AppName:   d.AppName,
			Variables: d.VariableCount,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GroupName < out[j].GroupName })
	return out, nil
}

// FindEnvGroup resolves a reference into exactly one group.
//
// Exact matching on the id and then the name, the same rule applications and
// volumes use: a near miss is reported rather than guessed, because the command
// being resolved for might be a deletion.
func FindEnvGroup(ctx context.Context, c *api.Client, ref string) (EnvGroup, error) {
	groups, err := ListEnvGroups(ctx, c)
	if err != nil {
		return EnvGroup{}, err
	}

	for _, g := range groups {
		if g.ID == ref {
			return g, nil
		}
	}

	var byName []EnvGroup
	for _, g := range groups {
		if g.Name == ref {
			byName = append(byName, g)
		}
	}

	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return EnvGroup{}, &EnvGroupNotFoundError{Ref: ref, Available: groupNames(groups)}
	default:
		return EnvGroup{}, &AmbiguousEnvGroupError{Ref: ref, Count: len(byName)}
	}
}

// EnvGroupNotFoundError carries what does exist.
type EnvGroupNotFoundError struct {
	Ref       string
	Available []string
}

func (e *EnvGroupNotFoundError) Error() string {
	return fmt.Sprintf("no variable group called %q in this team", e.Ref)
}

// AmbiguousEnvGroupError means two groups share a name.
type AmbiguousEnvGroupError struct {
	Ref   string
	Count int
}

func (e *AmbiguousEnvGroupError) Error() string {
	return fmt.Sprintf("%d variable groups are called %q", e.Count, e.Ref)
}

func groupNames(groups []EnvGroup) []string {
	names := make([]string, 0, len(groups))
	for _, g := range groups {
		names = append(names, g.Name)
	}
	return names
}

// CheckEnvGroupName rejects a name the server would refuse.
func CheckEnvGroupName(name string) error {
	switch {
	case strings.TrimSpace(name) == "":
		return usage("a variable group needs a name", "envgroup.name_required", "")
	case len(name) > 100:
		return usage(fmt.Sprintf("%q is longer than 100 characters", short(name)),
			"envgroup.name_invalid", "")
	}
	return nil
}

// EntryValues turns a group's entries back into the map an update has to send.
func EntryValues(entries []EnvVar) map[string]string {
	values := make(map[string]string, len(entries))
	for _, e := range entries {
		values[e.Key] = e.Value
	}
	return values
}
