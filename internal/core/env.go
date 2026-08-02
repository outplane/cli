package core

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/outplane/cli/internal/api"
	"github.com/outplane/cli/internal/clierr"
)

// Environment variables set directly on an application.
//
// Two things about the platform shape everything here.
//
// A change does not reach the running application. The API stores it and
// nothing else happens: the workload is only refreshed by scaling and pausing,
// so a variable takes effect at the next deployment. The console says this with
// a "Save" and a "Save and deploy" button; the CLI says it with --deploy and an
// automation note, because a value that was set and quietly did nothing is the
// worst outcome available here.
//
// Nothing in this file reads the whole set and writes it back. The API offers a
// full replacement, and the console uses it, but a replacement is a read-modify-
// write over shared state with no version check: two agents setting two
// different keys at the same time would each save the set they read, and one of
// them would silently lose. Adding merges on the server and removing names a
// single variable, so neither can lose a key it never mentioned.

// EnvVar is one variable set on an application.
type EnvVar struct {
	// ID is the variable's own identifier, which removal needs. It is an
	// integer rather than a GUID: this is the one table in the API that
	// numbers its rows.
	ID int `json:"id"`

	Key   string `json:"key"`
	Value string `json:"value"`
}

type envVarDTO struct {
	ID    int    `json:"id"`
	Key   string `json:"key"`
	Value string `json:"value"`
}

// ListEnv returns every variable set on an application, sorted by key.
//
// The order is the CLI's, not the server's, because the server returns
// insertion order and a list that reshuffles itself every time somebody adds a
// variable cannot be diffed between two runs.
func ListEnv(ctx context.Context, c *api.Client, appID string) ([]EnvVar, error) {
	var dtos []envVarDTO
	if err := c.Get(ctx, "/AppSetting/GetEnvironmentVariables/"+appID, &dtos); err != nil {
		return nil, err
	}

	vars := make([]EnvVar, 0, len(dtos))
	for _, d := range dtos {
		vars = append(vars, EnvVar{ID: d.ID, Key: d.Key, Value: d.Value})
	}
	sort.Slice(vars, func(i, j int) bool { return vars[i].Key < vars[j].Key })
	return vars, nil
}

// SetEnv adds or replaces variables, leaving every other key alone.
//
// The merge happens on the server, which is what keeps two callers setting two
// different keys from overwriting each other.
func SetEnv(ctx context.Context, c *api.Client, appID string, values map[string]string) error {
	body := map[string]any{"environmentVariables": values}
	return c.Post(ctx, "/AppSetting/AddEnvironmentVariables/"+appID, body, nil)
}

// UnsetEnv removes one variable by its own id.
func UnsetEnv(ctx context.Context, c *api.Client, appID string, id int) error {
	return c.Delete(ctx, fmt.Sprintf("/AppSetting/DeleteEnvironmentVariable/%s/%d", appID, id), nil)
}

// FindEnv looks up a variable by key.
//
// Matching is case-insensitive because the server compares that way when it
// decides whether an assignment replaces an existing variable. Reading and
// writing have to agree about what counts as the same key, or `env set path=x`
// would replace PATH while `env get path` reported nothing.
func FindEnv(vars []EnvVar, key string) (EnvVar, bool) {
	for _, v := range vars {
		if strings.EqualFold(strings.TrimSpace(v.Key), strings.TrimSpace(key)) {
			return v, true
		}
	}
	return EnvVar{}, false
}

// Limits the server enforces. They are duplicated here so that a violation is
// reported before a request is sent, naming the key that broke the rule; the
// server's own message names none of them.
const (
	MaxEnvVars     = 250
	MaxEnvKeyLen   = 500
	MaxEnvValueLen = 5000
)

// reservedPrefixes and reservedKeys are what the platform keeps for itself.
//
// PORT is deliberately not here. It looks like it should be reserved and is
// not: an application may set its own, and refusing it would break the most
// commonly set variable there is.
var (
	reservedPrefixes = []string{"OP_", "KUBERNETES_"}
	reservedKeys     = []string{"HOSTNAME"}
)

// CheckEnvKey rejects a key the server would refuse, or explains why.
func CheckEnvKey(key string) error {
	k := strings.TrimSpace(key)

	if k == "" {
		return clierr.New(clierr.KindUsage, "an environment variable needs a name").
			WithCode("env.empty_key")
	}
	if len(k) > MaxEnvKeyLen {
		return clierr.New(clierr.KindUsage, "the name %q is longer than %d characters", short(k), MaxEnvKeyLen).
			WithCode("env.key_too_long")
	}

	for _, reserved := range reservedKeys {
		if strings.EqualFold(k, reserved) {
			return clierr.New(clierr.KindUsage, "%s is reserved by the container runtime", reserved).
				WithCode("env.reserved_key").
				WithHint("The platform sets it, and an application that overrode it would not " +
					"be able to find itself on the network.")
		}
	}
	for _, prefix := range reservedPrefixes {
		if strings.HasPrefix(strings.ToUpper(k), prefix) {
			return clierr.New(clierr.KindUsage, "names starting with %s are reserved by the platform", prefix).
				WithCode("env.reserved_prefix").
				WithDetail("key", k)
		}
	}
	return nil
}

// CheckEnvValue rejects a value the server would refuse.
//
// The value itself never appears in the message. An error about a credential
// that prints the credential is a leak on the one path where somebody is
// already watching the screen.
func CheckEnvValue(key, value string) error {
	if len(value) > MaxEnvValueLen {
		return clierr.New(clierr.KindUsage,
			"the value for %s is %d characters, and the limit is %d",
			strings.TrimSpace(key), len(value), MaxEnvValueLen).
			WithCode("env.value_too_long")
	}
	return nil
}

func short(s string) string {
	if len(s) <= 40 {
		return s
	}
	return s[:40] + "…"
}
