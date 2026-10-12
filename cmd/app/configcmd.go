package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/rdborg/mediarium/internal/configfile"
)

const configUsage = `usage:
  app config export [-o FILE]       write the current settings as YAML (to FILE, or the screen)
  app config diff FILE              show what FILE would change, changing nothing
  app config apply [-n] FILE        make Mediarium match FILE (-n: only show what would change)

Options for all of them:
  --url ADDRESS   the running Mediarium (default: $MEDIARIUM_URL, or http://127.0.0.1:$APP_PORT)
  --key KEY       an administrator's API key (default: $MEDIARIUM_API_KEY); make one on the Profile page

In Docker:  docker exec -e MEDIARIUM_API_KEY=... mediarium /app/app config apply /config/mediarium.yml
`

// runConfigCommand is "app config ...": it keeps a running Mediarium's
// settings in a YAML file. It talks to the running app through its API, so
// every value is checked as it is on the Settings pages.
func runConfigCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, configUsage)
		return 2
	}
	sub, rest := args[0], args[1:]
	fs := flag.NewFlagSet("config "+sub, flag.ContinueOnError)
	fs.SetOutput(stderr)
	port := os.Getenv("APP_PORT")
	if port == "" {
		port = "8264"
	}
	url := fs.String("url", firstEnv("MEDIARIUM_URL", "http://127.0.0.1:"+port), "")
	key := fs.String("key", os.Getenv("MEDIARIUM_API_KEY"), "")
	out := fs.String("o", "", "")
	dry := new(bool)
	fs.BoolVar(dry, "n", false, "")
	fs.BoolVar(dry, "dry-run", false, "")
	if err := fs.Parse(rest); err != nil {
		fmt.Fprint(stderr, configUsage)
		return 2
	}
	if *key == "" {
		fmt.Fprintln(stderr, "mediarium: no API key. Set MEDIARIUM_API_KEY (or use --key): make one on the Profile page of an administrator account.")
		return 2
	}
	c := &configfile.Client{BaseURL: *url, Key: *key}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch sub {
	case "export":
		data, err := configfile.Export(ctx, c)
		if err != nil {
			fmt.Fprintf(stderr, "mediarium: %v\n", err)
			return 1
		}
		if *out == "" {
			_, _ = stdout.Write(data)
			return 0
		}
		if err := os.WriteFile(*out, data, 0o600); err != nil {
			fmt.Fprintf(stderr, "mediarium: %v\n", err)
			return 1
		}
		fmt.Fprintf(stdout, "Wrote %s\n", *out)
		return 0
	case "diff", "apply":
		if fs.NArg() != 1 {
			fmt.Fprint(stderr, configUsage)
			return 2
		}
		file, err := configfile.ReadFile(fs.Arg(0))
		if err != nil {
			fmt.Fprintf(stderr, "mediarium: %v\n", err)
			return 1
		}
		applying := sub == "apply" && !*dry
		var results []configfile.Result
		if applying {
			results, err = configfile.Apply(ctx, c, file)
		} else {
			results, err = configfile.Plan(ctx, c, file)
		}
		if err != nil {
			fmt.Fprintf(stderr, "mediarium: %v\n", err)
			return 1
		}
		return printConfigResults(stdout, stderr, results, applying)
	}
	fmt.Fprint(stderr, configUsage)
	return 2
}

func printConfigResults(stdout, stderr io.Writer, results []configfile.Result, applied bool) int {
	code, changes := 0, 0
	for _, r := range results {
		for _, ch := range r.Changes {
			changes++
			fmt.Fprintln(stdout, ch)
		}
		if r.Err != nil {
			fmt.Fprintf(stderr, "mediarium: %v\n", r.Err)
			code = 1
		}
	}
	switch {
	case code != 0 && applied:
		fmt.Fprintln(stdout, "Some blocks were not applied (see above); the others were.")
	case changes == 0:
		fmt.Fprintln(stdout, "Nothing to change: Mediarium already matches the file.")
	case applied:
		fmt.Fprintf(stdout, "Applied %s.\n", countChanges(changes))
	default:
		fmt.Fprintf(stdout, "%s would be made. Run apply to make them.\n", countChanges(changes))
	}
	return code
}

func countChanges(n int) string {
	if n == 1 {
		return "1 change"
	}
	return fmt.Sprintf("%d changes", n)
}

func firstEnv(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}
