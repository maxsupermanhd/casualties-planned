package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"image"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	read := func(name string) string {
		b, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	rs, err := parseRecipes(read("Recipes.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 3 {
		t.Fatalf("%d recipes, want 3", len(rs))
	}
	r := rs[0]
	if r.INT != 3 || r.Result != "rope" || r.Amount != 2 || r.Cond != 0.5 || r.Category != "Tools" || r.Repair || len(r.Items) != 2 {
		t.Fatalf("recipe %+v", r)
	}
	if a, b := r.Items[0], r.Items[1]; a.ID != "foliage" || a.MinCond != 0 || a.Kept || b.Q != "cutting" || b.Amt != 2 || !b.Kept || b.MinCond != 0.9 {
		t.Fatalf("items %+v %+v", a, b)
	}
	w := rs[1]
	if w.INT != -1 || w.Result != "water" || !w.Liquid || w.Amount != 1 || w.Cond != 1 || w.Category != "Materials" || len(w.Items) != 2 {
		t.Fatalf("recipe %+v", w)
	}
	if a, b := w.Items[0], w.Items[1]; a.ID != "groundwater" || !a.Liquid || a.MinCond != 1 || b.Q != "heatsource" || b.Amt != 1 || b.MinCond != 0.2 || !b.Kept || !b.NoMax {
		t.Fatalf("items %+v %+v", a, b)
	}
	if k := rs[2]; k.Result != "knife" || !k.Repair || len(k.Items) != 1 || k.Items[0].ID != "scrapmetal" {
		t.Fatalf("recipe %+v", k)
	}
	is, err := parseItems(read("Item.cs"))
	if err != nil {
		t.Fatal(err)
	}
	if len(is) != 2 {
		t.Fatalf("%d items, want 2", len(is))
	}
	if it := is["rope"]; it.Weight != 0.25 || it.Capacity != 300 || it.Tags != "material" || !it.Combine || len(it.Quals) != 1 || it.Quals[0] != (Qual{"binding", 3}) {
		t.Fatalf("rope %+v", it)
	}
	if it := is["lamp"]; it.Weight != 1 || it.Category != "utility" || it.Slot != "hat" || it.Armor != 0.4 || !it.DestroyAt0 || !it.ScaleW || len(it.Quals) != 1 || it.Quals[0] != (Qual{"heatsource", 1}) {
		t.Fatalf("lamp %+v", it)
	}
}

func TestBlock(t *testing.T) {
	for src, want := range map[string]string{
		`x {a = "}", b = '}'} y`:          `{a = "}", b = '}'}`,
		`x {a = @"q""}", b = 1} y`:        `{a = @"q""}", b = 1}`,
		`x {a = $"{f("}")} }}"} y`:        `{a = $"{f("}")} }}"}`,
		"x {a = 1 // }\n, b = 2} y":       "{a = 1 // }\n, b = 2}",
		`x {a = new L { 1, 2 }, b = 3} y`: `{a = new L { 1, 2 }, b = 3}`,
	} {
		got, err := block(src, strings.IndexByte(src, '{'))
		if err != nil || got != want {
			t.Errorf("block(%q) = %q, %v; want %q", src, got, err, want)
		}
	}
	if _, err := block(`x {a = "}`, 2); err == nil {
		t.Error("unterminated string accepted")
	}
	f, err := fields(`{a = "x, y", b = new L { 1, 2 }, c = f(1, 2)}`)
	if err != nil || len(f) != 3 || f["a"] != "x, y" || f["b"] != "new L { 1, 2 }" || f["c"] != "f(1, 2)" {
		t.Fatalf("fields %q %v", f, err)
	}
}

func TestLoadConfig(t *testing.T) {
	dir := t.TempDir()
	game := filepath.Join(dir, "game")
	if err := os.MkdirAll(filepath.Join(game, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(game, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	write := func(gameDir, workDir string) string {
		p := filepath.Join(dir, "config.json")
		s := fmt.Sprintf(`{"gameDir": %q, "gameDataDir": "data", "decompiler": {"command": "tools/ilspycmd", "args": ["{dll}"]}, "workDir": %q}`, gameDir, workDir)
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cfg, err := loadConfig(write("link", "work"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.GameDir != game || cfg.WorkDir != filepath.Join(dir, "work") || cfg.Decompiler.Command != filepath.Join(dir, "tools", "ilspycmd") || cfg.Decompiler.Args[0] != "{dll}" {
		t.Fatalf("config %+v", cfg)
	}
	for _, c := range [][2]string{{"game", "game"}, {"game", "link"}, {"game", "game/data/work"}, {"link", "link/work"}, {"work/decomp/game", "work"}} {
		if _, err := loadConfig(write(c[0], c[1])); err == nil {
			t.Errorf("gameDir %s, workDir %s accepted", c[0], c[1])
		}
	}
	if _, err := loadConfig(write("", "work")); err == nil {
		t.Error("config without gameDir accepted")
	}
}

type enc struct{ b []byte }

func (e *enc) u32(v uint32)  { e.b = binary.LittleEndian.AppendUint32(e.b, v) }
func (e *enc) i64(v int64)   { e.b = binary.LittleEndian.AppendUint64(e.b, uint64(v)) }
func (e *enc) ptr(id int64)  { e.u32(0); e.i64(id) }
func (e *enc) zero(n int)    { e.b = append(e.b, make([]byte, n)...) }
func (e *enc) f32(v float32) { e.u32(math.Float32bits(v)) }
func (e *enc) pad()          { e.b = append(e.b, make([]byte, (4-len(e.b)%4)%4)...) }
func (e *enc) str(s string) {
	e.u32(uint32(len(s)))
	e.b = append(e.b, s...)
	e.pad()
}

// mono writes the MonoBehaviour base fields.
func mono() *enc {
	e := &enc{}
	e.b = append(e.b, make([]byte, 12)...)
	e.b = append(e.b, 1)
	e.pad()
	e.b = append(e.b, make([]byte, 12)...)
	e.str("")
	return e
}

func TestContainer(t *testing.T) {
	e := mono()
	e.f32(4)
	e.f32(2.5)
	e.f32(0.6)
	e.b = append(e.b, 0)
	e.pad()
	e.u32(2)
	e.str("tool")
	e.str("gun")
	c, ok := container(e.b)
	if !ok || c.Capacity != 4 || c.PerItem != 2.5 || c.Encumbrance != 0.6 || len(c.Tags) != 2 || c.Tags[1] != "gun" {
		t.Fatalf("container %+v %v", c, ok)
	}
	if _, ok := container(append(e.b, 0, 0, 0, 0)); ok {
		t.Fatal("data with extra bytes read as a container")
	}
}

func TestBuilding(t *testing.T) {
	drop := func(e *enc, id string, ch, lo, hi float32) {
		e.str(id)
		e.f32(ch)
		e.f32(lo)
		e.f32(hi)
	}
	e := mono()
	e.u32(2)
	drop(e, "scrapmetal", 1, 0, 1)
	drop(e, "CircuitBoard", 0.35, 0, 1)
	e.f32(50)
	e.u32(0)
	e.u32(0)
	e.b = append(e.b, 1)
	e.pad()
	e.str("spikestabber")
	e.str("")
	e.str("")
	e.b = append(e.b, 0)
	e.pad()
	e.f32(1)
	e.u32(0)
	e.u32(0)
	b, ok := building(e.b)
	if !ok || b.ID != "spikestabber" || len(b.Drops) != 2 || b.Drops[1].ID != "circuitboard" || b.Drops[1].Chance != 0.35 || len(b.Always) != 0 {
		t.Fatalf("building %+v %v", b, ok)
	}
}

func TestPackAtlas(t *testing.T) {
	icons := map[string]*image.NRGBA{"a": image.NewNRGBA(image.Rect(0, 0, 40, 30)), "b": image.NewNRGBA(image.Rect(0, 0, 20, 11)), "c": image.NewNRGBA(image.Rect(0, 0, 64, 64))}
	icons["a"].Pix[0] = 9
	img, idx, err := packAtlas(icons)
	if err != nil {
		t.Fatal(err)
	}
	if idx["c"] != [4]int{0, 0, 64, 64} || idx["a"] != [4]int{65, 0, 40, 30} || idx["b"] != [4]int{106, 0, 20, 11} {
		t.Fatalf("index %v", idx)
	}
	if img.Pix[img.PixOffset(65, 0)] != 9 {
		t.Fatal("icon a not drawn at its place")
	}
	icons["d"] = image.NewNRGBA(image.Rect(0, 0, 65, 2))
	if _, _, err := packAtlas(icons); err == nil {
		t.Fatal("icon larger than 64 px accepted")
	}
}

// texture2D writes a Texture2D object in the layout that texture reads.
func texture2D(w, h int, format uint32, pix []byte) []byte {
	e := &enc{}
	e.str("tex")
	e.zero(4 + 1 + 1)
	e.pad()
	e.u32(uint32(w))
	e.u32(uint32(h))
	e.zero(4 + 4)
	e.u32(format)
	e.zero(4 + 3)
	e.pad()
	e.str("")
	e.zero(1)
	e.pad()
	e.zero(4 + 4 + 4 + 24 + 4 + 4)
	e.u32(0)
	e.pad()
	e.u32(uint32(len(pix)))
	e.b = append(e.b, pix...)
	return e.b
}

// sprite writes a Sprite object with a pixel rect and a texture pointer.
func sprite(rect [4]float32, atlas, tex int64) []byte {
	e := &enc{}
	e.str("spr")
	for _, v := range rect {
		e.f32(v)
	}
	e.zero(8 + 16 + 4 + 8 + 4)
	e.zero(1)
	e.pad()
	e.zero(24)
	e.u32(1)
	e.str("tag")
	e.ptr(atlas)
	e.ptr(tex)
	return e.b
}

// serialized writes a version 22 serialized file without type trees. Each object gets its own type entry.
func serialized(objs map[int64]object) []byte {
	e := &enc{}
	e.zero(48)
	binary.BigEndian.PutUint32(e.b[8:], 22)
	e.b = append(e.b, "2022.3.0f1"...)
	e.zero(1)
	e.u32(19)
	e.zero(1)
	var ids []int64
	for id := range objs {
		ids = append(ids, id)
	}
	e.u32(uint32(len(ids)))
	for _, id := range ids {
		e.u32(uint32(objs[id].class))
		e.zero(3)
		if objs[id].class == classMonoBehaviour {
			e.zero(16)
		}
		e.zero(16)
	}
	e.u32(uint32(len(ids)))
	var data []byte
	for i, id := range ids {
		e.pad()
		e.i64(id)
		e.i64(int64(len(data)))
		e.u32(uint32(len(objs[id].data)))
		e.u32(uint32(i))
		data = append(data, objs[id].data...)
		data = append(data, make([]byte, (8-len(data)%8)%8)...)
	}
	e.pad()
	binary.BigEndian.PutUint64(e.b[32:], uint64(len(e.b)))
	return append(e.b, data...)
}

func TestAssets(t *testing.T) {
	// A 3x2 RGBA texture. Unity stores the bottom row first.
	pix := []byte{
		1, 0, 0, 10, 2, 0, 0, 20, 3, 0, 0, 30,
		4, 0, 0, 40, 5, 0, 0, 50, 6, 0, 0, 60,
	}
	objs := map[int64]object{
		5: {classTexture2D, texture2D(3, 2, 4, pix)},
		6: {classSprite, sprite([4]float32{1, 0, 2, 2}, 0, 5)},
		7: {classSprite, sprite([4]float32{2, 0, 2, 2}, 0, 5)},
		8: {classSprite, sprite([4]float32{0, 0, 1, 1}, 9, 5)},
		9: {classTexture2D, texture2D(1, 1, 4, nil)},
	}
	p := filepath.Join(t.TempDir(), "resources.assets")
	if err := os.WriteFile(p, serialized(objs), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := openAssets(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.objs) != len(objs) || !bytes.Equal(f.objs[5].data, objs[5].data) || f.objs[6].class != classSprite {
		t.Fatalf("read %d objects", len(f.objs))
	}
	img, err := f.spriteImage(pptr{0, 6})
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{5, 0, 0, 50, 6, 0, 0, 60, 2, 0, 0, 20, 3, 0, 0, 30}
	if img.Bounds().Dx() != 2 || img.Bounds().Dy() != 2 || !bytes.Equal(img.Pix, want) {
		t.Fatalf("sprite pixels %v, want %v", img.Pix, want)
	}
	for id, what := range map[int64]string{7: "rect outside the texture", 8: "packed sprite"} {
		if _, err := f.spriteImage(pptr{0, id}); err == nil {
			t.Errorf("%s accepted", what)
		}
	}
	if _, err := f.spriteImage(pptr{0, 5}); err == nil {
		t.Error("texture read as a sprite")
	}
	if _, err := f.texture(pptr{0, 9}, [4]int{0, 0, 1, 1}); err == nil {
		t.Error("texture without inline pixels accepted")
	}
	rgb, err := f.texture(pptr{0, 5}, [4]int{0, 0, 1, 1})
	if err != nil || !bytes.Equal(rgb.Pix, []byte{1, 0, 0, 10}) {
		t.Fatalf("texture %v %v", rgb, err)
	}

	bad := serialized(objs)
	binary.BigEndian.PutUint32(bad[8:], 21)
	if err := os.WriteFile(p, bad, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openAssets(p); err == nil {
		t.Error("version 21 accepted")
	}
	if err := os.WriteFile(p, []byte("short"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := openAssets(p); err == nil {
		t.Error("short file accepted")
	}
}
