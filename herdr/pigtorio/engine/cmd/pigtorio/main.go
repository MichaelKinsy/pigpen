// Command pigtorio is the Pigtorio helper: engine, crate CLI, fake pig, installer.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"pigtorio/internal/engine"
	"pigtorio/internal/fakepig"
	"pigtorio/internal/farmcli"
	"pigtorio/internal/hc"
	"pigtorio/internal/install"
)

const usage = `pigtorio: Factorio, but every worker is a coding agent

  pigtorio engine [--home H]
  pigtorio crate add [--home H] --title T --done-when W [--text X] [--pen ID] [--size one-shot|large] [--afk]
  pigtorio farm show [--home H] [--json]
  pigtorio ghost place --kind oven|review|barn|pen|jobboard [--pen SPACE] [--x N --y N | --near REF] [--home H] [--by NAME]
  pigtorio ghost belt --from REF --to REF
  pigtorio ghost crate --title T --done-when W [--text X] [--size one-shot|large] [--pen SPACE]
  pigtorio ghost note TEXT | list [--json] | clear
  pigtorio fakepig [--session-dir DIR] [--name N] [--work 2s]
  pigtorio install [--home H] [--src DIR] [--keys]

H defaults to $HERDR_APP_HOME, else ~/.config/herdr-app.
`

func defaultHome() string {
	if h := os.Getenv("HERDR_APP_HOME"); h != "" {
		return h
	}
	u, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(u, ".config", "herdr-app")
}

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "engine":
		err = runEngine(os.Args[2:])
	case "crate":
		err = runCrate(os.Args[2:])
	case "fakepig":
		err = runFakepig(os.Args[2:])
	case "farm":
		err = farmcli.RunFarm(os.Args[2:], os.Stdout, farmcli.DefaultDeps())
	case "ghost":
		err = farmcli.RunGhost(os.Args[2:], os.Stdout, farmcli.DefaultDeps())
	case "install":
		err = runInstall(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	case "version", "--version":
		fmt.Println(engine.Version)
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "pigtorio:", err)
		os.Exit(1)
	}
}

func runEngine(args []string) error {
	fs := flag.NewFlagSet("engine", flag.ExitOnError)
	home := fs.String("home", defaultHome(), "Herdr app home")
	fs.Parse(args)
	client, err := hc.NewCLI()
	if err != nil {
		return err
	}
	log.SetFlags(log.LstdFlags)
	e := engine.New(*home, client)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	log.Printf("pigtorio engine %s: home %s, hc %s", engine.Version, *home, client.Bin)
	err = e.Run(ctx)
	if errors.Is(err, engine.ErrLocked) {
		fmt.Println("pigtorio engine: " + err.Error() + "; exiting")
		return nil
	}
	return err
}

func runCrate(args []string) error {
	if len(args) == 0 || args[0] != "add" {
		return errors.New("usage: pigtorio crate add --title T --done-when W [--text X] [--pen ID] [--size one-shot|large] [--afk]")
	}
	fs := flag.NewFlagSet("crate add", flag.ExitOnError)
	home := fs.String("home", defaultHome(), "Herdr app home")
	title := fs.String("title", "", "crate title")
	done := fs.String("done-when", "", "how anyone knows it is done")
	text := fs.String("text", "", "what to do")
	pen := fs.String("pen", "", "pen (space id)")
	size := fs.String("size", "", "one-shot or large")
	afk := fs.Bool("afk", false, "may run away from the keyboard")
	fs.Parse(args[1:])
	crate := map[string]any{"title": *title, "text": *text, "doneWhen": *done, "pen": *pen, "afk": *afk, "askedBy": "you"}
	if *size != "" {
		crate["size"] = *size
	}
	id, err := writeInbox(*home, map[string]any{"op": "crate.add", "crate": crate})
	if err != nil {
		return err
	}
	fmt.Println(id)
	return nil
}

// writeInbox writes one command into D/inbox atomically and returns its id.
func writeInbox(home string, cmd any) (string, error) {
	dir := filepath.Join(home, "pigtorio", "inbox")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	var r [3]byte
	_, _ = rand.Read(r[:])
	id := fmt.Sprintf("%d-%s", time.Now().UnixMilli(), hex.EncodeToString(r[:]))
	b, err := json.Marshal(cmd)
	if err != nil {
		return "", err
	}
	tmp := filepath.Join(dir, "."+id+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return "", err
	}
	return id, os.Rename(tmp, filepath.Join(dir, id+".json"))
}

func runFakepig(args []string) error {
	fs := flag.NewFlagSet("fakepig", flag.ExitOnError)
	dir := fs.String("session-dir", "", "where to write the session JSONL (default D/sessions/<terminal>)")
	name := fs.String("name", "fakepig", "name")
	work := fs.Duration("work", 2*time.Second, "how long each message takes")
	home := fs.String("home", "", "Herdr app home (for the default session dir)")
	fs.Parse(args)
	if *dir == "" {
		*dir = fakepig.DefaultSessionDir(*home)
	}
	defer fakepig.RawTerminal()()
	return fakepig.Run(fakepig.Options{SessionDir: *dir, Name: *name, Work: *work, In: os.Stdin, Out: os.Stdout})
}

func runInstall(args []string) error {
	fs := flag.NewFlagSet("install", flag.ExitOnError)
	home := fs.String("home", defaultHome(), "Herdr app home")
	src := fs.String("src", "", "Pigtorio source tree (default: found next to the binary or in the cwd)")
	keys := fs.Bool("keys", false, "also add the layout:pigtorio section (cmd-shift chords) to keys.json")
	fs.Parse(args)
	if *keys {
		if err := install.CheckKeys(*home); err != nil {
			return err
		}
	}
	exe, _ := os.Executable()
	if *src == "" {
		s, err := install.FindSource(exe, *home)
		if err != nil {
			return err
		}
		*src = s
	}
	if err := install.Run(*home, *src, exe); err != nil {
		return err
	}
	fmt.Printf("installed pigtorio into %s (mod, layout, engine binary)\n", *home)
	if *keys {
		added, err := install.Keys(*home)
		if err != nil {
			return err
		}
		if added {
			fmt.Println("added the layout:pigtorio section to keys.json")
		} else {
			fmt.Println("keys.json already has a layout:pigtorio section")
		}
	}
	return nil
}
