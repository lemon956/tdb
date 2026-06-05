package cli

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

type Options struct {
	ConfigPath string
}

func ParseOptions(args []string) (Options, error) {
	fs := flag.NewFlagSet("tdb", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	opts := Options{ConfigPath: defaultConfigPath()}
	fs.StringVar(&opts.ConfigPath, "config", opts.ConfigPath, "path to encrypted tdb config file")
	if err := fs.Parse(args); err != nil {
		return Options{}, err
	}
	if fs.NArg() > 0 {
		return Options{}, fmt.Errorf("unexpected argument %q", fs.Arg(0))
	}
	return opts, nil
}

func defaultConfigPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "tdb", "tdb.enc")
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".config", "tdb", "tdb.enc")
	}
	return "tdb.enc"
}
