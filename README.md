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
~/s/g/f/xscript $ xscript -F /tmp/foo
xscript: started, output file is /tmp/foo-1791288301
● ~/s/g/f/xscript $ echo "hello"
hello
● ~/s/g/f/xscript $ exit
exit
xscript: done, output file is /tmp/foo-1791288301
```

Using the CLI to record a session with remote control enabled:
```
~/s/g/f/xscript $ xscript -F /tmp/foo -s
xscript: started, output file is /tmp/foo-1791288477, remote socket is /tmp/foo-1791288477.socket
● ~/s/g/f/xscript $ echo hello
hello
● ~/s/g/f/xscript $ exit
xscript: done, output file is /tmp/foo-1791288477
```

Remote controlling a session over Unix socket using netcat:
```
~/s/g/f/xscript $ nc -U /tmp/foo-1791288527.socket
ls /tmp/foo-*
ls /tmp/foo-*
/tmp/foo-1791288301  /tmp/foo-1791288527
/tmp/foo-1791288477  /tmp/foo-1791288527.socket
● ~/s/g/f/xscript $ exit
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
