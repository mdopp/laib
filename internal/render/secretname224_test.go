package render

import (
	"maps"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mdopp/laib/internal/state"
)

// podman refuses a secret name outside this pattern or past 253 characters
// (containers/common: "only 253 [a-zA-Z0-9-_.] characters allowed, and the
// start and end character must be [a-zA-Z0-9]").
var podmanName = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]*[a-zA-Z0-9]$`)

func shareBox(units map[string]string, values map[string]state.Value) *state.State {
	return &state.State{Box: state.Box{Domain: "x.home", DataDir: "/d"}, Services: map[string]*state.Service{
		"share": {Name: "share", Values: values, Units: units}}}
}

// #224 review: the podman secret's name carries the env key, and a key can
// come from a value (file-share's ACCOUNT_${SHARE_USER}). Whatever the key,
// the name is one podman takes, and two keys never share a name: a clash
// would hand one container the other's secret.
func TestPodmanSecretNamesAreValidAndDistinct(t *testing.T) {
	values := map[string]state.Value{"U1": {Literal: "jörg"}, "U2": {Literal: "jürg"}, "U3": {Literal: "x.K"}, "U4": {Literal: strings.Repeat("A", 260)}, "U5": {Literal: ""}}
	secrets := map[string]string{}
	for i := range 8 {
		n := "PW" + string(rune('1'+i))
		values[n] = state.Value{Secret: n}
		secrets[n] = "pw-" + n + "-v224"
	}
	units := map[string]string{
		"a.container": "[Container]\nImage=x\n" +
			"Environment=ACCOUNT_${U1}=${PW1}\n" + // podman refuses ö
			"Environment=ACCOUNT_${U2}=${PW2}\n" + // ü: a mapping to _ would clash with ö
			"Environment=B_${U3}=${PW3}\n" + // laib.share.a.B_x.K …
			"Environment=L${U4}=${PW4}\n" + // past 253 characters
			"Environment=ACCOUNT_${U5}=${PW5}\n" + // ends in _
			"Environment=TOKEN=${PW7}\n",
		"a.B_x.container": "[Container]\nImage=x\nEnvironment=K=${PW6}\n",             // … and laib.share.a.B_x.K again
		"b.container":     "[Container]\nImage=x\nEnvironment=ACCOUNT_${U1}=${PW8}\n", // a.container's key, another container
	}
	st := shareBox(units, values)
	out, err := Render(st, secrets)
	if err != nil {
		t.Fatal(err)
	}
	sec := PodmanSecrets(st, secrets)
	if got := slices.Sorted(maps.Values(sec)); len(got) != 8 {
		t.Fatalf("eight secret-backed lines, eight podman secrets, got %d: %v", len(sec), slices.Collect(maps.Keys(sec)))
	}
	for name := range sec {
		if len(name) > 253 || !podmanName.MatchString(name) || !strings.HasPrefix(name, "laib.share.") {
			t.Errorf("podman refuses this secret name, or it is not laib's: %q", name)
		}
	}
	if sec["laib.share.a.TOKEN"] != secrets["PW7"] {
		t.Errorf("a plain key keeps its readable name laib.<service>.<container>.<KEY>: %v", slices.Collect(maps.Keys(sec)))
	}
	seen := asContainerSees(st, secrets, out)
	a, b := seen["quadlet/share/a.container"], seen["quadlet/share/a.B_x.container"]
	for _, want := range []string{"ACCOUNT_jörg=" + secrets["PW1"], "ACCOUNT_jürg=" + secrets["PW2"], "B_x.K=" + secrets["PW3"], "L" + strings.Repeat("A", 260) + "=" + secrets["PW4"], "ACCOUNT_=" + secrets["PW5"], "TOKEN=" + secrets["PW7"]} {
		if !strings.Contains(a, "\nEnvironment="+want+"\n") {
			t.Errorf("a.container's container does not get %s:\n%s", want, a)
		}
	}
	if !strings.Contains(b, "\nEnvironment=K="+secrets["PW6"]+"\n") {
		t.Errorf("a.B_x.container's container does not get its own K:\n%s", b)
	}
	if c := seen["quadlet/share/b.container"]; !strings.Contains(c, "\nEnvironment=ACCOUNT_jörg="+secrets["PW8"]+"\n") {
		t.Errorf("b.container's container does not get its own ACCOUNT_jörg:\n%s", c)
	}
}

// #224 review: no shape of an Environment= line lets a secret value stay on
// podman run's argv, and the container gets the value systemd would have
// handed it: quotes are systemd's, not part of the value.
func TestEveryEnvironmentShapeKeepsTheSecretOffArgv(t *testing.T) {
	secrets := map[string]string{"PW": "pw-v224"}
	values := map[string]state.Value{"PW": {Secret: "PW"}}
	for line, want := range map[string]string{
		"Environment = TOKEN=${PW}":     "TOKEN=pw-v224", // systemd takes spaces around =
		"Environment=TOKEN=\"${PW}\"":   "TOKEN=pw-v224",
		"Environment='TOKEN=${PW}'":     "TOKEN=pw-v224",
		"Environment=\"TOKEN=a ${PW}\"": "TOKEN=a pw-v224",
	} {
		st := shareBox(map[string]string{"a.container": "[Container]\nImage=x\n" + line + "\n"}, values)
		out, err := Render(st, secrets)
		if err != nil {
			t.Fatalf("%s: %v", line, err)
		}
		if unit := out["quadlet/share/a.container"]; strings.Contains(unit, "pw-v224") {
			t.Errorf("%s: the value is in the unit, so on podman run's argv:\n%s", line, unit)
		}
		if got := asContainerSees(st, secrets, out)["quadlet/share/a.container"]; !strings.Contains(got, "\nEnvironment="+want+"\n") {
			t.Errorf("%s: the container must get %s:\n%s", line, want, got)
		}
	}
	// the builtin MODEL_KEY is a secret too
	st := shareBox(map[string]string{"a.container": "[Container]\nImage=x\nEnvironment=MODEL_KEY=${MODEL_KEY}\n"}, values)
	st.Box.Model = state.Model{URL: "http://m/v1", KeySecret: "PW"}
	out, err := Render(st, secrets)
	if err != nil || strings.Contains(out["quadlet/share/a.container"], "pw-v224") {
		t.Errorf("MODEL_KEY's value is on podman run's argv (%v):\n%s", err, out["quadlet/share/a.container"])
	}
	// what podman cannot take as one secret is red, never a fallback to Environment=
	for _, bad := range []struct{ line, user string }{
		{"Environment=A=1 TOKEN=${PW}", "x"},      // two assignments: which one is the secret's?
		{"Environment=ACCOUNT_${U}=${PW}", "a,b"}, // podman splits --secret at commas
		{"Environment=ACCOUNT_${U}=${PW}", "a b"},
		{"Environment=${U}=${PW}", ""}, // no key at all // Quadlet splits Secret= at spaces
	} {
		st := shareBox(map[string]string{"a.container": "[Container]\nImage=x\n" + bad.line + "\n"}, map[string]state.Value{"PW": {Secret: "PW"}, "U": {Literal: bad.user}})
		if out, err := Render(st, secrets); err == nil {
			t.Errorf("%s (U=%q) rendered, though podman cannot take it as one secret:\n%s", bad.line, bad.user, out["quadlet/share/a.container"])
		}
	}
}
