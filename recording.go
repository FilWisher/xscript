// Package xscript rebuilds shell commands and their output from a
// `script -r` recording by replaying the terminal output through a headless
// VT emulator and splitting it on OSC 133 shell-integration markers.
package xscript

import (
	"encoding/binary"
	"errors"
	"io"
	"time"
)

// Record directions written by BSD/macOS script(1).
const (
	DirStart  byte = 's'
	DirInput  byte = 'i'
	DirOutput byte = 'o'
	DirEnd    byte = 'e'
)

// Record is one chunk of a `script -r` recording.
type Record struct {
	Time      time.Time
	Direction byte
	Data      []byte
}

// Records is a collection of [Record] read from a `script -r` recording.
type Records []Record

// Finished determines whether the recording was finished or not.
func (rr Records) Finished() bool {
	for _, r := range rr {
		if r.Direction == DirEnd {
			return true
		}
	}
	return false
}

// RecordHeader mirrors `struct stamp` in script.c: the direction field is
// also how script(1) detects byte order, which is little-endian on macOS.
type RecordHeader struct {
	Len       uint64
	Sec       uint64
	Usec      uint32
	Direction uint32
}

// ReadRecords parses every record in a `script -r` file. A truncated final
// record (the recording is still being written) is dropped, not an error.
func ReadRecords(r io.Reader) (Records, error) {
	var records []Record
	for {
		var h RecordHeader
		if err := binary.Read(r, binary.LittleEndian, &h); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return records, nil
			}
			return nil, err
		}
		data := make([]byte, h.Len)
		if _, err := io.ReadFull(r, data); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				return records, nil
			}
			return nil, err
		}
		records = append(records, Record{
			Time:      time.Unix(int64(h.Sec), int64(h.Usec)*int64(time.Microsecond)),
			Direction: byte(h.Direction),
			Data:      data,
		})
	}
}
