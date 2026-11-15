package render

import (
	"strings"
	"testing"
)

// Ablauf L-54. laib#1037: with every shipped template installed and nothing typed in
// homes' values.yml, Homes' unit carries Reitti's address from Reitti's
// own route; the template no longer sets HOMES_REITTI_URL from REITTI_URL
// itself, which would override it with "".
func TestHomesFindsReittiWithNothingTyped(t *testing.T) {
	st := shippedBox(t)
	if st.Services["homes"].Set("REITTI_URL") {
		t.Fatal("values.example.yml sets REITTI_URL: the found address is never used")
	}
	out, err := Render(st, nil)
	if err != nil {
		t.Fatal(err)
	}
	homes := out["quadlet/homes/homes.container"]
	if strings.Count(homes, "Environment=HOMES_REITTI_URL=") != 1 || !strings.Contains(homes, "\nEnvironment=HOMES_REITTI_URL=http://host.containers.internal:8730\n") {
		t.Fatalf("homes.container must carry Reitti's address once:\n%s", homes)
	}
}
