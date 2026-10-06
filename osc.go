package xscript

import (
	"bytes"
	"strings"
)

// oscSplitter pulls shell-integration OSC sequences (133 and 7) out of the
// output stream and passes everything else through untouched. It is
// stateful because a sequence can be split across script(1) records.
type oscSplitter struct {
	pending []byte
}

// feed calls onText for ordinary output and onOSC with the payload of each
// intercepted sequence (e.g. "133;D;0"), preserving their relative order.
func (s *oscSplitter) feed(chunk []byte, onText func([]byte), onOSC func(string)) {
	buf := append(s.pending, chunk...)
	s.pending = nil

	for len(buf) > 0 {
		start := bytes.Index(buf, []byte("\x1b]"))
		if start < 0 {
			// A trailing ESC may be the first half of an OSC introducer.
			if buf[len(buf)-1] == '\x1b' {
				onText(buf[:len(buf)-1])
				s.pending = []byte{'\x1b'}
				return
			}
			onText(buf)
			return
		}

		payloadStart := start + 2
		payloadLen, termLen := findOSCTerminator(buf[payloadStart:])
		if payloadLen < 0 {
			onText(buf[:start])
			s.pending = append([]byte(nil), buf[start:]...)
			return
		}

		payload := string(buf[payloadStart : payloadStart+payloadLen])
		end := payloadStart + payloadLen + termLen
		if strings.HasPrefix(payload, "133;") || strings.HasPrefix(payload, "7;") {
			onText(buf[:start])
			onOSC(payload)
		} else {
			// Not ours (window title, colours, ...): let the emulator handle it.
			onText(buf[:end])
		}
		buf = buf[end:]
	}
}

// findOSCTerminator returns the payload length and terminator length for an
// OSC ended by BEL or ST (ESC \), or -1 if the terminator hasn't arrived yet.
func findOSCTerminator(b []byte) (payloadLen, termLen int) {
	for i := range b {
		switch {
		case b[i] == '\a':
			return i, 1
		case b[i] == '\x1b' && i+1 < len(b) && b[i+1] == '\\':
			return i, 2
		}
	}
	return -1, 0
}
