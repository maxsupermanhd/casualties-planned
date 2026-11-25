public static void SetUpItems()
{
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
	GlobalItems.Add("lamp", new LiquidItemInfo {
		category = "utility",
		weight = 1f,
		useAction = delegate(Body body, Item item)
		{
			item.Say("a { brace", $"{body.name}: {(item.on ? "on" : "off")} }}", '}', @"a ""}"" b");
			weight = 9f;
		},
		wearSlotId = "hat",
		wearableArmor = 0.4f,
		destroyAtZeroCondition = true,
		scaleWeightWithCondition = true,
		qualities = new List<CraftingQuality> { new CraftingQuality("heatsource") }
	});
}
