# Reused weather code

Provider validation, ZIP matching, weather-condition mapping and solar day/night calculations in `internal/weather/` adapt code from [A Weather App](https://github.com/joega/a-weather-app). Static sky colors and cloud shapes draw on the browser atmosphere in the separate A Weather Site project. Both original projects use the following MIT license.

MIT License

Copyright (c) 2026 Joe Gaetano (https://github.com/joega/a-weather-app)

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.


Weather data is supplied by [Open-Meteo](https://open-meteo.com/); provider attribution is also visible in the dashboard.

# Cookbook adaptations

The saved-recipe/photo workflow, bounded provider normalization, ingredient check-off
and missing-ingredient selection, and recipe-detail working panes adapt
[Kitchen Frame](https://github.com/huntsyea/kitchen-frame), especially
`dashboard/worker/recipes.js`, `dashboard/src/views/Recipes.svelte` and
`dashboard/src/views/RecipePage.svelte`. These are ported to Go, HTML/HTMX and local
SQLite rather than introducing its Svelte/Cloudflare/Todoist runtime.
Provider exploration credits TheMealDB in the interface. Schema.org import, editable
household recipes, conflict checks, shopping transactions and native cooking-mode
brightness behavior are implemented for this dashboard.

MIT License

Copyright (c) 2026 Hunter Yeagley

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.

# Sports adaptations

The five Boston team identities, provider status/score normalization and bounded
cache/polling behavior in `internal/sports/` adapt the MIT-licensed
[Sportray](https://github.com/joega/sportray) provider/model code and fixture cases.
They are implemented in Go for this dashboard. Schedule data comes from ESPN,
NHL and MLB StatsAPI; team logo PNGs come from ESPN's reviewed asset host and
are cached locally. Provider content and team marks remain owned by their
respective providers/teams and are not covered by the application's MIT license.

MIT License

Copyright (c) 2026 Joe G

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
