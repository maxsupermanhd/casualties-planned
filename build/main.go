// Command build compiles the plan page: one HTML file with the game data, the icon atlas, the styles and the
// compiled TypeScript planner and page code.
package main

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image/png"
	"os"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"text/template"

	"github.com/evanw/esbuild/pkg/api"
	"github.com/maxsupermanhd/lac"
)

//go:embed ts/*.ts
var tsFiles embed.FS

//go:embed page.html
var pageHTML string

//go:embed defaults.json
var defaultsJSON []byte

var pageT = template.Must(template.New("page").Delims("[[", "]]").Parse(pageHTML))

type Config struct {
	WorkDir string
	Page    string
}

func loadConfig(p string) (*Config, error) {
	c, err := lac.FromFileJSON(p)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", p, err)
	}
	cfg := &Config{WorkDir: c.GetDSString("", "workDir"), Page: c.GetDSString("", "page")}
	if cfg.WorkDir == "" || cfg.Page == "" {
		return nil, fmt.Errorf("config %s: workDir and page are required", p)
	}
	if !strings.HasSuffix(cfg.Page, ".html") {
		return nil, fmt.Errorf("config %s: page %q must end in .html", p, cfg.Page)
	}
	dir := filepath.Dir(p)
	for _, s := range []*string{&cfg.WorkDir, &cfg.Page} {
		if !filepath.IsAbs(*s) {
			*s = filepath.Join(dir, *s)
		}
	}
	return cfg, nil
}

// compile bundles a TypeScript entry from the embedded ts/ files into one minified script.
func compile(entry string) (string, error) {
	embedFS := api.Plugin{Name: "embed", Setup: func(b api.PluginBuild) {
		b.OnResolve(api.OnResolveOptions{Filter: `^\./`}, func(a api.OnResolveArgs) (api.OnResolveResult, error) {
			p := path.Join("ts", a.Path)
			if !strings.HasSuffix(p, ".ts") {
				p += ".ts"
			}
			return api.OnResolveResult{Path: p, Namespace: "embed"}, nil
		})
		b.OnLoad(api.OnLoadOptions{Filter: `.*`, Namespace: "embed"}, func(a api.OnLoadArgs) (api.OnLoadResult, error) {
			src, err := tsFiles.ReadFile(a.Path)
			if err != nil {
				return api.OnLoadResult{}, err
			}
			s := string(src)
			return api.OnLoadResult{Contents: &s, Loader: api.LoaderTS}, nil
		})
	}}
	r := api.Build(api.BuildOptions{
		Stdin:             &api.StdinOptions{Contents: entry, Loader: api.LoaderTS, Sourcefile: "entry.ts"},
		Bundle:            true,
		Format:            api.FormatIIFE,
		Target:            api.ES2020,
		Platform:          api.PlatformBrowser,
		MinifyWhitespace:  true,
		MinifySyntax:      true,
		MinifyIdentifiers: true,
		Charset:           api.CharsetUTF8,
		LegalComments:     api.LegalCommentsNone,
		Plugins:           []api.Plugin{embedFS},
	})
	if len(r.Errors) > 0 {
		var msg []string
		for _, m := range r.Errors {
			loc := ""
			if m.Location != nil {
				loc = fmt.Sprintf("%s:%d: ", m.Location.File, m.Location.Line)
			}
			msg = append(msg, loc+m.Text)
		}
		return "", errors.New(strings.Join(msg, "\n"))
	}
	if len(r.OutputFiles) != 1 {
		return "", fmt.Errorf("esbuild wrote %d files, want 1", len(r.OutputFiles))
	}
	js := string(r.OutputFiles[0].Contents)
	for _, bad := range []string{"</script", "<!--"} {
		if strings.Contains(strings.ToLower(js), bad) {
			return "", fmt.Errorf("compiled script contains %s", bad)
		}
	}
	return js, nil
}

func readJSON(p string, v any) error {
	b, err := os.ReadFile(p)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, v); err != nil {
		return fmt.Errorf("%s: %w", p, err)
	}
	return nil
}

// pageData reads the prep output and adds the default settings (defaults.json). Values that come from the game
// rules fill the settings keys that the defaults do not set.
func pageData(dataDir string, defaults []byte) (map[string]any, []byte, error) {
	d := map[string]any{}
	for _, k := range []string{"recipes", "items", "names", "containers", "buildings", "rules", "atlas"} {
		var v any
		if err := readJSON(filepath.Join(dataDir, k+".json"), &v); err != nil {
			return nil, nil, fmt.Errorf("%w (run prep first)", err)
		}
		d[k] = v
	}
	atlas, err := os.ReadFile(filepath.Join(dataDir, "atlas.png"))
	if err != nil {
		return nil, nil, fmt.Errorf("%w (run prep first)", err)
	}
	ac, err := png.DecodeConfig(bytes.NewReader(atlas))
	if err != nil {
		return nil, nil, fmt.Errorf("atlas.png: %w", err)
	}
	d["atlasSize"] = []int{ac.Width, ac.Height}
	var def map[string]any
	if err := json.Unmarshal(defaults, &def); err != nil {
		return nil, nil, fmt.Errorf("defaults.json: %w", err)
	}
	rules, ok := d["rules"].(map[string]any)
	if !ok {
		return nil, nil, errors.New("rules.json is not an object")
	}
	fill := map[string][]string{
		"assumptions": {"carryCap", "backSlots", "layers", "firstCraftXP", "descendXP", "biochemLastLayer"},
		"game":        {"handsOnly", "toolQualities", "liquidQualities", "magazines"},
	}
	for sect, keys := range fill {
		m, ok := def[sect].(map[string]any)
		if !ok {
			return nil, nil, fmt.Errorf("defaults.json: %s is not an object", sect)
		}
		for _, k := range keys {
			if _, set := m[k]; !set {
				if m[k], ok = rules[k]; !ok {
					return nil, nil, fmt.Errorf("rules.json has no %s", k)
				}
			}
		}
	}
	if _, set := def["raws"]; !set {
		def["raws"] = rules["raws"]
	}
	presets, _ := def["presets"].(map[string]any)
	if presets == nil {
		presets = map[string]any{}
	}
	delete(def, "presets")
	for k, v := range presets {
		if l, _ := v.([]any); len(l) == 0 {
			presets[k] = def["targets"]
		}
	}
	d["defaults"], d["presets"] = def, presets
	if err := prune(d); err != nil {
		return nil, nil, err
	}
	return d, atlas, nil
}

// prune drops the item data that no plan can use and the buildings that the page does not show. An item stays when
// a recipe makes or uses it, when it has a quality that a recipe asks for, when it can hold liquid (the page offers
// it as the water holder), or when the settings or the rules name it. The names and the icons of all items stay.
func prune(d map[string]any) error {
	var recipes []struct {
		Result string
		Items  []struct{ ID, Q string }
	}
	if err := remarshal(d["recipes"], &recipes); err != nil {
		return fmt.Errorf("recipes.json: %w", err)
	}
	var items map[string]struct {
		Capacity float64
		Quals    []struct{ ID string }
	}
	if err := remarshal(d["items"], &items); err != nil {
		return fmt.Errorf("items.json: %w", err)
	}
	all, _ := d["items"].(map[string]any)
	conts, _ := d["containers"].(map[string]any)
	keep := map[string]bool{}
	quals := map[string]bool{}
	for _, r := range recipes {
		keep[r.Result] = true
		for _, g := range r.Items {
			keep[g.ID] = true
			quals[g.Q] = true
		}
	}
	var named func(v any)
	named = func(v any) {
		switch x := v.(type) {
		case string:
			keep[x] = true
		case []any:
			for _, e := range x {
				named(e)
			}
		case map[string]any:
			for k, e := range x {
				keep[k] = true
				named(e)
			}
		}
	}
	named(d["defaults"])
	named(d["presets"])
	named(d["rules"])
	for id, it := range items {
		hasQ := slices.ContainsFunc(it.Quals, func(q struct{ ID string }) bool { return quals[q.ID] })
		if !keep[id] && !hasQ && it.Capacity <= 0 {
			delete(all, id)
			delete(conts, id)
		}
	}
	rules, _ := d["rules"].(map[string]any)
	traps, _ := rules["traps"].(map[string]any)
	bs, _ := d["buildings"].(map[string]any)
	for id := range bs {
		if _, ok := traps[id]; !ok {
			delete(bs, id)
		}
	}
	return nil
}

// remarshal decodes the JSON value v into out.
func remarshal(v, out any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}

func build(cfg *Config) (int, error) {
	b, err := page(filepath.Join(cfg.WorkDir, "data"), defaultsJSON)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.Page), 0o755); err != nil {
		return 0, err
	}
	return len(b), os.WriteFile(cfg.Page, b, 0o644)
}

// page makes the HTML page from the prep output in dataDir and the default settings.
func page(dataDir string, defaults []byte) ([]byte, error) {
	d, atlas, err := pageData(dataDir, defaults)
	if err != nil {
		return nil, err
	}
	app, err := compile(`import "./app"`)
	if err != nil {
		return nil, fmt.Errorf("app.ts: %w", err)
	}
	worker, err := compile(`import "./worker"`)
	if err != nil {
		return nil, fmt.Errorf("worker.ts: %w", err)
	}
	blob, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(app))
	var b bytes.Buffer
	err = pageT.Execute(&b, map[string]string{"Data": string(blob), "App": app, "Worker": worker,
		"AppHash": base64.StdEncoding.EncodeToString(sum[:]), "Atlas": "data:image/png;base64," + base64.StdEncoding.EncodeToString(atlas)})
	if err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: build config.json")
		os.Exit(2)
	}
	cfg, err := loadConfig(os.Args[1])
	if err == nil {
		var n int
		if n, err = build(cfg); err == nil {
			fmt.Println(cfg.Page, n, "bytes")
			return
		}
	}
	fmt.Fprintln(os.Stderr, "build:", err)
	os.Exit(1)
}
