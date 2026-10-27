//go:build linux

package operator

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// probeSkills is a skills folder with the real laibskill.py and one
// built-in skill "probe" whose run.py is the given script.
func probeSkills(t *testing.T, script string) string {
	t.Helper()
	dir := t.TempDir()
	door, err := os.ReadFile("../skills/laibskill.py")
	if err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "laibskill.py"), door, 0o644)
	os.MkdirAll(filepath.Join(dir, "probe"), 0o755)
	os.WriteFile(filepath.Join(dir, "probe", "run.py"), []byte(script), 0o644)
	return dir
}

// headerMCP is a laib MCP that records each call with the skill it was made in.
func headerMCP(t *testing.T) (*httptest.Server, *[]string) {
	t.Helper()
	var mu sync.Mutex
	var calls []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Method string
			Params struct {
				Name      string
				Arguments map[string]any
			}
		}
		json.NewDecoder(r.Body).Decode(&req)
		mu.Lock()
		calls = append(calls, r.Header.Get("X-Laib-Skill")+" "+req.Method+" "+req.Params.Name+" "+shownArgs(req.Params.Arguments))
		mu.Unlock()
		text := "ok"
		if req.Params.Name == "laib_inbox" {
			text = "[]"
		}
		json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": 1, "result": map[string]any{"content": []map[string]string{{"type": "text", "text": text}}}})
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func probeSkill() Skill {
	return Skill{Name: "probe", Source: "builtin", Status: "active", Run: "run.py", Trigger: SkillTrigger{Daily: "06:30"},
		Tools: []string{"status", "wish", "note"}, Granted: []string{"status", "wish", "note"}, Limits: map[string]int{"wish": 2}, Output: "note"}
}

// #839, the trust rule: a skill's script reaches the box only through the
// runner, with the skill's granted tools and its limits. A call to a tool
// the kernel did not grant, or one over the limit, is refused before it
// leaves the Operator and recorded with the session; what it may call goes
// out in the skill's name, so the kernel checks it again.
func TestAScriptGetsOnlyItsSkillsToolsAndARefusalIsRecorded(t *testing.T) {
	mcp, calls := headerMCP(t)
	home := t.TempDir()
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "geheim-ops"}, LLM: &LLM{URL: "http://127.0.0.1:1/v1"}, Home: home, Skills: probeSkills(t, `
import laibskill
def run(s):
    said = []
    for tool, args in [("laib_restart", {"service": "bote"}), ("laib_apply", {}), ("websuche", {"frage": "x"}),
                       ("laib_status", {}), ("laib_wish", {"text": "a"}), ("laib_wish", {"text": "b"}), ("laib_wish", {"text": "c"})]:
        r = s.call(tool, **args)
        said.append(f"{tool}:{'refused' if r.refused else 'ok'}")
    s.call("laib_note", kind="watch", text=" ".join(said))
    second = s.call("laib_note", kind="watch", text="noch eine")
    return laibskill.done(outcome="green", why="second note " + ("refused" if second.refused else "ran"))
`)}
	s, err := op.Run(context.Background(), probeSkill(), Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	if s.Note != "laib_restart:refused laib_apply:refused websuche:refused laib_status:ok laib_wish:ok laib_wish:ok laib_wish:refused" || s.Why != "second note refused" {
		t.Fatalf("the script saw: %q, %q", s.Note, s.Why)
	}
	all := strings.Join(*calls, "\n")
	for _, never := range []string{"laib_restart", "laib_apply", "websuche", "noch eine"} {
		if strings.Contains(all, "tools/call "+never+" ") || strings.Contains(all, "noch eine") {
			t.Errorf("a refused call reached laib (%s):\n%s", never, all)
		}
	}
	for _, c := range *calls {
		if strings.Contains(c, "tools/call") && !strings.HasPrefix(c, "probe ") && !strings.Contains(c, "laib_inbox --all") { // the record is the runner's own read
			t.Errorf("a script's call was not made in its skill's name: %s", c)
		}
	}
	if len(s.Refused) != 5 || !strings.Contains(s.Refused[0], "laib_restart") || !strings.Contains(s.Refused[3], "höchstens 2 Mal wish") {
		t.Errorf("the refusals are not in the session: %q", s.Refused)
	}
	rec, _ := os.ReadFile(filepath.Join(home, "incidents.jsonl"))
	if !strings.Contains(string(rec), `"refused":["laib_restart: `) {
		t.Errorf("the record does not keep the refusals: %s", rec)
	}
	if len(s.Calls) != 0 || len(s.Script) != 4 {
		t.Errorf("the script's calls are its own, apart from the model's: %q %q", s.Calls, s.Script)
	}
	if r := op.Work().Skills["probe"]; r.Outcome != "green" || !strings.Contains(r.Caused, "5 verweigert") || !strings.Contains(r.Caused, "2× Wunsch") {
		t.Errorf("/ready does not show the run: %+v", r)
	}
}

// #839: a script gets no token, no address and none of the Operator's
// files: its environment is empty, its argv names only the scripts, and
// its door refuses the network, other programs and every file outside the
// skills and the standard library. And the Operator itself is undumpable,
// so its /proc is closed to a process of the same user.
func TestAScriptHasNoTokenNoNetworkAndNoFiles(t *testing.T) {
	mcp, _ := headerMCP(t)
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "findings.json"), []byte(`{"seen":{}}`), 0o644)
	t.Setenv("LAIB_TOKEN", "geheim-ops")
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "geheim-ops"}, LLM: &LLM{URL: "http://127.0.0.1:1/v1"}, Home: home, Skills: probeSkills(t, `
import os, sys
import laibskill
def tries(what, f):
    try:
        f()
        return what + ":open"
    except Exception as e:
        return what + ":" + type(e).__name__
def run(s):
    out = ["env=" + ",".join(sorted(os.environ)), "argv=" + " ".join(sys.argv[1:])]
    out.append(tries("socket", lambda: __import__("socket")))
    out.append(tries("urllib", lambda: __import__("urllib.request").request.urlopen("`+mcp.URL+`")))
    out.append(tries("home", lambda: open("`+filepath.Join(home, "findings.json")+`").read()))
    out.append(tries("proc", lambda: open("/proc/%d/environ" % os.getppid(), "rb").read()))
    out.append(tries("write", lambda: open(os.path.join(os.path.dirname(laibskill.__file__), "x"), "w")))
    out.append(tries("exec", lambda: os.system("true")))
    out.append(tries("subprocess", lambda: __import__("subprocess")))
    out.append(tries("listdir", lambda: os.listdir("/")))
    return laibskill.done(outcome="green", note=" ".join(out))
`)}
	s, err := op.Run(context.Background(), probeSkill(), Trigger{Kind: "tick"})
	if err != nil {
		t.Fatal(err)
	}
	// LC_CTYPE is Python's own (PEP 538), set for its UTF-8; nothing else is there
	for _, want := range []string{"env=LC_CTYPE argv=" + filepath.Join(op.Skills, "probe", "run.py") + " ", "socket:Refused", "urllib:Refused", "home:Refused", "proc:Refused", "write:Refused", "exec:Refused", "subprocess:Refused", "listdir:Refused"} {
		if !strings.Contains(s.Note+" ", want) {
			t.Errorf("want %s in what the script could do: %s", want, s.Note)
		}
	}
	if strings.Contains(s.Note, "geheim") || strings.Contains(s.Note, ":open") {
		t.Errorf("the script reached past its door: %s", s.Note)
	}

	if err := Private(); err != nil {
		t.Fatal(err)
	}
	if d, _, _ := syscall.RawSyscall(syscall.SYS_PRCTL, syscall.PR_GET_DUMPABLE, 0, 0); d != 0 {
		t.Errorf("the Operator is still dumpable: %d", d)
	}
}

// #839: a script's state is kept for its next run unless the session
// failed, and a skill from the state repo runs no script at all.
func TestAScriptsStateIsKeptAndOnlyABuiltinRunsOne(t *testing.T) {
	mcp, calls := headerMCP(t)
	home := t.TempDir()
	op := &Operator{MCP: &MCP{URL: mcp.URL, Token: "t"}, LLM: &LLM{URL: "http://127.0.0.1:1/v1"}, Home: home, Skills: probeSkills(t, `
import laibskill
def run(s):
    n = (s.state or {}).get("n", 0) + 1
    return laibskill.done(outcome="unchanged", why=f"run {n}", state={"n": n})
`)}
	for want := 1; want <= 2; want++ {
		s, err := op.Run(context.Background(), probeSkill(), Trigger{Kind: "tick"})
		if err != nil || s.Outcome != "unchanged" || s.Why != "run "+string(rune('0'+want)) {
			t.Fatalf("run %d: %+v %v", want, s, err)
		}
	}
	rec, _ := os.ReadFile(filepath.Join(home, "incidents.jsonl"))
	if strings.Count(string(rec), `"outcome":"unchanged"`) != 2 {
		t.Errorf("a run with nothing to do is recorded once each: %s", rec)
	}
	state := probeSkill()
	state.Source = "state"
	n := len(*calls)
	if _, err := op.Run(context.Background(), state, Trigger{Kind: "tick"}); err == nil || !strings.Contains(err.Error(), "eingebaute") {
		t.Errorf("a state repo's skill ran a script: %v", err)
	}
	if strings.Contains(strings.Join((*calls)[n:], "\n"), "tools/call laib_status") {
		t.Errorf("a refused script still called laib: %v", (*calls)[n:])
	}
}
