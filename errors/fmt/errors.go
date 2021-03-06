// Copyright 2018 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package fmt

import (
	"bytes"
	"strings"

	"golang.org/x/exp/errors"
)

// fmtError formats err according to verb, writing to p.
// If it cannot handle the error, it does no formatting
// and returns false.
func errorf(format string, a []interface{}) error {
	err := lastError(format, a)
	if err == nil {
		return &simpleErr{Sprintf(format, a...), errors.Caller(2)}
	}

	// TODO: this is not entirely correct. The error value could be
	// printed elsewhere in format if it mixes numbered with unnumbered
	// substitutions. With relatively small changes to doPrintf we can
	// have it optionally ignore extra arguments and pass the argument
	// list in its entirety.
	format = format[:len(format)-len(": %s")]
	return &withChain{
		msg:   Sprintf(format, a[:len(a)-1]...),
		err:   err,
		frame: errors.Caller(2),
	}
}

func lastError(format string, a []interface{}) error {
	if !strings.HasSuffix(format, ": %s") && !strings.HasSuffix(format, ": %v") {
		return nil
	}

	if len(a) == 0 {
		return nil
	}

	err, ok := a[len(a)-1].(error)
	if !ok {
		return nil
	}

	return err
}

type simpleErr struct {
	msg   string
	frame errors.Frame
}

func (e *simpleErr) Error() string {
	return Sprint(e)
}

func (e *simpleErr) Format(p errors.Printer) (next error) {
	p.Print(e.msg)
	e.frame.Format(p)
	return nil
}

type withChain struct {
	// TODO: add frame information
	msg   string
	err   error
	frame errors.Frame
}

func (e *withChain) Error() string {
	return Sprint(e)
}

func (e *withChain) Format(p errors.Printer) (next error) {
	p.Print(e.msg)
	e.frame.Format(p)
	return e.err
}

func (e *withChain) Unwrap() error {
	return e.err
}

func fmtError(p *pp, verb rune, err error) (handled bool) {
	var (
		sep = " " // separator before next error
		w   = p   // print buffer where error text is written
	)
	switch {
	// Note that this switch must match the preference order
	// for ordinary string printing (%#v before %+v, and so on).

	case p.fmt.sharpV:
		if stringer, ok := p.arg.(GoStringer); ok {
			// Print the result of GoString unadorned.
			p.fmt.fmtS(stringer.GoString())
			return true
		}
		return false

	case p.fmt.plusV:
		sep = "\n--- "
		w.fmt.fmtFlags = fmtFlags{plusV: p.fmt.plusV} // only keep detail flag

		// The width or precision of a detailed view could be the number of
		// errors to print from a list.

	default:
		// Use an intermediate buffer in the rare cases that precision,
		// truncation, or one of the alternative verbs (q, x, and X) are
		// specified.
		switch verb {
		case 's', 'v':
			if (!w.fmt.widPresent || w.fmt.wid == 0) && !w.fmt.precPresent {
				break
			}
			fallthrough
		case 'q', 'x', 'X':
			w = newPrinter()
			defer w.free()
		default:
			w.badVerb(verb)
			return true
		}
	}

loop:
	for {
		w.fmt.inDetail = false
		switch v := err.(type) {
		case errors.Formatter:
			err = v.Format((*errPP)(w))
		// TODO: This case is for supporting old error implementations.
		// It may eventually disappear.
		case interface{ FormatError(errors.Printer) error }:
			err = v.FormatError((*errPP)(w))
		case Formatter:
			// Discard verb, but keep the flags. Discarding the verb prevents
			// nested quoting and other unwanted behavior. Preserving flags
			// recursively signals a request for detail, if interpreted as %+v.
			w.fmt.fmtFlags = p.fmt.fmtFlags
			if w.fmt.plusV {
				v.Format((*errPPState)(w), 'v') // indent new lines
			} else {
				v.Format(w, 'v') // do not indent new lines
			}
			break loop
		default:
			w.fmtString(v.Error(), 's')
			break loop
		}
		if err == nil {
			break
		}
		if !w.fmt.inDetail || !p.fmt.plusV {
			w.buf.WriteByte(':')
		}
		// Strip last newline of detail.
		if bytes.HasSuffix([]byte(w.buf), detailSep) {
			w.buf = w.buf[:len(w.buf)-len(detailSep)]
		}
		w.buf.WriteString(sep)
		w.fmt.inDetail = false
	}

	if w != p {
		p.fmtString(string(w.buf), verb)
	}
	return true
}

var detailSep = []byte("\n    ")

// errPPState wraps a pp to implement State with indentation. It is used
// for errors implementing fmt.Formatter.
type errPPState pp

func (p *errPPState) Width() (wid int, ok bool)      { return (*pp)(p).Width() }
func (p *errPPState) Precision() (prec int, ok bool) { return (*pp)(p).Precision() }
func (p *errPPState) Flag(c int) bool                { return (*pp)(p).Flag(c) }

func (p *errPPState) Write(b []byte) (n int, err error) {
	if !p.fmt.inDetail || p.fmt.plusV {
		k := 0
		if p.fmt.indent {
			for i, c := range b {
				if c == '\n' {
					p.buf.Write(b[k:i])
					p.buf.Write(detailSep)
					k = i + 1
				}
			}
		}
		p.buf.Write(b[k:])
	}
	return len(b), nil
}

// errPP wraps a pp to implement an errors.Printer.
type errPP pp

func (p *errPP) Print(args ...interface{}) {
	if !p.fmt.inDetail || p.fmt.plusV {
		if p.fmt.indent {
			Fprint((*errPPState)(p), args...)
		} else {
			(*pp)(p).doPrint(args)
		}
	}
}

func (p *errPP) Printf(format string, args ...interface{}) {
	if !p.fmt.inDetail || p.fmt.plusV {
		if p.fmt.indent {
			Fprintf((*errPPState)(p), format, args...)
		} else {
			(*pp)(p).doPrintf(format, args)
		}
	}
}

func (p *errPP) Detail() bool {
	inDetail := p.fmt.inDetail
	p.fmt.inDetail = true
	p.fmt.indent = p.fmt.plusV
	if p.fmt.plusV && !inDetail {
		(*errPPState)(p).Write([]byte(":\n"))
	}
	return p.fmt.plusV
}
