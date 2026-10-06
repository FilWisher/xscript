# xscript

xscript is a close copy of the Unix `script` command. It uses the same
timestamped recording format.

It adds support for remote writers over Unix domain sockets and a library for
parsing commands and output from recordings. 

The parsing library assumes your shell is configured with OSC 133 semantic
prompt features:
- `A`: for prompt start
- `B`: for input start
- `C`: for command execution start
- `D`: for command execution complete plus status code

It also assumes your shell is configured to use OSC 7 for the current working
directory. 

## Example

Using the CLI to record a session:

```
$ xscript -F /tmp/foo
xscript: started, output file is /tmp/foo-1791288301
● $ echo "hello"
hello
● $ exit
exit
xscript: done, output file is /tmp/foo-1791288301
```

Using the CLI to record a session with remote control enabled:
```
$ xscript -F /tmp/foo -s
xscript: started, output file is /tmp/foo-1791288477, remote socket is /tmp/foo-1791288477.socket
● $ echo hello
hello
● $ exit
xscript: done, output file is /tmp/foo-1791288477
```

Remote controlling a session over Unix socket using netcat:
```
$ nc -U /tmp/foo-1791288527.socket
ls /tmp/foo-*
ls /tmp/foo-*
/tmp/foo-1791288301  /tmp/foo-1791288527
/tmp/foo-1791288477  /tmp/foo-1791288527.socket
● $ exit
exit
```

Using the library to parse recordings:
```
f, _ := os.Open("/tmp/foo-1791288301")

// ReadRecords parses the raw records in the recording file.
records, _ := xscript.ReadRecords(f)

// Replay parses the commands, output, and exit codes by replays the recordings
// in a headless virtual terminal 
cmds := xscript.Replay("/tmp/foo-1791288301", records, xscript.ReplayOptions{})
```

## CLI usage

```
$ xscript --help
Usage of xscript:
  -F <recordfile>
    	Specify the <recordfile> in which to record. (default "typescript")
  -a	Append the output to file or typescript, retaining prior contents.
  -d	When playing back a session with the -p flag, do not sleep between records when playing back a timestamped session.
  -e	The child command exit status is always the exit status of the script.
  -l <logfile>
    	The <logfile> in which to log errors.
  -p <file>
    	Play back a session in real time from <file>.
  -s	Enable remote control via <recordfile>.socket (use -socket to override).
  -socket <path>
    	Specify the <path> of the unix socket for remote control. Implies -s.
```
