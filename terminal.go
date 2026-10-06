package xscript

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"slices"
	"sync"
	"syscall"
	"time"

	"github.com/creack/pty/v2"
	"golang.org/x/term"
)

type Terminal struct {
	Options Options
	cmd *exec.Cmd
	file string
	cleanups []func() error
	errlog *slog.Logger
}

func (tt *Terminal) cleanup(fn func () error) {
	if fn == nil {
		return
	}
	tt.cleanups = append(tt.cleanups, fn)
}

func (tt *Terminal) Close() error {
	var errs error
	for _, cleanup := range slices.Backward(tt.cleanups) {
		err := cleanup()
		errs = errors.Join(errs, err)
	}
	return errs
}

func (tt *Terminal) Wait(ctx context.Context) (_ *Output, err error) {
	defer func() {
		err = errors.Join(err, tt.Close())
	}()

	out := &Output{
		Options: tt.Options,
		File: tt.file,
	}

	err = tt.cmd.Wait()
	out.ExitCode = tt.cmd.ProcessState.ExitCode()
	if _, ok := errors.AsType[*exec.ExitError](err); !ok {
		return out, err
	}
	return out, nil
}

func Start(ctx context.Context, options Options) (*Terminal, error) {

	if options.File == "" {
		options.File = options.Default().File
	}
	if len(options.Cmd) == 0 {
		options.Cmd = options.Default().Cmd
	}

	comm := options.Cmd

	recordFile := options.File + "-" + fmt.Sprintf("%d", time.Now().Unix())

	ctx, cancel := context.WithCancel(ctx)

	errlogfile, err := options.getErrlogFile()
	if err != nil {
		cancel()
		return nil, err
	}

	// TODO: support level from flags
	errlog := slog.New(slog.NewTextHandler(errlogfile, &slog.HandlerOptions{
		AddSource: true,
	}))

	tt := &Terminal{
		Options: options,
		file: recordFile,
		errlog: errlog,
	}
	tt.cleanup(func() error { cancel(); return nil })
	tt.cleanup(errlogfile.Close)

	logf, err := openLogfile(recordFile, options.Append)
	if err != nil {
		return nil, err
	}
	tt.cleanup(logf.Close)

	tt.cmd = exec.CommandContext(ctx, comm[0], comm[1:]...)
	tt.cmd.Env = append(os.Environ(), "SCRIPT=" + recordFile)

	ptmx, err := pty.Start(tt.cmd)
	if err != nil {
		return nil, errors.Join(err, tt.Close())
	}
	tt.cleanup(func() error {
		if err := ptmx.Close(); err != nil {
			return fmt.Errorf("closing pty: %w", err)
		}
		return nil
	})
	tt.cleanup(func() error {
		if tt.cmd.ProcessState != nil && !tt.cmd.ProcessState.Exited() {
			return fmt.Errorf("canceling command: %w", tt.cmd.Cancel())
		}
		return nil
	})

	var suffix string
	if options.remoteEnabled() {
		suffix = fmt.Sprintf(", remote socket is %s", options.getSocketFile(recordFile))
	}

	log.Printf("started, output file is %s%s", recordFile, suffix)

	// TODO: if we're not using a TTY, we need to setup the command's
	// Stdin/Stdout differently.
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGWINCH)
	go func() {
		for range ch {
			select {
			case <-ctx.Done():
				return
			default:
			}
			if err := pty.InheritSize(os.Stdin, ptmx); err != nil {
				tt.errlog.ErrorContext(ctx, "error resizing pty", "error", err)
			}
		}
	}()
	ch <- syscall.SIGWINCH // Initial resize.
	tt.cleanup(func() error { signal.Stop(ch); close(ch); return nil })

	// Set stdin in raw mode.
	oldState, err := term.MakeRaw(int(os.Stdin.Fd()))
	if err != nil {
		return nil, errors.Join(err, tt.Close())
	}
	tt.cleanup(func() error {
		return term.Restore(int(os.Stdin.Fd()), oldState)
	})

	stdinReader := logf.reader(os.Stdin)
	stdoutWriter := logf.writer(os.Stdout)
	ptyWriter := newLockWriter(ptmx)

	if options.remoteEnabled() {
		socketFile := options.getSocketFile(recordFile)
		err = os.RemoveAll(socketFile)
		if err != nil {
			return nil, errors.Join(err, tt.Close())
		}

		listener, err := newListener(socketFile)
		if err != nil {
			return nil, errors.Join(err, tt.Close())
		}
		tt.cleanup(listener.Close)

		stdoutWriter = io.MultiWriter(stdoutWriter, listener)

		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				default:
				}
				conn, err := listener.Accept()
				if err != nil {
					tt.errlog.ErrorContext(ctx, "error accepting remote connection", "error", err)
					continue
				}
				connReader := logf.reader(conn)
				go tt.handleRemote(ctx, ptyWriter, connReader, conn)
			}
		}()
	}

	// Copy stdin to the pty and the pty to stdout.
	// NOTE: The goroutine will keep reading until the next keystroke before returning.
	go func() {
		err := copyContext(ctx, ptyWriter, stdinReader)
		if err != nil {
			tt.errlog.ErrorContext(ctx, "error copying input from stdin to pty", "error", err)
			return
		}
	}()
	go func() {
		err := copyContext(ctx, stdoutWriter, ptmx)
		if err != nil {
			tt.errlog.ErrorContext(ctx, "error copying output to from pty to stdout", "error", err)
			return
		}
	}()

	return tt, nil
}


func (tt *Terminal) handleRemote(ctx context.Context, w io.Writer, r io.Reader, conn net.Conn) {
	for {
		err := copyContext(ctx, w, r)
		if err != nil {
			if errors.Is(err, net.ErrClosed) || errors.Is(err, io.EOF) {
				_ = conn.Close()
			}
			tt.errlog.ErrorContext(ctx, "error copying input from remote connection", "error", err)
			break
		}
	}
}

type lockWriter struct {
	mu sync.Mutex
	w io.Writer
}

func newLockWriter(w io.Writer) io.Writer {
	return &lockWriter{w:w}
}

func (l *lockWriter) Write(buf []byte) (n int, err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.Write(buf)
}

func copyContext(ctx context.Context, dst io.Writer, src io.Reader) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		buf := make([]byte, 1024*1024)
		n, err := src.Read(buf)
		if err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		_, err = dst.Write(buf[:n])
		if err != nil {
			return err
		}
	}
}

func Run(ctx context.Context, options Options) (_ *Output, err error) {
	term, err := Start(ctx, options)
	if err != nil {
		return nil, err
	}
	return term.Wait(ctx)
}

type listener struct {
	net.Listener
	path string

	mu sync.Mutex
	conns map[net.Conn]struct{}
}

// Write broadcasts writes to all of the open client connections.
// It's best-effort: if a connection is closed we ignore the error
// and delete the connection.
func (l *listener) Write(buf []byte) (n int, err error) {
	var conns []net.Conn
	l.mu.Lock()
	for conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()
	for _, conn := range conns {
		conn.SetWriteDeadline(time.Now().Add(300 * time.Millisecond))
		_, werr := conn.Write(buf)
		if werr != nil && errors.Is(werr, net.ErrClosed) {
			l.mu.Lock()
			delete(l.conns, conn)
			l.mu.Unlock()
		}
	}
	return len(buf), nil
}

type conn struct {
	net.Conn
	onClose func()
}

func (c *conn) Close() error {
	c.onClose()
	return c.Conn.Close()
}

// Accept implements [net.Listener].
func (l *listener) Accept() (net.Conn, error) {
	rawconn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}

	var c *conn
	c = &conn{
		Conn:rawconn,
		onClose: func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			delete(l.conns, c)
		},
	}
	l.mu.Lock()
	l.conns[c] = struct{}{}
	l.mu.Unlock()

	return c, nil
}

var _ net.Listener = (*listener)(nil)

func newListener(path string) (*listener, error) {
	lis, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	return &listener{
		Listener: lis,
		path: path,
		conns: make(map[net.Conn]struct{}),
	}, nil
}

func (l *listener) Close() error {
	defer func() { _ = os.RemoveAll(l.path) }()

	var conns []net.Conn
	l.mu.Lock()
	for conn := range l.conns {
		conns = append(conns, conn)
	}
	l.mu.Unlock()
	for _, conn := range conns {
		_ = conn.Close()
		delete(l.conns, conn)
	}
	return l.Listener.Close()
}

func writeStamp(w io.Writer, stamp RecordHeader) error {
	now := time.Now()
	stamp.Sec = uint64(now.Unix())
	stamp.Usec = uint32(now.Nanosecond() / 1000)
	if err := binary.Write(w, binary.LittleEndian, stamp); err != nil {
		return err
	}
	return nil
}

type logfile struct {
	f *os.File
	mu *sync.Mutex
}

func openLogfile(path string, appendMode bool) (*logfile, error) {
	flags := os.O_CREATE | os.O_WRONLY
	if appendMode {
		flags |= os.O_APPEND
	} else {
		flags |= os.O_TRUNC
	}

	f, err := os.OpenFile(path, flags, 0600)
	if err != nil {
		return nil, err
	}
	err = writeStamp(f, RecordHeader{
		Len:       0,
		Direction: uint32(DirStart),
	})
	return &logfile{f: f, mu: &sync.Mutex{}}, err
}

func (l *logfile) Close() error {
	l.mu.Lock()
	err := writeStamp(l.f, RecordHeader{
		Len:       0,
		Direction: uint32(DirEnd),
	})
	l.mu.Unlock()
	if err != nil {
		return err
	}
	return l.f.Close()
}

func (l *logfile) writer(w io.Writer) io.Writer {
	return stampWriter{
		mu: l.mu,
		dir: DirOutput,
		log: l.f,
		w:   io.MultiWriter(l.f, w),
	}
}

func (l *logfile) reader(r io.Reader) io.Reader {
	return io.TeeReader(r, stampWriter{
		mu: l.mu,
		dir: DirInput,
		log: l.f,
		w:   l.f,
	})
}

type stampWriter struct {
	mu *sync.Mutex
	dir byte
	log io.Writer
	w   io.Writer
}

func (s stampWriter) Write(buf []byte) (n int, err error) {
	s.mu.Lock()
	err = writeStamp(s.log, RecordHeader{
		Len:       uint64(len(buf)),
		Direction: uint32(s.dir),
	})
	s.mu.Unlock()
	if err != nil {
		return 0, err
	}
	return s.w.Write(buf)
}

type Options struct {
	PlayFile string
	DisablePlayDelay bool
	Append bool
	File string
	Cmd []string
	Remote bool
	UseChildExit bool
	SocketFile string
	ErrorLogFile string
}

func (o Options) Default() Options {
	shell := os.Getenv("SHELL")
	if shell == "" {
		shell = "/bin/sh"
	}
	return Options{
		Append: false,
		File: "typescript",
		Cmd: []string{shell},
		Remote: false,
		SocketFile: "",
	}
}

func (o Options) getSocketFile(recordFile string) string {
	if o.SocketFile != "" {
		return o.SocketFile
	}
	return recordFile + ".socket"
}

func (o Options) remoteEnabled() bool {
	return o.Remote || o.SocketFile != ""
}

func (o Options) getErrlogFile() (io.WriteCloser, error) {
	if o.ErrorLogFile != "" {
		return os.OpenFile(o.ErrorLogFile, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	}
	return nopCloser{io.Discard}, nil
}

type nopCloser struct {
	io.Writer
}

func (nopCloser) Close() error { return nil }

type Output struct {
	Options Options
	File string
	ExitCode int
}
