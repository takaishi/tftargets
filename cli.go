package tftargets

import (
	"context"
	"fmt"

	"github.com/alecthomas/kong"
)

var Version = "dev"
var Revision = "HEAD"

type GlobalOptions struct {
	BaseBranch  string `help:"Base branch for comparison" default:"main"`
	BaseDir     string `help:"Base directory" default:"."`
	SearchPath  string `help:"Search path" default:"."`
	BaseTargets string `help:"Base targets (JSON array)"`
}

type CLI struct {
	Version        VersionFlag `name:"version" help:"show version"`
	*GlobalOptions `embed:""`
}

type VersionFlag string

func (v VersionFlag) Decode(ctx *kong.DecodeContext) error { return nil }
func (v VersionFlag) IsBool() bool                         { return true }
func (v VersionFlag) BeforeApply(app *kong.Kong, vars kong.Vars) error {
	fmt.Printf("%s-%s\n", Version, Revision)
	app.Exit(0)
	return nil
}

func RunCLI(ctx context.Context, args []string) error {
	cli := CLI{
		Version: VersionFlag("0.1.0"),
	}
	parser, err := kong.New(&cli)
	if err != nil {
		return fmt.Errorf("error creating CLI parser: %w", err)
	}
	_, err = parser.Parse(args)
	if err != nil {
		fmt.Printf("error parsing CLI: %v\n", err)
		return fmt.Errorf("error parsing CLI: %w", err)
	}
	app := New(&cli)
	return app.Run(ctx)
}
