package main

import (
	"context"
	"flag"
	"log"
	"os"

	"github.com/filwisher/xscript"
)

// TODO: install script under bin
// TODO: test that MCP read paths work with script
// TODO: update MCP to support writing commands to recording
// TODO: update MCP to support listing open recordings

func run() (*xscript.Output, error) {
	var options xscript.Options
	flag.StringVar(&options.File, "F", options.Default().File, "Specify the filename in which to record.")
	flag.BoolVar(&options.Append, "a", options.Default().Append, "Append the output to file or typescript, retaining prior contents.")
	flag.BoolVar(&options.Remote, "s", options.Default().Remote, "Enable remote control over unix socket.")
	flag.StringVar(&options.SocketFile, "socket", options.Default().SocketFile, "Specify the path of the unix socket for remote control. Implies -s. Defaults to `<recordfile>.socket`.")
	flag.BoolVar(&options.UseChildExit, "e", options.Default().UseChildExit, "The child command exit status is always the exit status of the script.")
	flag.StringVar(&options.ErrorLogFile, "l", options.Default().ErrorLogFile, "The file in which to log errors.")
	flag.Parse()

	options.Cmd = flag.Args()

	ctx := context.Background()

	return xscript.Run(ctx, options)
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("xscript: ")

	out, err := run()
	if out != nil {
		log.Printf("done, output file is %s", out.File)
	}

	if out != nil && out.Options.UseChildExit {
		if err != nil {
			log.Println(err)
		}
		os.Exit(out.ExitCode)
		return
	}
	if err != nil {
		log.Fatal(err)
	}
}
