package core

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/outplane/cli/internal/api"
)

// A volume is reported against its claim, and comes back keyed by the volume id
// the claim was named after, in the case the API writes ids in. One with no
// capacity on record has no percentage rather than a fictional one.
func TestQueryVolumeUsageKeysByVolumeID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := func(claim, v string) string {
			return fmt.Sprintf(`{"metric":{"persistentvolumeclaim":%q},"value":[1700000000,%q]}`, claim, v)
		}
		var result []string
		if strings.Contains(r.URL.Query().Get("query"), "capacity_bytes") {
			result = []string{value("pvc-0A1B2C3D-0000-4000-8000-00000000000A", "1000")}
		} else {
			result = []string{
				value("pvc-0A1B2C3D-0000-4000-8000-00000000000A", "250"),
				value("pvc-0a1b2c3d-0000-4000-8000-00000000000b", "40"),
				value("pvc-0a1b2c3d-0000-4000-8000-00000000000c", "NaN"),
			}
		}
		fmt.Fprintf(w, `{"status":"success","data":{"resultType":"vector","result":[%s]}}`, strings.Join(result, ","))
	}))
	defer server.Close()

	got, err := QueryVolumeUsage(context.Background(), api.New(api.Config{}), server.URL)
	if err != nil {
		t.Fatal(err)
	}

	full, ok := got["0a1b2c3d-0000-4000-8000-00000000000a"]
	if !ok || full.UsedBytes != 250 || full.CapacityBytes != 1000 || full.Percent == nil || *full.Percent != 25 {
		t.Errorf("measured volume %+v (found %v), want 250 of 1000, 25%%", full, ok)
	}
	if part := got["0a1b2c3d-0000-4000-8000-00000000000b"]; part.UsedBytes != 40 || part.Percent != nil {
		t.Errorf("volume without a capacity %+v, want 40 used and no percentage", part)
	}
	if _, ok := got["0a1b2c3d-0000-4000-8000-00000000000c"]; ok {
		t.Error("a NaN sample was reported as a reading")
	}
}
