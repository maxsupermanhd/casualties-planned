// Command prep reads your copy of Casualties Unknown and writes the game data and the icon atlas for the plan page.
package main

import (
	"bytes"
	_ "embed"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"maps"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/maxsupermanhd/lac"
)

type Config struct {
	GameDir     string
	GameDataDir string
	Decompiler  struct {
		Command string
		Args    []string
		Env     map[string]string
	}
	WorkDir string
	Page    string
}

func loadConfig(path string) (*Config, error) {
	c, err := lac.FromFileJSON(path)
	if err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	cfg := &Config{}
	if err := c.GetToStruct(cfg); err != nil {
		return nil, fmt.Errorf("config %s: %w", path, err)
	}
	for k, v := range map[string]string{"gameDir": cfg.GameDir, "gameDataDir": cfg.GameDataDir, "workDir": cfg.WorkDir, "decompiler.command": cfg.Decompiler.Command} {
		if v == "" {
			return nil, fmt.Errorf("config %s: %s is required", path, k)
		}
	}
	dir := filepath.Dir(path)
	for _, p := range []*string{&cfg.GameDir, &cfg.WorkDir} {
		if !filepath.IsAbs(*p) {
			*p = filepath.Join(dir, *p)
		}
		if *p, err = resolve(*p); err != nil {
			return nil, fmt.Errorf("config %s: %w", path, err)
		}
	}
	if strings.ContainsRune(cfg.Decompiler.Command, filepath.Separator) && !filepath.IsAbs(cfg.Decompiler.Command) {
		cfg.Decompiler.Command = filepath.Join(dir, cfg.Decompiler.Command)
	}
	// prep deletes <workDir>/decomp, so that folder must not hold the game, and prep must not write into the game.
	if within(cfg.WorkDir, cfg.GameDir) || within(cfg.GameDir, filepath.Join(cfg.WorkDir, "decomp")) {
		return nil, fmt.Errorf("config %s: workDir %s must not be in gameDir %s, and gameDir must not be in workDir/decomp", path, cfg.WorkDir, cfg.GameDir)
	}
	return cfg, nil
}

// resolve makes p absolute and resolves the symbolic links in the part of p that exists.
func resolve(p string) (string, error) {
	p, err := filepath.Abs(p)
	if err != nil {
		return "", err
	}
	rest := ""
	for {
		r, err := filepath.EvalSymlinks(p)
		if err == nil {
			return filepath.Join(r, rest), nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return "", err
		}
		rest = filepath.Join(filepath.Base(p), rest)
		p = parent
	}
}

// within reports whether p is dir or a path inside dir.
func within(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func decompile(cfg *Config, dll, out string) error {
	d := cfg.Decompiler
	cmdPath, err := exec.LookPath(d.Command)
	if err != nil {
		return fmt.Errorf("decompiler %q not found (install ilspycmd, see README, or set decompiler.command): %w", d.Command, err)
	}
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	args := slices.Clone(d.Args)
	for i, a := range args {
		args[i] = strings.NewReplacer("{dll}", dll, "{out}", out).Replace(a)
	}
	cmd := exec.Command(cmdPath, args...)
	cmd.Env = os.Environ()
	for _, k := range slices.Sorted(maps.Keys(d.Env)) {
		cmd.Env = append(cmd.Env, k+"="+d.Env[k])
	}
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

//go:embed rules.json
var rulesJSON []byte

// writeFile writes to a temporary file in the same folder and then renames it, so a reader never sees half a file.
func writeFile(path string, b []byte) error {
	f, err := os.CreateTemp(filepath.Dir(path), filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	_, err = f.Write(b)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Chmod(f.Name(), 0o644)
	}
	if err == nil {
		err = os.Rename(f.Name(), path)
	}
	if err != nil {
		return errors.Join(err, os.Remove(f.Name()))
	}
	return nil
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", " ")
	if err != nil {
		return err
	}
	return writeFile(path, b)
}

var unsafeName = regexp.MustCompile(`[\x00-\x1f<>]`)

func cleanName(v any) string {
	s, ok := v.(string)
	if !ok {
		return ""
	}
	r := []rune(strings.TrimSpace(unsafeName.ReplaceAllString(s, "")))
	return string(r[:min(len(r), 48)])
}

func run() error {
	if len(os.Args) != 2 {
		return errors.New("usage: prep config.json")
	}
	cfg, err := loadConfig(os.Args[1])
	if err != nil {
		return err
	}
	gd := filepath.Join(cfg.GameDir, cfg.GameDataDir)
	dll, assets, lang := filepath.Join(gd, "Managed", "Assembly-CSharp.dll"), filepath.Join(gd, "resources.assets"), filepath.Join(gd, "Lang", "EN.json")
	for _, f := range []string{dll, assets, lang} {
		if _, err := os.Stat(f); err != nil {
			return fmt.Errorf("game file not found: %s (set gameDir to the folder that holds %s): %w", f, cfg.GameDataDir, err)
		}
	}
	decomp, out := filepath.Join(cfg.WorkDir, "decomp"), filepath.Join(cfg.WorkDir, "data")
	fmt.Println("decompile:", dll)
	if err := decompile(cfg, dll, decomp); err != nil {
		return fmt.Errorf("decompile: %w", err)
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	rs, err := os.ReadFile(filepath.Join(decomp, "Recipes.cs"))
	if err != nil {
		return err
	}
	is, err := os.ReadFile(filepath.Join(decomp, "Item.cs"))
	if err != nil {
		return err
	}
	recipes, err := parseRecipes(string(rs))
	if err != nil {
		return fmt.Errorf("Recipes.cs: %w", err)
	}
	items, err := parseItems(string(is))
	if err != nil {
		return fmt.Errorf("Item.cs: %w", err)
	}
	if len(recipes) == 0 || len(items) == 0 {
		return fmt.Errorf("found %d recipes and %d items in the decompiled code (a different ilspycmd version?)", len(recipes), len(items))
	}
	lb, err := os.ReadFile(lang)
	if err != nil {
		return err
	}
	var en struct{ Main map[string]any }
	if err := json.Unmarshal(bytes.TrimPrefix(lb, []byte("\xef\xbb\xbf")), &en); err != nil {
		return fmt.Errorf("%s: %w", lang, err)
	}
	names := map[string]string{}
	for id := range items {
		if n := cleanName(en.Main[id]); n != "" {
			names[id] = n
		}
	}
	f, err := openAssets(assets)
	if err != nil {
		return err
	}
	pf, err := f.prefabs(items)
	if err != nil {
		return err
	}
	atlas, index, err := packAtlas(pf.icons)
	if err != nil {
		return err
	}
	var rules any
	if err := json.Unmarshal(rulesJSON, &rules); err != nil {
		return fmt.Errorf("rules.json: %w", err)
	}
	files := []struct {
		name string
		v    any
	}{{"recipes.json", recipes}, {"items.json", items}, {"names.json", names}, {"containers.json", pf.containers},
		{"buildings.json", pf.buildings}, {"rules.json", rules}, {"atlas.json", index}}
	for _, f := range files {
		if err := writeJSON(filepath.Join(out, f.name), f.v); err != nil {
			return err
		}
	}
	var pngBuf bytes.Buffer
	if err := png.Encode(&pngBuf, atlas); err != nil {
		return err
	}
	if err := writeFile(filepath.Join(out, "atlas.png"), pngBuf.Bytes()); err != nil {
		return err
	}
	fmt.Printf("%s: %d recipes, %d items, %d names, %d icons (%dx%d atlas), %d containers, %d buildings\n", out, len(recipes), len(items), len(names),
		len(index), atlas.Bounds().Dx(), atlas.Bounds().Dy(), len(pf.containers), len(pf.buildings))
	return nil
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "prep:", err)
		os.Exit(1)
	}
}

// ---------------------------------------------------------------- extract: decompiled C# to JSON

type Ing struct {
	ID      string  `json:"id,omitempty"`
	Q       string  `json:"q,omitempty"`
	Amt     float64 `json:"amt,omitempty"`
	Liquid  bool    `json:"liquid,omitempty"`
	Kept    bool    `json:"kept,omitempty"`
	MinCond float64 `json:"minCond"`
	NoMax   bool    `json:"noMax,omitempty"`
}

type Recipe struct {
	INT      int     `json:"int"`
	Result   string  `json:"result"`
	Amount   int     `json:"amount"`
	Cond     float64 `json:"cond"`
	Liquid   bool    `json:"liquid,omitempty"`
	Category string  `json:"category"`
	Repair   bool    `json:"repair,omitempty"`
	Items    []Ing   `json:"items"`
}

type Qual struct {
	ID  string  `json:"id"`
	Amt float64 `json:"amt"`
}

type ItemInfo struct {
	Weight     float64 `json:"weight"`
	Armor      float64 `json:"armor,omitempty"`
	Capacity   float64 `json:"capacity,omitempty"`
	HitLoss    float64 `json:"hitLoss,omitempty"`
	Slot       string  `json:"slot,omitempty"`
	Limb       string  `json:"limb,omitempty"`
	DestroyAt0 bool    `json:"destroyAt0,omitempty"`
	Combine    bool    `json:"combine,omitempty"`
	ScaleW     bool    `json:"scaleW,omitempty"`
	Tags       string  `json:"tags,omitempty"`
	Category   string  `json:"category,omitempty"`
	Quals      []Qual  `json:"quals,omitempty"`
}

// skipLit returns the index after the string or char literal that starts at src[i], or -1 if it does not end.
// It knows verbatim (@"") and interpolated ($"{x}") strings.
func skipLit(src string, i int) int {
	if src[i] == '\'' {
		for j := i + 1; j < len(src); j++ {
			switch src[j] {
			case '\\':
				j++
			case '\'':
				return j + 1
			}
		}
		return -1
	}
	verbatim, interp := false, false
	for k := i - 1; k >= 0 && k >= i-2; k-- {
		switch src[k] {
		case '@':
			verbatim = true
			continue
		case '$':
			interp = true
			continue
		}
		break
	}
	for j := i + 1; j < len(src); j++ {
		c := src[j]
		switch {
		case c == '"' && verbatim && j+1 < len(src) && src[j+1] == '"':
			j++
		case c == '"':
			return j + 1
		case c == '\\' && !verbatim:
			j++
		case c == '{' && interp && j+1 < len(src) && src[j+1] == '{':
			j++
		case c == '{' && interp:
			end, err := closeOf(src, j)
			if err != nil {
				return -1
			}
			j = end
		}
	}
	return -1
}

// closeOf returns the index of the bracket that closes the one at src[open]. It skips literals and // comments.
func closeOf(src string, open int) (int, error) {
	d := 0
	for j := open; j < len(src); j++ {
		switch src[j] {
		case '"', '\'':
			n := skipLit(src, j)
			if n < 0 {
				return 0, fmt.Errorf("unterminated literal at %d", j)
			}
			j = n - 1
		case '/':
			if j+1 < len(src) && src[j+1] == '/' {
				if n := strings.IndexByte(src[j:], '\n'); n >= 0 {
					j += n
				} else {
					j = len(src)
				}
			}
		case '{', '(', '[':
			d++
		case '}', ')', ']':
			d--
			if d == 0 {
				return j, nil
			}
		}
	}
	return 0, fmt.Errorf("unbalanced block at %d", open)
}

func block(src string, open int) (string, error) {
	end, err := closeOf(src, open)
	if err != nil {
		return "", err
	}
	return src[open : end+1], nil
}

// fields returns the top-level "key = value" members of an object initializer block "{ ... }". A string value
// loses its quotes.
func fields(b string) (map[string]string, error) {
	out := map[string]string{}
	add := func(part string) {
		k, v, ok := strings.Cut(part, "=")
		k, v = strings.TrimSpace(k), strings.TrimSpace(v)
		if !ok || !wordID.MatchString(k) {
			return
		}
		if len(v) >= 2 && v[0] == '"' && skipLit(v, 0) == len(v) {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	start := 1
	for j := 1; j < len(b)-1; j++ {
		switch b[j] {
		case '"', '\'', '{', '(', '[':
			var end int
			if b[j] == '"' || b[j] == '\'' {
				end = skipLit(b, j) - 1
				if end < 0 {
					return nil, fmt.Errorf("unterminated literal at %d", j)
				}
			} else {
				var err error
				if end, err = closeOf(b, j); err != nil {
					return nil, err
				}
			}
			j = end
		case ',':
			add(b[start:j])
			start = j + 1
		}
	}
	add(b[start : len(b)-1])
	return out, nil
}

func num(s string) (float64, error) {
	return strconv.ParseFloat(strings.TrimSuffix(s, "f"), 64)
}

var (
	reRecipe = regexp.MustCompile(`recipes\s*\.\s*Add\s*\(\s*new\s+Recipe\s*(?:\(\s*\)\s*)?\{`)
	reRItem  = regexp.MustCompile(`new\s+RecipeItem\s*(\(\s*([\d.]+f?)\s*\))?\s*\{`)
	reQNew   = regexp.MustCompile(`^new\s+CraftingQuality\s*\(\s*"(\w+)"\s*(?:,\s*([\d.]+f?)\s*)?\)$`)
	reItem   = regexp.MustCompile(`GlobalItems\s*\.\s*Add\s*\(\s*"(\w+)"\s*,\s*new\s+\w+\s*(?:\(\s*\)\s*)?\{`)
	reQual   = regexp.MustCompile(`new\s+CraftingQuality\s*\(\s*"(\w+)"\s*(?:,\s*([\d.]+f?)\s*)?\)`)
	reInit   = regexp.MustCompile(`^new\s+[\w<>.]+\s*(?:\(\s*\)\s*)?\{`)
)

// sub returns the fields of a value "new T { ... }".
func sub(v string) (map[string]string, error) {
	m := reInit.FindStringIndex(v)
	if m == nil {
		return nil, fmt.Errorf("%.40q is not an object initializer", v)
	}
	b, err := block(v, m[1]-1)
	if err != nil {
		return nil, err
	}
	return fields(b)
}

func parseRecipes(src string) ([]Recipe, error) {
	var out []Recipe
	for _, loc := range reRecipe.FindAllStringIndex(src, -1) {
		b, err := block(src, loc[1]-1)
		if err != nil {
			return nil, err
		}
		f, err := fields(b)
		if err != nil {
			return nil, fmt.Errorf("recipe at %d: %w", loc[0], err)
		}
		var r Recipe
		if r.INT, err = strconv.Atoi(f["INT"]); err != nil {
			return nil, fmt.Errorf("recipe at %d: INT: %w", loc[0], err)
		}
		res, err := sub(f["result"])
		if err != nil {
			return nil, fmt.Errorf("recipe at %d: result: %w", loc[0], err)
		}
		r.Result = res["id"]
		if !wordID.MatchString(r.Result) {
			return nil, fmt.Errorf("recipe at %d: bad result id %q", loc[0], r.Result)
		}
		r.Amount, r.Cond = 1, 1
		if a, ok := res["amount"]; ok {
			if r.Amount, err = strconv.Atoi(a); err != nil {
				return nil, fmt.Errorf("recipe %s: amount: %w", r.Result, err)
			}
		}
		if c, ok := res["resultCondition"]; ok {
			if r.Cond, err = num(c); err != nil {
				return nil, fmt.Errorf("recipe %s: resultCondition: %w", r.Result, err)
			}
		}
		r.Liquid = res["isLiquid"] == "true"
		r.Category = "Materials"
		if c, ok := strings.CutPrefix(f["category"], "RecipeCategory."); ok {
			r.Category = c
		}
		r.Repair = f["isRepair"] == "true"
		il, ok := f["items"]
		if !ok {
			return nil, fmt.Errorf("no items in recipe %s", r.Result)
		}
		for _, m := range reRItem.FindAllStringSubmatchIndex(il, -1) {
			ib, err := block(il, m[1]-1)
			if err != nil {
				return nil, err
			}
			it, err := fields(ib)
			if err != nil {
				return nil, fmt.Errorf("recipe %s: %w", r.Result, err)
			}
			// 0.9 is the RecipeItem default minimum condition in the game code.
			g := Ing{MinCond: 0.9, Kept: it["destroyItem"] == "false", Liquid: it["isLiquid"] == "true", NoMax: it["noMaxCondition"] == "true", ID: it["specificId"]}
			if m[4] >= 0 {
				if g.MinCond, err = num(il[m[4]:m[5]]); err != nil {
					return nil, err
				}
			}
			if mc, ok := it["minimumCondition"]; ok {
				if g.MinCond, err = num(mc); err != nil {
					return nil, err
				}
			}
			if q := it["quality"]; q != "" {
				g.Q, g.Amt = q, 1
				if qm := reQNew.FindStringSubmatch(q); qm != nil {
					g.Q = qm[1]
					if qm[2] != "" {
						if g.Amt, err = num(qm[2]); err != nil {
							return nil, err
						}
					}
				}
				if !wordID.MatchString(g.Q) {
					return nil, fmt.Errorf("recipe %s: bad quality %q", r.Result, q)
				}
			}
			if g.ID == "" && g.Q == "" {
				return nil, fmt.Errorf("ingredient without id or quality in %s", r.Result)
			}
			r.Items = append(r.Items, g)
		}
		out = append(out, r)
	}
	return out, nil
}

func parseItems(src string) (map[string]ItemInfo, error) {
	out := map[string]ItemInfo{}
	for _, m := range reItem.FindAllStringSubmatchIndex(src, -1) {
		id := src[m[2]:m[3]]
		b, err := block(src, m[1]-1)
		if err != nil {
			return nil, err
		}
		f, err := fields(b)
		if err != nil {
			return nil, fmt.Errorf("item %s: %w", id, err)
		}
		var it ItemInfo
		for k, p := range map[string]*float64{"weight": &it.Weight, "capacity": &it.Capacity, "wearableArmor": &it.Armor, "wearableHitDurabilityLossMultiplier": &it.HitLoss} {
			if v := f[k]; v != "" {
				if *p, err = num(v); err != nil {
					return nil, fmt.Errorf("%s %s: %w", id, k, err)
				}
			}
		}
		it.Slot, it.Limb, it.Tags, it.Category = f["wearSlotId"], f["desiredWearLimb"], f["tags"], f["category"]
		it.DestroyAt0 = f["destroyAtZeroCondition"] == "true"
		it.Combine = f["combineable"] == "true"
		it.ScaleW = f["scaleWeightWithCondition"] == "true"
		for _, q := range reQual.FindAllStringSubmatch(f["qualities"], -1) {
			a := 1.0
			if q[2] != "" {
				if a, err = num(q[2]); err != nil {
					return nil, err
				}
			}
			it.Quals = append(it.Quals, Qual{q[1], a})
		}
		out[id] = it
	}
	return out, nil
}

// ---------------------------------------------------------------- icons: Unity 2022.3 serialized file (format 22, no type trees)

const (
	classGameObject     = 1
	classTransform      = 4
	classTexture2D      = 28
	classSpriteRenderer = 212
	classSprite         = 213
	classMonoBehaviour  = 114
)

type object struct {
	class int32
	data  []byte
}

type assetFile struct {
	objs  map[int64]object
	order []int64
}

type pptr struct {
	FileID int32
	PathID int64
}

type reader struct {
	b   []byte
	off int
	err error
}

func (r *reader) take(n int) []byte {
	if r.err != nil {
		return nil
	}
	if n < 0 || r.off+n > len(r.b) {
		r.err = fmt.Errorf("read %d bytes at %d past end %d", n, r.off, len(r.b))
		return nil
	}
	p := r.b[r.off : r.off+n]
	r.off += n
	return p
}

func (r *reader) align() {
	if pad := (4 - r.off%4) % 4; pad > 0 {
		r.take(pad)
	}
}

func (r *reader) u8() byte {
	if p := r.take(1); p != nil {
		return p[0]
	}
	return 0
}

func (r *reader) i32() int32 {
	if p := r.take(4); p != nil {
		return int32(binary.LittleEndian.Uint32(p))
	}
	return 0
}

func (r *reader) i64() int64 {
	if p := r.take(8); p != nil {
		return int64(binary.LittleEndian.Uint64(p))
	}
	return 0
}

func (r *reader) f32() float32 {
	if p := r.take(4); p != nil {
		return math.Float32frombits(binary.LittleEndian.Uint32(p))
	}
	return 0
}

func (r *reader) str() string {
	s := string(r.take(int(r.i32())))
	r.align()
	return s
}

func (r *reader) cstr() string {
	if r.err != nil {
		return ""
	}
	i := bytes.IndexByte(r.b[r.off:], 0)
	if i < 0 {
		r.err = errors.New("unterminated string")
		return ""
	}
	s := string(r.b[r.off : r.off+i])
	r.off += i + 1
	return s
}

func (r *reader) ptr() pptr { return pptr{r.i32(), r.i64()} }

func (r *reader) ptrs() []pptr {
	n := int(r.i32())
	if n < 0 || n > len(r.b)/12 {
		r.err = fmt.Errorf("bad pointer array length %d", n)
		return nil
	}
	out := make([]pptr, n)
	for i := range out {
		out[i] = r.ptr()
	}
	return out
}

func openAssets(path string) (*assetFile, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if len(b) < 48 {
		return nil, fmt.Errorf("%s: too short for a serialized file", path)
	}
	if v := binary.BigEndian.Uint32(b[8:]); v != 22 {
		return nil, fmt.Errorf("%s: serialized file version %d, only 22 is supported", path, v)
	}
	if b[16] != 0 {
		return nil, fmt.Errorf("%s: big-endian data is not supported", path)
	}
	dataOff := binary.BigEndian.Uint64(b[32:])
	if dataOff > uint64(len(b)) {
		return nil, fmt.Errorf("%s: data offset past end", path)
	}
	r := &reader{b: b, off: 48}
	ver := r.cstr()
	r.i32()
	if r.u8() != 0 {
		return nil, fmt.Errorf("%s: type trees present, the reader expects stripped types", path)
	}
	nt := int(r.i32())
	if nt < 0 || nt > 1<<16 {
		return nil, fmt.Errorf("%s: bad type count %d", path, nt)
	}
	classes := make([]int32, nt)
	for i := range classes {
		classes[i] = r.i32()
		r.take(3)
		if classes[i] == 114 {
			r.take(16)
		}
		r.take(16)
	}
	no := int(r.i32())
	if no < 0 || no > len(b)/24 {
		return nil, fmt.Errorf("%s: bad object count %d", path, no)
	}
	f := &assetFile{objs: make(map[int64]object, no)}
	for range no {
		r.align()
		id, start, size, ti := r.i64(), r.i64(), int64(r.i32()), r.i32()
		if r.err != nil {
			break
		}
		if ti < 0 || int(ti) >= nt {
			return nil, fmt.Errorf("%s: object %d has type index %d", path, id, ti)
		}
		lo := int64(dataOff) + start
		if start < 0 || size < 0 || lo+size > int64(len(b)) {
			return nil, fmt.Errorf("%s: object %d out of bounds", path, id)
		}
		f.objs[id] = object{classes[ti], b[lo : lo+size]}
		f.order = append(f.order, id)
	}
	if r.err != nil {
		return nil, fmt.Errorf("%s (unity %s): %w", path, ver, r.err)
	}
	return f, nil
}

func (f *assetFile) get(p pptr, class int32) (*reader, error) {
	if p.FileID != 0 {
		return nil, fmt.Errorf("object %d is in external file %d", p.PathID, p.FileID)
	}
	o, ok := f.objs[p.PathID]
	if !ok {
		return nil, fmt.Errorf("object %d not found", p.PathID)
	}
	if o.class != class {
		return nil, fmt.Errorf("object %d is class %d, want %d", p.PathID, o.class, class)
	}
	return &reader{b: o.data}, nil
}

func spriteOf(b []byte) (pptr, error) {
	r := &reader{b: b}
	r.take(12)
	r.take(10)
	r.align()
	r.take(4 + 4 + 2 + 2 + 16 + 16)
	r.ptrs()
	r.take(4 + 12 + 12 + 12 + 4 + 2 + 2)
	p := r.ptr()
	return p, r.err
}

func fatherOf(b []byte) (pptr, error) {
	r := &reader{b: b}
	r.take(12 + 16 + 12 + 12)
	r.ptrs()
	p := r.ptr()
	return p, r.err
}

func (f *assetFile) spriteImage(p pptr) (*image.NRGBA, error) {
	r, err := f.get(p, classSprite)
	if err != nil {
		return nil, err
	}
	r.str()
	rect := [4]float32{r.f32(), r.f32(), r.f32(), r.f32()}
	r.take(8 + 16 + 4 + 8 + 4)
	r.u8()
	r.align()
	r.take(24)
	n := int(r.i32())
	for range n {
		r.str()
		if r.err != nil {
			return nil, r.err
		}
	}
	atlas := r.ptr()
	tex := r.ptr()
	if r.err != nil {
		return nil, r.err
	}
	if atlas.PathID != 0 {
		return nil, fmt.Errorf("sprite %d is packed in an atlas, not supported", p.PathID)
	}
	var px [4]int
	for i, v := range rect {
		if v != float32(math.Trunc(float64(v))) || v < 0 || v > 1<<14 {
			return nil, fmt.Errorf("sprite %d has a bad rect %v", p.PathID, rect)
		}
		px[i] = int(v)
	}
	return f.texture(tex, px)
}

func (f *assetFile) texture(p pptr, rc [4]int) (*image.NRGBA, error) {
	r, err := f.get(p, classTexture2D)
	if err != nil {
		return nil, err
	}
	name := r.str()
	r.take(4 + 1 + 1)
	r.align()
	w, h := int(r.i32()), int(r.i32())
	r.take(4 + 4)
	format := r.i32()
	r.take(4 + 3)
	r.align()
	r.str()
	r.u8()
	r.align()
	r.take(4 + 4 + 4 + 24 + 4 + 4)
	r.take(int(r.i32()))
	r.align()
	data := r.take(int(r.i32()))
	if r.err != nil {
		return nil, fmt.Errorf("texture %s: %w", name, r.err)
	}
	bpp := map[int32]int{3: 3, 4: 4}[format]
	if bpp == 0 {
		return nil, fmt.Errorf("texture %s: format %d not supported (only RGB24, RGBA32)", name, format)
	}
	if w <= 0 || h <= 0 || len(data) < w*h*bpp {
		return nil, fmt.Errorf("texture %s: %d bytes inline for %dx%d (streamed textures not supported)", name, len(data), w, h)
	}
	x0, y0, cw, ch := rc[0], rc[1], rc[2], rc[3]
	if x0+cw > w || y0+ch > h {
		return nil, fmt.Errorf("texture %s: rect %v outside %dx%d", name, rc, w, h)
	}
	img := image.NewNRGBA(image.Rect(0, 0, cw, ch))
	for y := range ch {
		row := (y0 + ch - 1 - y) * w
		for x := range cw {
			s := data[(row+x0+x)*bpp:]
			d := img.Pix[y*img.Stride+x*4:]
			d[0], d[1], d[2], d[3] = s[0], s[1], s[2], 255
			if bpp == 4 {
				d[3] = s[3]
			}
		}
	}
	return img, nil
}

type Container struct {
	Capacity    float64  `json:"capacity"`
	PerItem     float64  `json:"perItem"`
	Encumbrance float64  `json:"encumbrance"`
	Tags        []string `json:"tags"`
}

type Drop struct {
	ID     string  `json:"id"`
	Chance float64 `json:"chance"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
}

type Building struct {
	ID     string `json:"id"`
	Drops  []Drop `json:"drops"`
	Always []Drop `json:"always,omitempty"`
}

type prefabSet struct {
	icons      map[string]*image.NRGBA
	containers map[string]Container
	buildings  map[string]Building
}

var wordID = regexp.MustCompile(`^\w{1,64}$`)

// monoHeader skips the MonoBehaviour base fields: m_GameObject, m_Enabled, m_Script and m_Name.
func monoHeader(b []byte) *reader {
	r := &reader{b: b}
	r.take(12)
	r.u8()
	r.align()
	r.take(12)
	r.str()
	return r
}

// container reads a Container script: maxWeight, maxWeightPerItem, encumberanceMult, itemsVisible, tagRestriction.
// The data must end after the last field, so other scripts do not match.
func container(b []byte) (Container, bool) {
	r := monoHeader(b)
	c := Container{Capacity: float64(r.f32()), PerItem: float64(r.f32()), Encumbrance: float64(r.f32()), Tags: []string{}}
	r.u8()
	r.align()
	n := int(r.i32())
	if r.err != nil || n < 0 || n > 16 {
		return c, false
	}
	for range n {
		t := r.str()
		if !wordID.MatchString(t) {
			return c, false
		}
		c.Tags = append(c.Tags, t)
	}
	ok := r.err == nil && r.off == len(b) && c.Capacity > 0 && c.Capacity < 1000 && c.PerItem > 0 && c.Encumbrance >= 0 && c.Encumbrance <= 1
	for _, v := range []*float64{&c.Capacity, &c.PerItem, &c.Encumbrance} {
		*v = math.Round(*v*1e4) / 1e4
	}
	return c, ok
}

func drops(r *reader) ([]Drop, bool) {
	n := int(r.i32())
	if r.err != nil || n < 0 || n > 64 {
		return nil, false
	}
	out := []Drop{}
	for range n {
		d := Drop{ID: strings.ToLower(r.str()), Chance: float64(r.f32()), Min: float64(r.f32()), Max: float64(r.f32())}
		if r.err != nil || !wordID.MatchString(d.ID) || d.Chance < 0 || d.Chance > 100 || d.Min < 0 || d.Max > 1.0001 || d.Min > d.Max {
			return nil, false
		}
		for _, v := range []*float64{&d.Chance, &d.Min, &d.Max} {
			*v = math.Round(*v*1e4) / 1e4
		}
		out = append(out, d)
	}
	return out, true
}

// building reads the drop lists of a BuildingEntity script: itemsDropOnDestroy, _health, blockPlacedOn, requireGround,
// id, fullName, description, skipDescriptionSet, dropChanceMultiplier, guaranteedDropAmount, alwaysDrop.
func building(b []byte) (Building, bool) {
	r := monoHeader(b)
	var bl Building
	ds, ok := drops(r)
	if !ok {
		return bl, false
	}
	r.take(4 + 8)
	r.u8()
	r.align()
	bl.ID = strings.ToLower(r.str())
	r.str()
	r.str()
	r.u8()
	r.align()
	r.take(4 + 4)
	al, ok := drops(r)
	if !ok || r.err != nil || !wordID.MatchString(bl.ID) {
		return bl, false
	}
	bl.Drops, bl.Always = ds, al
	return bl, true
}

// prefabs reads the item prefabs (GameObjects named after an item id) and all buildings. A root prefab wins over a
// nested object with the same name.
func (f *assetFile) prefabs(items map[string]ItemInfo) (*prefabSet, error) {
	p := &prefabSet{icons: map[string]*image.NRGBA{}, containers: map[string]Container{}, buildings: map[string]Building{}}
	sprites := map[string]pptr{}
	for _, id := range f.order {
		o := f.objs[id]
		if o.class != classGameObject {
			continue
		}
		r := &reader{b: o.data}
		comps := r.ptrs()
		r.i32()
		name := strings.ToLower(r.str())
		if r.err != nil {
			return nil, fmt.Errorf("game object %d: %w", id, r.err)
		}
		_, isItem := items[name]
		var sprite pptr
		var cont *Container
		root := true
		for _, c := range comps {
			co, ok := f.objs[c.PathID]
			if c.FileID != 0 || !ok {
				return nil, fmt.Errorf("game object %s: component %d not in this file", name, c.PathID)
			}
			switch co.class {
			case classSpriteRenderer:
				s, err := spriteOf(co.data)
				if err != nil {
					return nil, fmt.Errorf("sprite renderer %d: %w", c.PathID, err)
				}
				sprite = s
			case classTransform:
				father, err := fatherOf(co.data)
				if err != nil {
					return nil, fmt.Errorf("transform %d: %w", c.PathID, err)
				}
				root = father.PathID == 0
			case classMonoBehaviour:
				if ct, ok := container(co.data); ok && isItem {
					cont = &ct
				}
				if bl, ok := building(co.data); ok {
					if _, dup := p.buildings[bl.ID]; !dup {
						p.buildings[bl.ID] = bl
					}
				}
			}
		}
		if !isItem {
			continue
		}
		if _, dup := sprites[name]; sprite.PathID != 0 && (!dup || root) {
			sprites[name] = sprite
		}
		if _, dup := p.containers[name]; cont != nil && (!dup || root) {
			p.containers[name] = *cont
		}
	}
	for name, sp := range sprites {
		img, err := f.spriteImage(sp)
		if err != nil {
			return nil, fmt.Errorf("icon %s: %w", name, err)
		}
		p.icons[name] = img
	}
	return p, nil
}

// packAtlas puts the icons on shelves, tallest first, in a 512 px wide image.
func packAtlas(icons map[string]*image.NRGBA) (*image.NRGBA, map[string][4]int, error) {
	const width = 512
	ids := slices.Sorted(maps.Keys(icons))
	sort.SliceStable(ids, func(a, b int) bool { return icons[ids[a]].Bounds().Dy() > icons[ids[b]].Bounds().Dy() })
	index := map[string][4]int{}
	x, y, shelf := 0, 0, 0
	for _, id := range ids {
		w, h := icons[id].Bounds().Dx(), icons[id].Bounds().Dy()
		if w > 64 || h > 64 {
			return nil, nil, fmt.Errorf("icon %s is %dx%d, larger than 64 px", id, w, h)
		}
		if x+w > width {
			x, y, shelf = 0, y+shelf+1, 0
		}
		index[id] = [4]int{x, y, w, h}
		x += w + 1
		shelf = max(shelf, h)
	}
	img := image.NewNRGBA(image.Rect(0, 0, width, max(1, y+shelf)))
	for id, r := range index {
		draw.Draw(img, image.Rect(r[0], r[1], r[0]+r[2], r[1]+r[3]), icons[id], image.Point{}, draw.Src)
	}
	return img, index, nil
}
