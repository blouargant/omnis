package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/blouargant/yoke/core/permissions"
)

// runPermissions implements `yoke permissions <convert|import>` — the upgrade
// path from old-format yoke permissions.json and from Claude Code settings.json
// to yoke's new (Claude-nomenclature) permissions format.
func runPermissions(args []string) error {
	if len(args) == 0 {
		printPermissionsUsage()
		return fmt.Errorf("a subcommand is required (convert or import)")
	}
	switch args[0] {
	case "convert":
		return runPermissionsConvert(args[1:])
	case "import":
		return runPermissionsImport(args[1:])
	case "help", "-h", "--help":
		printPermissionsUsage()
		return nil
	default:
		printPermissionsUsage()
		return fmt.Errorf("unknown subcommand %q", args[0])
	}
}

func printPermissionsUsage() {
	fmt.Fprint(os.Stderr, `Usage:
  yoke permissions convert [-w] <file|->   Upgrade an old-format yoke
                                           permissions.json to the new
                                           nomenclature. -w rewrites in place
                                           (with a .bak backup).
  yoke permissions import  [-o out] <file|->  Convert a Claude Code
                                           settings.json (or its permissions
                                           block) into a yoke permissions.json.
`)
}

func readSource(src string) ([]byte, error) {
	if src == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(src)
}

func marshalConfig(cfg *permissions.Config) ([]byte, error) {
	out, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func runPermissionsConvert(args []string) error {
	fs := flag.NewFlagSet("permissions convert", flag.ContinueOnError)
	write := fs.Bool("w", false, "Rewrite the file in place (keeps a .bak backup)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("file argument is required (use - for stdin)")
	}
	src := fs.Arg(0)
	content, err := readSource(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	cfg, err := permissions.ParseBytes(content)
	if err != nil {
		return fmt.Errorf("parse %s: %w", src, err)
	}
	out, err := marshalConfig(cfg)
	if err != nil {
		return err
	}
	if *write {
		if src == "-" {
			return fmt.Errorf("-w cannot be used with stdin")
		}
		if err := os.WriteFile(src+".bak", content, 0o644); err != nil {
			return fmt.Errorf("write backup: %w", err)
		}
		if err := os.WriteFile(src, out, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", src, err)
		}
		fmt.Fprintf(os.Stderr, "converted %s (backup at %s.bak)\n", src, src)
		return nil
	}
	_, err = os.Stdout.Write(out)
	return err
}

func runPermissionsImport(args []string) error {
	fs := flag.NewFlagSet("permissions import", flag.ContinueOnError)
	outPath := fs.String("o", "", "Write the converted permissions.json to this path (default: stdout)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return fmt.Errorf("file argument is required (use - for stdin)")
	}
	src := fs.Arg(0)
	content, err := readSource(src)
	if err != nil {
		return fmt.Errorf("read %s: %w", src, err)
	}
	cfg, warnings, err := permissions.ImportClaudeSettings(content)
	if err != nil {
		return fmt.Errorf("import %s: %w", src, err)
	}
	for _, w := range warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}
	out, err := marshalConfig(cfg)
	if err != nil {
		return err
	}
	if *outPath != "" {
		if err := os.WriteFile(*outPath, out, 0o644); err != nil {
			return fmt.Errorf("write %s: %w", *outPath, err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d allow / %d ask / %d deny rules)\n",
			*outPath, len(cfg.Permissions.Allow), len(cfg.Permissions.Ask), len(cfg.Permissions.Deny))
		return nil
	}
	_, err = os.Stdout.Write(out)
	return err
}
