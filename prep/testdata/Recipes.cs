public static class Recipes
{
    public static void SetUpRecipes()
    {
        recipes = new List<Recipe>();
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
    recipes.Add(new Recipe() {
        INT = -1,
        result = new RecipeResult { id = "water", isLiquid = true },
        items = new List<RecipeItem> {
            new RecipeItem(1f) { specificId = "groundwater", isLiquid = true },
            new RecipeItem { quality = "heatsource", minimumCondition = 0.2f, destroyItem = false, noMaxCondition = true }
        },
        isRepair = false
    });
		recipes.Add(new Recipe
		{
			INT = 7,
			result = new RecipeResult
			{
				id = "knife"
			},
			items = new List<RecipeItem>
			{
				// a } in a comment
				new RecipeItem
				{
					specificId = "scrapmetal",
					minimumCondition = 0f
				}
			},
			isRepair = true
		});
    }
}
