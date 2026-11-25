package main

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// chrome returns the browser from $CHROME or from PATH.
func chrome(t *testing.T) string {
	if c := os.Getenv("CHROME"); c != "" {
		return c
	}
	for _, n := range []string{"chromium", "chromium-browser", "google-chrome", "chrome"} {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	t.Skip("no Chromium: set CHROME")
	return ""
}

var preOut = regexp.MustCompile(`(?s)<pre id="out">(.*?)</pre>`)

// browserPlan runs the TypeScript planner in headless Chromium with the default settings and returns its JSON.
func browserPlan(t *testing.T, data map[string]any) []byte {
	js, err := compile(`import { plan } from "./planner";
const d = JSON.parse(document.getElementById("d")!.textContent!);
const out = document.createElement("pre");
out.id = "out";
try { out.textContent = JSON.stringify(plan(d, d.defaults)); } catch (e) { out.textContent = "ERR " + String(e); }
document.body.append(out);`)
	if err != nil {
		t.Fatal(err)
	}
	blob, err := json.Marshal(data)
	if err != nil {
		t.Fatal(err)
	}
	page := filepath.Join(t.TempDir(), "t.html")
	src := `<!doctype html><body><script type="application/json" id="d">` + string(blob) + `</script><script>` + js + `</script></body>`
	if err := os.WriteFile(page, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(chrome(t), "--headless=new", "--disable-gpu", "--no-first-run", "--user-data-dir="+t.TempDir(),
		"--virtual-time-budget=60000", "--dump-dom", "file://"+page)
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("chromium: %v", err)
	}
	m := preOut.FindSubmatch(b)
	if m == nil {
		t.Fatalf("no planner output in the page")
	}
	out := html.UnescapeString(string(m[1]))
	if strings.HasPrefix(out, "ERR ") {
		t.Fatal(out)
	}
	return []byte(out)
}

// norm drops the empty values that Go omits or writes as null, so both outputs have the same shape.
func norm(v any) any {
	switch x := v.(type) {
	case map[string]any:
		o := map[string]any{}
		for k, e := range x {
			if e = norm(e); e != nil {
				o[k] = e
			}
		}
		if len(o) == 0 {
			return nil
		}
		return o
	case []any:
		if len(x) == 0 {
			return nil
		}
		o := make([]any, len(x))
		for i, e := range x {
			o[i] = norm(e)
		}
		return o
	case string:
		if x == "" {
			return nil
		}
	case bool:
		if !x {
			return nil
		}
	case float64:
		if x == 0 {
			return nil
		}
	}
	return v
}

// diff returns the paths where a and b differ. Numbers match to 1e-9.
func diff(a, b any, p string) []string {
	switch x := a.(type) {
	case map[string]any:
		y, ok := b.(map[string]any)
		if !ok {
			return []string{p}
		}
		keys := map[string]bool{}
		for k := range x {
			keys[k] = true
		}
		for k := range y {
			keys[k] = true
		}
		var out []string
		for k := range keys {
			out = append(out, diff(x[k], y[k], p+"."+k)...)
		}
		sort.Strings(out)
		return out
	case []any:
		y, ok := b.([]any)
		if !ok || len(x) != len(y) {
			return []string{p + " (length)"}
		}
		var out []string
		for i := range x {
			out = append(out, diff(x[i], y[i], fmt.Sprintf("%s[%d]", p, i))...)
		}
		return out
	case float64:
		y, ok := b.(float64)
		if !ok || math.Abs(x-y) > 1e-9*math.Max(1, math.Abs(x)) {
			return []string{fmt.Sprintf("%s: %v != %v", p, a, b)}
		}
		return nil
	}
	if fmt.Sprint(a) != fmt.Sprint(b) {
		return []string{fmt.Sprintf("%s: %v != %v", p, a, b)}
	}
	return nil
}

// TestEquivalence compares the browser planner with a reference plan with the same default settings, step by step
// ($CUPLAN_REF, default ../work/ref/steps.json).
func TestEquivalence(t *testing.T) {
	ref := os.Getenv("CUPLAN_REF")
	if ref == "" {
		ref = filepath.Join("..", "work", "ref", "steps.json")
	}
	rb, err := os.ReadFile(ref)
	if err != nil {
		t.Skipf("no reference plan: %v", err)
	}
	data, _, err := pageData(filepath.Join("..", "work", "data"))
	if err != nil {
		t.Skipf("no prep output: %v", err)
	}
	var want, got map[string]any
	if err := json.Unmarshal(rb, &want); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(browserPlan(t, data), &got); err != nil {
		t.Fatal(err)
	}
	ws, _ := want["steps"].([]any)
	gs, _ := got["steps"].([]any)
	t.Logf("reference %d steps, browser %d steps", len(ws), len(gs))
	for i := range max(len(ws), len(gs)) {
		if i >= len(ws) || i >= len(gs) {
			t.Fatalf("step %d exists in only one plan", i+1)
		}
		if d := diff(norm(ws[i]), norm(gs[i]), "steps"); len(d) > 0 {
			t.Fatalf("step %d differs:\n%s", i+1, strings.Join(d[:min(len(d), 20)], "\n"))
		}
	}
	if d := diff(norm(want["notReached"]), norm(got["notReached"]), "notReached"); len(d) > 0 {
		t.Fatalf("missed targets differ:\n%s", strings.Join(d, "\n"))
	}
	if d := diff(norm(want["maxLayer"]), norm(got["maxLayer"]), "maxLayer"); len(d) > 0 {
		t.Fatal(d)
	}
}

// TestLiquidDiscard checks that the liquid result of an INT-up craft goes nowhere: no holder keeps it.
func TestLiquidDiscard(t *testing.T) {
	data, _, err := pageData(filepath.Join("..", "work", "data"))
	if err != nil {
		t.Skipf("no prep output: %v", err)
	}
	var p struct {
		Steps []struct {
			Step  int
			Name  string
			Note  string
			Delta map[string]float64
			After struct{ Liq map[string]float64 }
		}
	}
	if err := json.Unmarshal(browserPlan(t, data), &p); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, s := range p.Steps {
		if !strings.Contains(s.Note, "INT-UP: discard") || s.Delta[s.Name+" mL"] <= 0 {
			continue
		}
		n++
		if s.After.Liq[s.Name] > 0 {
			t.Errorf("step %d %s: discarded liquid is still held (%v mL)", s.Step, s.Name, s.After.Liq[s.Name])
		}
	}
	if n == 0 {
		t.Fatal("the default plan has no INT-up craft with a liquid result")
	}
}

var dNote = regexp.MustCompile(`^\[INT(\d+) d([+-]?\d+)\]`)

// TestSafeInt checks that with options.safeInt no craft starts below its recipe INT and no craft is a gamble.
func TestSafeInt(t *testing.T) {
	data, _, err := pageData(filepath.Join("..", "work", "data"))
	if err != nil {
		t.Skipf("no prep output: %v", err)
	}
	def := data["defaults"].(map[string]any)
	def["options"].(map[string]any)["safeInt"] = true
	var p struct {
		Steps []struct {
			Step  int
			Kind  string
			Name  string
			Note  string
			Incl  map[string]float64
			After struct{ Int int }
		}
		NotReached []struct{ ID, Block, Why string }
	}
	if err := json.Unmarshal(browserPlan(t, data), &p); err != nil {
		t.Fatal(err)
	}
	have := int(def["assumptions"].(map[string]any)["startINT"].(float64))
	for _, s := range p.Steps {
		if s.Kind == "craft" && s.Name != "prep" {
			m := dNote.FindStringSubmatch(s.Note)
			if m == nil {
				t.Fatalf("step %d %s: no [INT d] note: %q", s.Step, s.Name, s.Note)
			}
			if strings.HasPrefix(m[2], "-") || strings.Contains(s.Note, "GAMBLE") {
				t.Errorf("step %d %s: craft below recipe INT: %q", s.Step, s.Name, s.Note)
			}
			need, err := strconv.Atoi(m[1])
			if err != nil {
				t.Fatal(err)
			}
			if len(s.Incl) == 0 && have < need {
				t.Errorf("step %d %s: INT %d before the craft, recipe INT %d", s.Step, s.Name, have, need)
			}
		}
		have = s.After.Int
	}
	t.Logf("%d steps, INT %d at the end", len(p.Steps), have)
	for _, m := range p.NotReached {
		t.Logf("missed %s: %s: %s", m.ID, m.Block, m.Why)
	}
}

// TestKeptMaterials checks that only items with the game tag "tool" show tool wear or tool breaks.
// Canvas and nails are materials: a kept slot uses them up.
func TestKeptMaterials(t *testing.T) {
	data, _, err := pageData(filepath.Join("..", "work", "data"))
	if err != nil {
		t.Skipf("no prep output: %v", err)
	}
	items, ok := data["items"].(map[string]any)
	if !ok {
		t.Fatal("items.json is not an object")
	}
	tool := func(id string) bool {
		it, _ := items[id].(map[string]any)
		tags, _ := it["tags"].(string)
		return strings.Contains(","+tags+",", ",tool,")
	}
	var p struct {
		Steps []struct {
			Step     int
			Name     string
			Breaks   string
			UsedUp   string
			ToolWear map[string]float64
			MatWear  map[string]float64
		}
	}
	if err := json.Unmarshal(browserPlan(t, data), &p); err != nil {
		t.Fatal(err)
	}
	up := map[string]int{}
	for _, s := range p.Steps {
		for _, id := range strings.Fields(strings.ReplaceAll(s.Breaks, "BREAKS", "")) {
			if !tool(id) {
				t.Errorf("step %d %s: %s is not a tool but breaks", s.Step, s.Name, id)
			}
		}
		for id := range s.ToolWear {
			if !tool(id) {
				t.Errorf("step %d %s: %s is not a tool but has tool wear", s.Step, s.Name, id)
			}
		}
		for id := range s.MatWear {
			if tool(id) {
				t.Errorf("step %d %s: tool %s has material wear", s.Step, s.Name, id)
			}
		}
		for _, id := range strings.Fields(s.UsedUp) {
			if tool(id) {
				t.Errorf("step %d %s: tool %s is used up, not broken", s.Step, s.Name, id)
			}
			up[id]++
		}
	}
	if up["canvas"] == 0 || up["nails"] == 0 {
		t.Fatalf("the default plan uses up no canvas or no nails: %v", up)
	}
}
