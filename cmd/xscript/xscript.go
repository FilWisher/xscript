package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"os"
	"time"

	"github.com/filwisher/xscript"
)

// TODO: install script under bin
// TODO: test that MCP read paths work with script
// TODO: update MCP to support writing commands to recording
// TODO: update MCP to support listing open recordings

func run() (*xscript.Output, error) {
	var options xscript.Options
	flag.StringVar(&options.File, "F", options.Default().File, "Specify the `<recordfile>` in which to record.")
	flag.BoolVar(&options.Append, "a", options.Default().Append, "Append the output to file or typescript, retaining prior contents.")
	flag.BoolVar(&options.Remote, "s", options.Default().Remote, "Enable remote control via <recordfile>.socket (use -socket to override).")
	flag.StringVar(&options.SocketFile, "socket", options.Default().SocketFile, "Specify the `<path>` of the unix socket for remote control. Implies -s.")
	flag.BoolVar(&options.UseChildExit, "e", options.Default().UseChildExit, "The child command exit status is always the exit status of the script.")
	flag.StringVar(&options.ErrorLogFile, "l", options.Default().ErrorLogFile, "The `<logfile>` in which to log errors.")
	flag.StringVar(&options.PlayFile, "p", options.Default().PlayFile, "Play back a session in real time from `<file>`.")
	flag.BoolVar(&options.DisablePlayDelay, "d", options.Default().DisablePlayDelay, "When playing back a session with the -p flag, do not sleep between records when playing back a timestamped session.")
	flag.Parse()

	options.Cmd = flag.Args()

	ctx := context.Background()

	if options.DisablePlayDelay && options.PlayFile == "" {
		return nil, errors.New("-d must be used in combination with -p")
	}

	if options.PlayFile != "" {	
		err := replay(options)
		return nil, err
	}

	out, err := xscript.Run(ctx, options)
	if out != nil {
		log.Printf("done, output file is %s", out.File)
	}
	return out, err
}

func replay(options xscript.Options) error {
	file, err := os.Open(options.PlayFile)
	if err != nil {
		return err
	}
	records, err := xscript.ReadRecords(file)
	if err != nil {
		return err
	}

	if len(records) == 0 {
		return errors.New("no records found")
	}

	if records[0].Direction != 's' {
		return errors.New("bad format: expected initial start record")
	}

	origStart := records[0].Time
	records = records[1:]

	log.Printf("starting replay")
	start := time.Now()
	for _, record := range records {
		if record.Direction != 'o' {
			continue
		}
		due := record.Time
		delta := time.Since(start)
		wait := due.Sub(origStart.Add(delta))
		if !options.DisablePlayDelay {
			<-time.After(max(wait, 0))
		}
		os.Stdout.Write(record.Data)
	}

	log.Printf("finished replay")
	return nil
}

func main() {
	log.SetFlags(0)
	log.SetPrefix("xscript: ")

	out, err := run()
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
