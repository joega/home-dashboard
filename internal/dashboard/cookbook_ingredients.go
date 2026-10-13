package dashboard

import (
	"regexp"
	"strings"
)

var ingredientMeasure = regexp.MustCompile(`(?i)^([0-9¼½¾⅓⅔⅛⅜⅝⅞][0-9¼½¾⅓⅔⅛⅜⅝⅞ ./–-]*\s*(?:cups?|tbsp|tablespoons?|tsp|teaspoons?|kg|g|grams?|ml|l|liters?|oz|ounces?|lbs?|pounds?|cloves?|cans?|bunch(?:es)?|pinch|pieces?)?)\s+(.+)$`)

func splitRecipeIngredient(line string) (string, string) {
	m := ingredientMeasure.FindStringSubmatch(strings.TrimSpace(line))
	if m != nil {
		return strings.TrimSpace(m[2]), strings.TrimSpace(m[1])
	}
	return line, ""
}
