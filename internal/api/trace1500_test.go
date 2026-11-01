package api

import (
	"encoding/json"
	"flag"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	pitwallv1 "github.com/those-who-run/contracts/gen/go/twr/pitwall/v1"
	"github.com/those-who-run/contracts/gen/go/twr/pitwall/v1/pitwallv1connect"
	storev1 "github.com/those-who-run/contracts/gen/go/twr/store/v1"

	"github.com/those-who-run/pitwall/internal/logbooktest"
)

// update writes trace1500 again from what the API answers: go test ./internal/api -run 1500 -update.
var update = flag.Bool("update", false, "write tests/fixtures/trace-1500.json again")

// trace1500 is a 1500-point trace as the API answered it over Connect's GET, which
// the web app's src/api/adapt.test.ts decodes (those-who-run/paddock#121 AC3, moved
// to #130): Go encodes, TypeScript decodes. The real laps the end-to-end store
// holds have about 150 points each (v1's recordings are thinned), so the 1500
// points are made here, from sums both languages compute to the same bits.
var trace1500 = filepath.Join("..", "..", "tests", "fixtures", "trace-1500.json")

// lapTrace1500 is the trace logbook answers: every channel 1500 points long, with
// a NaN in the rpm, which protobuf's JSON writes as "NaN".
func lapTrace1500() *storev1.LapTrace {
	tr := &storev1.LapTrace{SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA", Lap: 5}
	for i := range 1500 {
		f := float64(i)
		tr.T = append(tr.T, f/60)
		tr.SpeedKph = append(tr.SpeedKph, 100+float64(i%97)*0.5)
		tr.ThrottlePct = append(tr.ThrottlePct, float64(i*7%101))
		brake := 0.0
		if i%13 == 0 {
			brake = 50.5
		}
		tr.BrakePct = append(tr.BrakePct, brake)
		tr.Gear = append(tr.Gear, int32(1+i%6))
		rpm := 3000 + f*3.25
		if i == 7 {
			rpm = math.NaN()
		}
		tr.Rpm = append(tr.Rpm, rpm)
		tr.X = append(tr.X, f*0.1-50)
		tr.Z = append(tr.Z, -f*0.2)
	}
	return tr
}

func TestATraceOf1500PointsGoesToTypeScript(t *testing.T) {
	r := newRig(t, 0)
	r.logbook.Reply(logbooktest.LapsTrace, &storev1.GetTraceResponse{Trace: lapTrace1500(), DataVersion: "1"})
	resp := get(t, r, pitwallv1connect.PitwallServiceGetTraceProcedure,
		&pitwallv1.GetTraceRequest{SessionId: "01JB9QZ4V3X8M2K7T5R1N6PDWA", Lap: 5, Points: 1500}, nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	var got pitwallv1.GetTraceResponse
	if err := protojson.Unmarshal(body, &got); err != nil {
		t.Fatal(err)
	}
	if n := len(got.GetTrace().GetT()); n != 1500 {
		t.Fatalf("the trace has %d points, want 1500", n)
	}
	if *update {
		if err := os.WriteFile(trace1500, append(body, '\n'), 0o644); err != nil { //nolint:gosec // a fixture, read by the web app's tests
			t.Fatal(err)
		}
	}
	// The file the web app reads is this answer: the same message, field for field.
	b, err := os.ReadFile(trace1500) //nolint:gosec // a fixed fixture path
	if err != nil {
		t.Fatalf("%v (go test ./internal/api -run 1500 -update writes it)", err)
	}
	var want pitwallv1.GetTraceResponse
	if err := protojson.Unmarshal(b, &want); err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(&want, &got) {
		t.Errorf("%s is not what the API answers: go test ./internal/api -run 1500 -update", trace1500)
	}
	// And the same JSON, key for key: protojson reads proto names (speed_kph) as
	// well as JSON names (speedKph), so proto.Equal alone would pass a file, or an
	// answer, in the names the web app doesn't read. Not byte for byte: protojson
	// varies its whitespace.
	var wantJSON, gotJSON any
	if err := json.Unmarshal(b, &wantJSON); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &gotJSON); err != nil {
		t.Fatal(err)
	}
	if d := cmp.Diff(wantJSON, gotJSON); d != "" {
		t.Errorf("%s is not the API's JSON (-file +answer):\n%s\ngo test ./internal/api -run 1500 -update", trace1500, d)
	}
}
