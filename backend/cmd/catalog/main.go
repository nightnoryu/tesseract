package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/nightnoryu/tesseract/internal/semantic/infrastructure"
)

func main() { os.Exit(run(os.Args[1:], os.Stdout, os.Stderr)) }
func run(args []string, out, errOut io.Writer) int {
	if len(args) == 0 || args[0] != "validate" {
		fmt.Fprintln(errOut, "usage: catalog validate [-dir catalog]")
		return 2
	}
	flags := flag.NewFlagSet("validate", flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "catalog", "catalog directory")
	if err := flags.Parse(args[1:]); err != nil {
		return 2
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(errOut, "unexpected arguments")
		return 2
	}
	c, err := infrastructure.Load(os.DirFS(*dir))
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	hash, err := c.Hash()
	if err != nil {
		fmt.Fprintln(errOut, err)
		return 1
	}
	fmt.Fprintf(out, "Catalog valid: %d sources, %d datasets, sha256:%s\n", len(c.Sources), len(c.Datasets), hash)
	return 0
}
