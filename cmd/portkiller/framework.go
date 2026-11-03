package main

import (
	"path/filepath"
	"strings"
)

// fwRule names the framework behind a launcher binary or script. sub, when
// set, must appear later on the command line (manage.py runserver, ng serve).
type fwRule struct {
	sub  string
	name string
}

// fwRules is keyed by the base name of an argv entry, script extension
// stripped, so node_modules/.bin/vite and vite/bin/vite.js both match.
var fwRules = map[string][]fwRule{
	"vite":               {{"", "Vite"}},
	"next":               {{"", "Next.js"}},
	"nuxt":               {{"", "Nuxt"}},
	"nuxi":               {{"", "Nuxt"}},
	"astro":              {{"", "Astro"}},
	"remix":              {{"", "Remix"}},
	"remix-serve":        {{"", "Remix"}},
	"svelte-kit":         {{"", "SvelteKit"}},
	"ng":                 {{"serve", "Angular"}},
	"react-scripts":      {{"", "Create React App"}},
	"webpack-dev-server": {{"", "webpack"}},
	"webpack":            {{"serve", "webpack"}},
	"parcel":             {{"", "Parcel"}},
	"nest":               {{"", "NestJS"}},
	"storybook":          {{"", "Storybook"}},
	"start-storybook":    {{"", "Storybook"}},
	"manage.py":          {{"runserver", "Django"}},
	"django-admin":       {{"runserver", "Django"}},
	"django":             {{"runserver", "Django"}},
	"flask":              {{"", "Flask"}},
	"uvicorn":            {{"", "Uvicorn"}},
	"gunicorn":           {{"", "Gunicorn"}},
	"hypercorn":          {{"", "Hypercorn"}},
	"daphne":             {{"", "Daphne"}},
	"streamlit":          {{"", "Streamlit"}},
	"jupyter-lab":        {{"", "Jupyter"}},
	"jupyter-notebook":   {{"", "Jupyter"}},
	"http.server":        {{"", "Python http.server"}},
	"rails":              {{"server", "Rails"}, {"s", "Rails"}},
	"puma":               {{"", "Puma"}},
	"artisan":            {{"serve", "Laravel"}},
	"php":                {{"-S", "PHP built-in server"}},
	"hugo":               {{"server", "Hugo"}},
	"jekyll":             {{"serve", "Jekyll"}},
	"mix":                {{"phx.server", "Phoenix"}},
	"air":                {{"", "Air"}},
}

// fwScanArgs bounds how far into argv a launcher is looked for: the
// interpreter, a wrapper (npx, -m) and the tool itself. Later entries are
// user arguments that could merely be named like a tool.
const fwScanArgs = 4

// detectFramework (pure) names the dev server or framework behind a command
// line, or "" when none is recognised.
func detectFramework(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	// Next.js renames its process title; `go run` executes a temp build.
	switch {
	case strings.HasPrefix(argv[0], "next-server"):
		return "Next.js"
	case strings.Contains(argv[0], "/go-build"):
		return "go run"
	}
	for i := 0; i < len(argv) && i < fwScanArgs; i++ {
		base := filepath.Base(argv[i])
		for _, ext := range []string{".js", ".mjs", ".cjs"} {
			base = strings.TrimSuffix(base, ext)
		}
		for _, r := range fwRules[base] {
			if r.sub == "" || containsArg(argv[i+1:], r.sub) {
				return r.name
			}
		}
	}
	return ""
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// procFramework reads pid's command line and names its framework.
func procFramework(pid string) string {
	b, err := procFile("/proc/" + pid + "/cmdline")
	if err != nil {
		return ""
	}
	return detectFramework(parseCmdline(b))
}
