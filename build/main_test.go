package main

import (
	"bytes"
	"context"
	"encoding/json"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// need skips the test when a tool is missing. In CI ($CI is set) it fails instead, so CI always runs every test.
func need(t *testing.T, msg string) {
	t.Helper()
	if os.Getenv("CI") != "" {
		t.Fatal(msg)
	}
	t.Skip(msg)
}

// lookPath returns the program from the environment variable env or the first of names in PATH.
func lookPath(t *testing.T, env string, names ...string) string {
	t.Helper()
	if p := os.Getenv(env); p != "" {
		return p
	}
	for _, n := range names {
		if p, err := exec.LookPath(n); err == nil {
			return p
		}
	}
	need(t, "no "+names[0]+" in PATH: set "+env)
	return ""
}

var harness = sync.OnceValues(func() (string, error) {
	return compile(`import { plan } from "./planner";
declare const process: any;
let s = "";
process.stdin.setEncoding("utf8");
process.stdin.on("data", (c: string) => (s += c));
process.stdin.on("end", () => {
  const r = JSON.parse(s);
  let out;
  try {
    out = { plan: plan(r.data, r.settings) };
  } catch (e) {
    out = { error: e instanceof Error ? e.message : String(e) };
  }
  process.stdout.write(JSON.stringify(out));
});`)
})

// runPlan runs the TypeScript planner in node (or bun, or $CUPLAN_JS) and returns the plan JSON or the error text.
func runPlan(t *testing.T, data map[string]any, settings any) (json.RawMessage, string) {
	t.Helper()
	js, err := harness()
	if err != nil {
		t.Fatal(err)
	}
	rt := lookPath(t, "CUPLAN_JS", "node", "bun")
	p := filepath.Join(t.TempDir(), "harness.js")
	if err := os.WriteFile(p, []byte(js), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := json.Marshal(map[string]any{"data": data, "settings": settings})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, rt, p)
	cmd.Stdin = bytes.NewReader(in)
	cmd.Stderr = os.Stderr
	b, err := cmd.Output()
	if err != nil {
		t.Fatalf("%s: %v", rt, err)
	}
	var r struct {
		Plan  json.RawMessage
		Error string
	}
	if err := json.Unmarshal(b, &r); err != nil {
		t.Fatalf("planner output: %v", err)
	}
	return r.Plan, r.Error
}

type cell struct {
	ID    string
	N     int
	Conds []float64
	Ammo  *struct {
		ID string
		N  int
	}
}

type snap struct {
	Int, Layer          int
	Xp, Load            float64
	HandR, HandL, Mouth *cell
	Worn                []struct {
		Slot, ID string
		Cond     float64
	}
	Bags []struct {
		ID    string
		Items []cell
	}
	Over []cell
	Liq  map[string]float64
}

// cells returns all cells of the snapshot: hands, mouth, bags and items without room.
func (s snap) cells() []cell {
	var out []cell
	for _, c := range []*cell{s.HandR, s.HandL, s.Mouth} {
		if c != nil {
			out = append(out, *c)
		}
	}
	for _, b := range s.Bags {
		out = append(out, b.Items...)
	}
	return append(out, s.Over...)
}

type step struct {
	Step                        int
	Kind, Name, Note            string
	Breaks, UsedUp, Bottleneck  string
	Times                       int
	Goal                        bool
	Gathered, Used, Delta, Incl map[string]float64
	ToolWear, MatWear           map[string]float64
	InclXp                      float64
	After                       snap
}

type planJSON struct {
	MaxLayer   int
	StartXp    float64
	Steps      []step
	NotReached []struct {
		ID, Block, Why string
		Done, N        int
	}
	Lost []struct {
		ID         string
		Made, Left int
		By         []int
	}
	Warnings []string
	Stopped  string
}

// fixture returns the page data of the small invented game in testdata, and its default settings.
func fixture(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	def, err := os.ReadFile(filepath.Join("testdata", "defaults.json"))
	if err != nil {
		t.Fatal(err)
	}
	d, _, err := pageData(filepath.Join("testdata", "game"), def)
	if err != nil {
		t.Fatal(err)
	}
	return d, d["defaults"].(map[string]any)
}

// realData returns the page data from the prep output in ../work/data. The test skips when there is none, also in CI.
func realData(t *testing.T) (map[string]any, map[string]any) {
	t.Helper()
	d, _, err := pageData(filepath.Join("..", "work", "data"), defaultsJSON)
	if err != nil {
		t.Skipf("no prep output: %v", err)
	}
	return d, d["defaults"].(map[string]any)
}

// set changes the settings value at a path like "assumptions.startINT".
func set(s map[string]any, path string, v any) {
	keys := strings.Split(path, ".")
	for _, k := range keys[:len(keys)-1] {
		s = s[k].(map[string]any)
	}
	s[keys[len(keys)-1]] = v
}

func mustPlan(t *testing.T, data, settings map[string]any) planJSON {
	t.Helper()
	raw, msg := runPlan(t, data, settings)
	if msg != "" {
		t.Fatal(msg)
	}
	var p planJSON
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	return p
}

func find(p planJSON, name string) *step {
	for i := range p.Steps {
		if p.Steps[i].Name == name {
			return &p.Steps[i]
		}
	}
	return nil
}

// TestFixturePlan checks the plan for the fixture defaults against a plan made by hand:
//  1. craft blade on layer 1 (gather 1 wood, d+0): 10 xp, INT 0.
//  2. descend to layer 2: plank needs INT 1, 50 xp short. 90 xp, INT 1.
//  3. craft pack (gather 2 wood): the blade and the plank do not fit in one free hand. 100 xp.
//  4. craft plank (gather 1 wood) with the blade, which loses 1/4 of its condition. 110 xp.
//
// Load at the end: lamp 0.2 + pack 0.5 + (plank 0.4 + blade 0.3) x 0.5 = 1.05.
func TestFixturePlan(t *testing.T) {
	data, def := fixture(t)
	p := mustPlan(t, data, def)
	type want struct {
		kind, name  string
		layer, int  int
		xp          float64
		gathered    float64
		note, bneck string
	}
	ws := []want{
		{"craft", "blade", 1, 0, 10, 1, "[INT0 d+0] FIRST +10xp", ""},
		{"descend", "to layer 2", 2, 1, 90, 0, "", "INT 1 gate: 50 xp short (plank)"},
		{"craft", "pack", 2, 1, 100, 2, "[INT0 d+1] FIRST +10xp STORAGE (back slot)", ""},
		{"craft", "plank", 2, 1, 110, 1, "[INT1 d+0] FIRST +10xp", ""},
	}
	if len(p.Steps) != len(ws) {
		t.Fatalf("%d steps, want %d: %+v", len(p.Steps), len(ws), p.Steps)
	}
	for i, w := range ws {
		s := p.Steps[i]
		if s.Kind != w.kind || s.Name != w.name || s.After.Layer != w.layer || s.After.Int != w.int || s.After.Xp != w.xp || s.Gathered["wood"] != w.gathered || s.Note != w.note || s.Bottleneck != w.bneck {
			t.Errorf("step %d: %+v, want %+v", i+1, s, w)
		}
	}
	if w := p.Steps[3].ToolWear["blade"]; w != 0.25 {
		t.Errorf("blade wear %v, want 0.25", w)
	}
	last := p.Steps[3].After
	if math.Abs(last.Load-1.05) > 1e-9 || last.HandR == nil || last.HandR.ID != "lamp" || len(last.Bags) != 1 || len(last.Bags[0].Items) != 2 {
		t.Errorf("final kit %+v", last)
	}
	if p.StartXp != 0 || len(p.NotReached) != 0 || len(p.Lost) != 0 || len(p.Warnings) != 0 || p.Stopped != "" {
		t.Errorf("plan %+v", p)
	}
}

// TestLostTarget checks that a reached target that a later craft eats is in the lost list.
func TestLostTarget(t *testing.T) {
	data, def := fixture(t)
	def["targets"] = []string{"blade", "plank", "spear"}
	p := mustPlan(t, data, def)
	sp := find(p, "spear")
	if sp == nil || sp.Used["blade"] != 1 {
		t.Fatalf("no spear step that uses the blade: %+v", p.Steps)
	}
	if len(p.Lost) != 1 || p.Lost[0].ID != "blade" || p.Lost[0].Made != 1 || p.Lost[0].Left != 0 || len(p.Lost[0].By) != 1 || p.Lost[0].By[0] != sp.Step {
		t.Fatalf("lost %+v, want the blade, used in step %d", p.Lost, sp.Step)
	}
}

// TestMagazine checks that loaded rounds show in their magazine and add no weight.
func TestMagazine(t *testing.T) {
	data, def := fixture(t)
	def["targets"] = []string{"clip", "shell*2"}
	p := mustPlan(t, data, def)
	last := p.Steps[len(p.Steps)-1].After
	var clip, shell *cell
	for _, c := range last.cells() {
		switch c.ID {
		case "clip":
			clip = &c
		case "shell":
			shell = &c
		}
	}
	if clip == nil || clip.Ammo == nil || clip.Ammo.ID != "shell" || clip.Ammo.N != 3 {
		t.Fatalf("clip %+v, want 3 shells loaded", clip)
	}
	if shell == nil || shell.N != 1 {
		t.Fatalf("loose shells %+v, want 1 of 4", shell)
	}
	// lamp 0.2 + pack 0.5 + (clip 0.3 + shell 0.05) x 0.5
	if math.Abs(last.Load-0.875) > 1e-9 {
		t.Fatalf("load %v, want 0.875", last.Load)
	}
}

// TestGambleOnlyTargets checks that on the last layer only a gamble target is crafted at d = -1, not the storage bag.
func TestGambleOnlyTargets(t *testing.T) {
	data, def := fixture(t)
	for _, r := range data["recipes"].([]any) {
		if r := r.(map[string]any); r["result"] == "pack" {
			r["int"] = 5.0
		}
	}
	def["targets"] = []string{"blade", "plank", "helmet"}
	set(def, "assumptions.startINT", 4)
	set(def, "assumptions.maxLayer", 1)
	p := mustPlan(t, data, def)
	h := find(p, "helmet")
	if h == nil || !strings.Contains(h.Note, "d-1") || !strings.Contains(h.Note, "GAMBLE") || !strings.Contains(h.Note, "assumes 20%") {
		t.Fatalf("no helmet gamble: %+v", p.Steps)
	}
	if len(h.After.Worn) != 1 || h.After.Worn[0].Cond != 0.2 {
		t.Fatalf("helmet worn %+v, want condition 0.2", h.After.Worn)
	}
	for _, s := range p.Steps {
		if s.Name != "helmet" && (strings.Contains(s.Note, "GAMBLE") || strings.Contains(s.Note, "d-")) {
			t.Errorf("step %d %s: %s", s.Step, s.Name, s.Note)
		}
	}
	if len(p.NotReached) != 1 || p.NotReached[0].ID != "plank" || p.NotReached[0].Block != "storage" {
		t.Fatalf("missed %+v, want plank blocked by storage", p.NotReached)
	}
}

// TestExtraGathers checks that a bad or unused extra gather gives a warning and does not stop the plan.
func TestExtraGathers(t *testing.T) {
	data, def := fixture(t)
	set(def, "assumptions.extraGathers", []any{
		map[string]any{"layer": 1, "item": "ore", "count": 1},
		map[string]any{"layer": 4, "item": "wood", "count": 1},
	})
	p := mustPlan(t, data, def)
	if len(p.Steps) != 4 || len(p.NotReached) != 0 || p.Stopped != "" {
		t.Fatalf("plan %d steps, missed %+v, stopped %q", len(p.Steps), p.NotReached, p.Stopped)
	}
	if len(p.Warnings) != 2 || !strings.Contains(p.Warnings[0], "ore is not on layer 1") || !strings.Contains(p.Warnings[1], "layer limit") {
		t.Fatalf("warnings %q", p.Warnings)
	}
}

// TestNoSteps checks a plan that has no step at all.
func TestNoSteps(t *testing.T) {
	data, def := fixture(t)
	def["targets"] = []string{"helmet"}
	set(def, "assumptions.maxLayer", 1)
	set(def, "options.gambleMode", "off")
	p := mustPlan(t, data, def)
	if len(p.Steps) != 0 || len(p.NotReached) != 1 || p.NotReached[0].Block != "INT" {
		t.Fatalf("plan %+v", p)
	}
}

// TestOtherRecipe checks that the planner uses the second recipe of a target when the first one has no source.
func TestOtherRecipe(t *testing.T) {
	data, def := fixture(t)
	def["targets"] = []string{"rod"}
	p := mustPlan(t, data, def)
	if len(p.Steps) != 1 || p.Steps[0].Name != "rod" || p.Steps[0].Used["wood"] != 2 || len(p.NotReached) != 0 {
		t.Fatalf("plan %+v", p)
	}
}

// TestNoSpareLight checks a target that eats the light when no recipe makes a spare one.
func TestNoSpareLight(t *testing.T) {
	data, def := fixture(t)
	def["targets"] = []string{"lantern"}
	p := mustPlan(t, data, def)
	for _, s := range p.Steps {
		if s.Kind == "craft" {
			t.Errorf("step %d crafts %s", s.Step, s.Name)
		}
	}
	if len(p.NotReached) != 1 || !strings.Contains(p.NotReached[0].Why, "no recipe makes a spare one") {
		t.Fatalf("missed %+v", p.NotReached)
	}
}

// TestFillerResults checks that an INT-up craft keeps a tool and discards other results.
func TestFillerResults(t *testing.T) {
	data, def := fixture(t)
	def["targets"] = []string{"helmet"}
	set(def, "options.fillers", true)
	set(def, "assumptions.maxLayer", 1)
	p := mustPlan(t, data, def)
	ch, pl := find(p, "chisel"), find(p, "plank")
	if ch == nil || !strings.Contains(ch.Note, "INT-UP: keep the result (tool)") || pl == nil || !strings.Contains(pl.Note, "INT-UP: discard the result now") {
		t.Fatalf("steps %+v", p.Steps)
	}
	has := func(s snap, id string) bool {
		for _, c := range s.cells() {
			if c.ID == id {
				return true
			}
		}
		return false
	}
	if !has(ch.After, "chisel") || has(pl.After, "plank") {
		t.Fatalf("chisel kept %v, plank kept %v", has(ch.After, "chisel"), has(pl.After, "plank"))
	}
}

// TestCheck checks that bad settings give an error and do not hang the planner.
func TestCheck(t *testing.T) {
	data, _ := fixture(t)
	for path, v := range map[string]any{
		"assumptions.startINT":     800,
		"assumptions.descendXP":    "80",
		"assumptions.lights":       nil,
		"assumptions.startItems":   5,
		"assumptions.maxLayer":     9,
		"assumptions.extraGathers": []any{map[string]any{"layer": 1, "item": "gem", "count": 1}},
		"options.guns":             "sometimes",
		"targets":                  []string{"blade*1000"},
	} {
		_, def := fixture(t)
		set(def, path, v)
		if _, msg := runPlan(t, data, def); msg == "" {
			t.Errorf("%s = %v: no error", path, v)
		}
	}
}

// TestPageData checks the pruned page data: all names and icons stay, unused item data and buildings go.
func TestPageData(t *testing.T) {
	data, _ := fixture(t)
	items := data["items"].(map[string]any)
	if _, ok := items["junk"]; ok {
		t.Error("unused item junk is in the page data")
	}
	for _, id := range []string{"wood", "ore", "gem", "lamp", "canteen", "chisel"} {
		if _, ok := items[id]; !ok {
			t.Errorf("item %s is not in the page data", id)
		}
	}
	if names := data["names"].(map[string]any); names["junk"] != "Junk" {
		t.Error("the name of junk is not in the page data")
	}
	if atlas := data["atlas"].(map[string]any); atlas["junk"] == nil {
		t.Error("the icon of junk is not in the page data")
	}
	if bs := data["buildings"].(map[string]any); len(bs) != 1 || bs["zapper"] == nil {
		t.Errorf("buildings %v, want only the trap zapper", bs)
	}
	if p, ok := data["presets"].(map[string]any); !ok || len(p) != 0 {
		t.Errorf("presets %v, want an empty object", data["presets"])
	}
}

func TestCompile(t *testing.T) {
	for _, src := range []string{`console.log("<!--")`, `console.log("</script>")`} {
		if js, err := compile(src); err == nil && (strings.Contains(js, "<!--") || strings.Contains(strings.ToLower(js), "</script")) {
			t.Errorf("compile(%s) gave %q", src, js)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "config.json")
	write := func(s string) {
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(`{"workDir": "work", "page": "/srv/cuplan.html"}`)
	cfg, err := loadConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.WorkDir != filepath.Join(dir, "work") || cfg.Page != "/srv/cuplan.html" {
		t.Fatalf("config %+v", cfg)
	}
	for _, s := range []string{`{"workDir": "work"}`, `{"workDir": "work", "page": "out.txt"}`, `{`} {
		write(s)
		if _, err := loadConfig(p); err == nil {
			t.Errorf("config %s accepted", s)
		}
	}
}

// The tests below use the prep output of the real game in ../work/data. They check rules, not a stored plan.

// TestLiquidDiscard checks that the liquid result of an INT-up craft goes nowhere: no holder keeps it.
func TestLiquidDiscard(t *testing.T) {
	data, def := realData(t)
	n := 0
	for _, s := range mustPlan(t, data, def).Steps {
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

// xpFor and intOf are Skills.GetExperienceForLevel from the game code and its inverse, as in planner.ts.
func xpFor(l int) float64 {
	n, s := 0.0, 60.0
	for i := 1; i <= l; i++ {
		switch {
		case i < 10:
			n += 60
		case i == 10:
			s = 100
			n += s
		default:
			s *= 1.18
			if i > 20 {
				s *= 1.15
				if i >= 30 {
					s *= 2
				}
			}
			n += s
		}
	}
	return math.Round(n)
}

func intOf(xp float64) int {
	l := 0
	for xpFor(l+1) <= xp {
		l++
	}
	return l
}

var dNote = regexp.MustCompile(`^\[INT(\d+) d([+-]?\d+)\]`)

// TestSafeInt checks that with options.safeInt no craft starts below its recipe INT and no craft is a gamble.
// The XP before a craft is at most the XP after the step before plus the first-craft XP of its sub-crafts.
func TestSafeInt(t *testing.T) {
	data, def := realData(t)
	set(def, "options.safeInt", true)
	p := mustPlan(t, data, def)
	xp := p.StartXp
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
			if have := intOf(xp + s.InclXp); have < need {
				t.Errorf("step %d %s: INT %d at most before the craft, recipe INT %d", s.Step, s.Name, have, need)
			}
		}
		xp = s.After.Xp
	}
	t.Logf("%d steps, INT %d at the end", len(p.Steps), intOf(xp))
}

// TestKeptMaterials checks that only items with the game tag "tool" show tool wear or tool breaks.
// Canvas and nails are materials: a kept slot uses them up.
func TestKeptMaterials(t *testing.T) {
	data, def := realData(t)
	items := data["items"].(map[string]any)
	tool := func(id string) bool {
		it, _ := items[id].(map[string]any)
		tags, _ := it["tags"].(string)
		return strings.Contains(","+tags+",", ",tool,")
	}
	up := map[string]int{}
	for _, s := range mustPlan(t, data, def).Steps {
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

// TestPruneKeepsPlan checks that the default plan is the same with all item data and with the pruned page data.
func TestPruneKeepsPlan(t *testing.T) {
	data, def := realData(t)
	var all map[string]any
	if err := readJSON(filepath.Join("..", "work", "data", "items.json"), &all); err != nil {
		t.Fatal(err)
	}
	if len(all) <= len(data["items"].(map[string]any)) {
		t.Fatal("prune drops no item")
	}
	a, _ := runPlan(t, data, def)
	full := map[string]any{}
	for k, v := range data {
		full[k] = v
	}
	full["items"] = all
	b, _ := runPlan(t, full, def)
	if !bytes.Equal(a, b) {
		t.Fatal("the plan changes with the pruned item data")
	}
}
