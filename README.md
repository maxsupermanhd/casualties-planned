> **Disclaimer:** This project is fully vibecoded. An AI wrote all of the code and the docs. Use it at your own risk.

# cuplan

cuplan makes a crafting planner page for the game Casualties Unknown.

The page is one HTML file. It contains the game data, the item icons, the styles and the planner. The planner
runs in your browser. You set the targets and the rules in the page, and the page makes the plan. The page does
not send requests and does not need a server. You can open it from a disk (`file://`) or put it on any static host.

This repository does not contain game files, decompiled game code or game images. The programs make them on your
computer, from your copy of the game.

## Programs

There are two Go programs. Run them in this sequence:

1. `prep` reads the game. It decompiles the game code with ilspycmd, reads the recipes and items, reads the item
   prefabs and the buildings from the game assets, and writes the data as JSON files. It also puts all item icons
   into one texture atlas.
2. `build` makes the page. It compiles the TypeScript code in `build/ts/` with esbuild (as a Go library) and puts
   the code, the data, the atlas and the styles into one HTML file.

```
cp config.example.json config.json
# edit gameDir in config.json
go run ./prep config.json
go run ./build config.json
```

Then open `work/cuplan.html` in a browser.

To publish the page, copy it to `site/index.html` and push. The workflow `.github/workflows/pages.yml` deploys the
`site/` folder to GitHub Pages. CI cannot build the page, because it has no game files. In the repository settings,
set Pages to the source "GitHub Actions".

## Requirements

- Go 1.23 or later. The Go programs get esbuild and lac as Go modules. You do not need Node.js or npm.
- ilspycmd, the command-line ILSpy decompiler. Only `prep` uses it.
- Your installed copy of Casualties Unknown.

ilspycmd is a .NET tool. Install the .NET SDK, then install the tool:

```
dotnet tool install --global ilspycmd --version 8.2.0.7535
```

The extractor was tested with ilspycmd 8.2.0.7535. A different version can write the C# code in a different form.
Then `prep` can fail. ilspycmd 8.2 needs the .NET 6 runtime. On a newer runtime, keep `DOTNET_ROLL_FORWARD=Major`
in `decompiler.env`. If .NET is not in a standard location, add `DOTNET_ROOT` to `decompiler.env`.

## Config

Both programs read the same JSON file. Relative paths are relative to the folder of the config file.

- `gameDir`: the game folder. It contains the folder `CasualtiesUnknown_Data`.
- `gameDataDir`: the data folder in `gameDir` (`CasualtiesUnknown_Data`).
- `decompiler.command`: the ilspycmd command, or a path to it.
- `decompiler.args`: the arguments. `{dll}` is the game DLL, `{out}` is the output folder.
- `decompiler.env`: extra environment variables for the decompiler.
- `workDir`: the folder for the output of `prep` (`work`).
- `page`: the HTML file that `build` writes (`work/cuplan.html`).

`prep` reads these game files. It does not change them:

- `Managed/Assembly-CSharp.dll`: recipes and items (from `Recipes.cs` and `Item.cs`).
- `resources.assets`: item icons, bag data (Container) and building drops (BuildingEntity).
- `Lang/EN.json`: English item names.

`prep` writes these files to `<workDir>/data/`:

- `recipes.json`, `items.json`, `names.json`: recipes, item data (weight, armor, wear slot, tags, qualities,
  liquid capacity) and English names.
- `containers.json`: bag capacity, maximum weight for each item, encumbrance and allowed tags, from the prefabs.
- `buildings.json`: the item drops of each building and trap, from the prefabs.
- `rules.json`: game rules that are not in a data file: layers, XP values, the INT failure rules, the layers of each
  raw resource and trap, and the magazines. `prep` copies this file from `prep/rules.json`.
- `atlas.png`, `atlas.json`: the icon atlas and the position of each icon (`id: [x, y, w, h]`).

The decompiled code goes to `<workDir>/decomp/`. Do not publish the `work/` folder.

## The page

The page opens on the plan. It plans with the settings that you used last. The first time, it uses the defaults.

- **Plan view**: one step at a time. Use the bottom bar (previous, next, jump menu), the slider, a swipe on the card,
  or the keys (left, right, Home, End, J for the jump menu, L for the list view, S for the setup). The jump menu
  goes to a step, a layer or the next bottleneck. Each step shows what to gather and craft, the INT and XP, and
  the inventory after the step: the hands, the mouth, the worn items and each bag. The summary shows the final kit,
  the total raw resources and all bottlenecks. The top box shows each target that the plan does not reach, and
  what blocks it (INT, INT cap, layer, storage, carry cap or recipe).
- **Setup**: all settings. Push **Plan** to make a new plan. The planner runs in a Web Worker, so the page stays
  responsive.
  - Targets: add items with the item picker, set the count, change the sequence, or use a preset.
  - Character: start INT, carry cap, back slots, layer limit, XP values.
  - Start items, lights, the water holder, the bag to craft when the items do not fit, and the biochem rule (the
    first canteen, and then the IV bag, is always full of biochem).
  - Rules: XP filler crafts, gun crafts for XP, an INT cap, "Always craft with enough INT", the d = -1 gamble on
    the last layer and its result condition, recipes that the planner must not use, and items that it must not craft only for XP.
  - Resources and traps: the layers and the condition of each raw resource. Clear the box to remove a resource.
    The trap drops come from the game prefabs. You can also carry extra items down from a layer.
  - Settings file: export the settings as JSON, import a JSON file, copy a share link (the settings go in the
    URL after `#cfg=`), reset to the defaults, or edit all settings as JSON.

The page keeps the settings, the step and the view in the local storage of the browser.

The default settings are in `build/defaults.json`. `build` adds the values from `rules.json` that
`defaults.json` does not set.

## Planner rules

The planner is `build/ts/planner.ts`. These rules come from the game code and the game data:

- You see a recipe when INT >= recipe INT - 3. Let d = INT - recipe INT. If d >= 0, you get the full result. If
  d = -1, there is a 50% chance of 20-90% condition. If d = -2, there is a 50% chance of nothing. If d = -3, there is
  a 50% chance of nothing and a hand injury. You lose the ingredients in all cases.
- XP: the first craft of a recipe gives 10 XP, also if it fails. A descent gives 80 XP.
- An item in a kept recipe slot loses condition for each craft (needed amount / item amount). At 0% the game
  destroys it. The game has no other tool rule.
- A tool is an item with the game tag "tool" (for example knife, cleaver, wrench, machete). At 0% a tool breaks,
  and the plan shows "tool breaks".
- Canvas, nails, sticks and other items with the tag "material" are not tools. A kept slot uses them up. The plan
  shows this as "Materials used (kept slot)" and "used up", not as a tool break.
- Load is the weight in the body slots, plus worn items, plus bag contents multiplied by the bag encumbrance.
- The body has 2 hands, 1 mouth and 3 back slots. Wear slots are separate.

The planner obeys these rules:

- It crafts only at d >= 0. On the last layer it can craft the gamble items at d = -1, and it assumes the worst
  condition (the gamble result condition).
- "Always craft with enough INT" (`options.safeInt`, default off): the planner crafts only at d >= 0, also on
  the last layer. No craft gives a lower condition, fails, injures a hand or wears a tool for no result. This
  setting overrides the gamble setting, and the page disables the gamble controls. The planner gets XP first (XP
  crafts and descents) and crafts a target only when INT is sufficient. A target that needs more INT than the plan
  gets by the layer limit is in the missed targets, blocked by INT (for example "needs INT 16 (have 15)").
- You never leave an item behind. You carry every item to the next layer. The only exception is an INT-up craft (a
  craft only for the first-craft XP): you discard its result immediately. A liquid result of an INT-up craft does
  not need a canteen or other holder. The planner does not craft a holder for it, and it uses no slot or capacity.
  A liquid result that you keep needs a free canteen.
- If the items do not fit, the planner crafts a bag for a free back slot. If they still do not fit, it tries the
  target again later.
- When no target and no XP craft is possible, it descends, until the layer limit.

These are assumptions. The game code does not confirm them:

- You start with a lightbulb. You mine copper. Circuit boards come from traps at the mean condition (50%).
- One hand always holds the light. The mouth stays empty. Back slots hold only bags.
- Time and resources on a layer are not limited. Drop pods, loot and traders are not used, so ePDA and blueprint XP
  are not counted.

## Test

```
go vet ./...
go test ./...
```

`build/main_test.go` has an equivalence test. It runs the TypeScript planner in headless Chromium with the default
settings, and compares the plan step by step with a reference plan. The reference plan came from the earlier Go
planner. It was then made again after two changes: the planner discards the liquid result of an INT-up craft, and
the wear of canvas and nails moves from `toolWear` and `breaks` to `matWear` and `usedUp`. The steps, amounts and
wear values did not change. It needs the `prep` output in `work/data/`, the reference plan in `work/ref/steps.json` (or set `CUPLAN_REF`), and Chromium in
`PATH` (or set `CHROME`). If one is missing, the test is skipped.

`TestSafeInt` makes a plan with `options.safeInt` on. It fails if a craft step has d < 0, a gamble note, or an INT
before the craft that is less than the recipe INT.

`TestKeptMaterials` fails if an item without the game tag "tool" has tool wear or a tool break, or if a tool is
"used up".

To check the TypeScript types, use the TypeScript compiler with `tsconfig.json` (strict mode):

```
tsc -p tsconfig.json
```

The build does not need this step. esbuild does not check types.

## License

The owner has not chosen a license yet.
