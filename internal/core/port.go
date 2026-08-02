package core

import (
	"context"
	"fmt"
	"sort"

	"github.com/outplane/cli/internal/api"
)

// The ports an application serves.
//
// Two platform facts shape everything here, and the second is the reason this
// file exists at all rather than the commands calling the API directly.
//
// A port change does not reach the running application. AppPortService writes
// to the database and tracks an event; nothing refreshes the workload, so a
// port takes effect at the next deployment. That is the same rule environment
// variables follow, and the commands say it the same way.
//
// The API offers one write and it is a replacement: UpdateAppPorts deletes
// every port absent from the request. There is no add and no delete-one, which
// is exactly what environment variables do have, so the trick that keeps `env
// set` from touching a variable it was not told about is unavailable here. The
// merge happens in this file instead: read the current set, apply the change,
// send the result. MergePorts and WithoutPorts are the whole of that logic and
// are pure, so what a command is about to send can be tested without a server
// and shown to a reader by --dry-run.

// ListPorts returns the ports an application serves, lowest first.
//
// It reads the application detail, which is the only endpoint that carries
// them. Ports come back with their custom domains attached, and that is worth
// keeping: a reader deciding whether to remove a port wants to see what is
// pointed at it.
func ListPorts(ctx context.Context, c *api.Client, appID string) ([]Endpoint, error) {
	detail, err := GetApp(ctx, c, appID)
	if err != nil {
		return nil, err
	}

	ports := detail.Endpoints
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
	return ports, nil
}

// SetPorts replaces an application's ports with exactly this set.
//
// The name says replacement because that is what the request does. Callers
// wanting to change one port pass MergePorts' result, not one port.
func SetPorts(ctx context.Context, c *api.Client, appID string, ports []NewPort) error {
	body := map[string]any{"ports": portBodies(ports)}
	return c.Put(ctx, "/AppSetting/UpdateAppPorts/"+appID, body, nil)
}

// MergePorts is the full set to send when some ports are being changed.
//
// A port already there is replaced by the incoming one; a port that is not
// mentioned is carried through untouched. That carrying is the whole point:
// without it, adding a port would delete every other one.
func MergePorts(existing []Endpoint, incoming []NewPort) []NewPort {
	merged := make([]NewPort, 0, len(existing)+len(incoming))
	for _, e := range existing {
		merged = append(merged, NewPort{Port: e.Port, Scheme: e.Scheme, Public: e.Public})
	}

	for _, in := range incoming {
		replaced := false
		for i := range merged {
			if merged[i].Port == in.Port {
				merged[i] = in
				replaced = true
				break
			}
		}
		if !replaced {
			merged = append(merged, in)
		}
	}

	sortPorts(merged)
	return merged
}

// WithoutPorts is the full set to send when some ports are being removed.
//
// Every other port is carried through, for the same reason MergePorts carries
// them: the request that removes one port is the same request that would
// remove all of them.
func WithoutPorts(existing []Endpoint, numbers []int) []NewPort {
	drop := make(map[int]bool, len(numbers))
	for _, n := range numbers {
		drop[n] = true
	}

	kept := make([]NewPort, 0, len(existing))
	for _, e := range existing {
		if !drop[e.Port] {
			kept = append(kept, NewPort{Port: e.Port, Scheme: e.Scheme, Public: e.Public})
		}
	}

	sortPorts(kept)
	return kept
}

// FindPort looks up one port by number.
func FindPort(ports []Endpoint, number int) (Endpoint, bool) {
	for _, p := range ports {
		if p.Port == number {
			return p, true
		}
	}
	return Endpoint{}, false
}

// PortNumbers lists what an application serves, for an error that can say so.
func PortNumbers(ports []Endpoint) []string {
	out := make([]string, 0, len(ports))
	for _, p := range ports {
		out = append(out, fmt.Sprintf("%d", p.Port))
	}
	return out
}

// CheckPort rejects a port the server would, naming which rule it broke.
//
// The same rules `app create` applies, because they are the same rules: a port
// added later is not different from a port declared at creation, and having two
// answers to "is 70000 a port" would be a bug waiting for somebody to find it.
func CheckPort(p NewPort) error {
	return NewApp{Ports: []NewPort{p}}.checkPorts()
}

func sortPorts(ports []NewPort) {
	sort.Slice(ports, func(i, j int) bool { return ports[i].Port < ports[j].Port })
}
