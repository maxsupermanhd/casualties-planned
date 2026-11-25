package main

import (
	"encoding/binary"
	"image"
	"math"
	"testing"
)

const recipeSrc = `
		recipes.Add(new Recipe
		{
			INT = 3,
			result = new RecipeResult
			{
				id = "rope",
				amount = 2,
				resultCondition = 0.5f
			},
			items = new List<RecipeItem>
			{
				new RecipeItem(0f)
				{
					specificId = "foliage"
				},
				new RecipeItem
				{
					quality = new CraftingQuality("cutting", 2f),
					destroyItem = false
				}
			},
			category = RecipeCategory.Tools
		});
`

const itemSrc = `
		GlobalItems.Add("rope", new ItemInfo
		{
			weight = 0.25f,
			capacity = 300f,
			tags = "material",
			qualities = new List<CraftingQuality>
			{
				new CraftingQuality("binding", 3f)
			},
			combineable = true
		});
`

func TestParse(t *testing.T) {
	rs, err := parseRecipes(recipeSrc)
	if err != nil {
		t.Fatal(err)
	}
	if len(rs) != 1 {
		t.Fatalf("%d recipes", len(rs))
	}
	r := rs[0]
	if r.INT != 3 || r.Result != "rope" || r.Amount != 2 || r.Cond != 0.5 || r.Category != "Tools" || len(r.Items) != 2 {
		t.Fatalf("recipe %+v", r)
	}
	if a, b := r.Items[0], r.Items[1]; a.ID != "foliage" || a.MinCond != 0 || a.Kept || b.Q != "cutting" || b.Amt != 2 || !b.Kept || b.MinCond != 0.9 {
		t.Fatalf("items %+v %+v", a, b)
	}
	is, err := parseItems(itemSrc)
	if err != nil {
		t.Fatal(err)
	}
	it := is["rope"]
	if it.Weight != 0.25 || it.Capacity != 300 || it.Tags != "material" || !it.Combine || len(it.Quals) != 1 || it.Quals[0].Amt != 3 {
		t.Fatalf("item %+v", it)
	}
}

type enc struct{ b []byte }

func (e *enc) u32(v uint32)  { e.b = binary.LittleEndian.AppendUint32(e.b, v) }
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
