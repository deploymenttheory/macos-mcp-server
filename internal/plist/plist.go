// Package plist writes Apple XML property lists. It is deliberately small: the
// server emits launchd job definitions and preference values, which need
// dictionaries, arrays, strings, integers, floats, booleans and dates and
// nothing else. Reading property lists is done through CoreFoundation, which
// already knows the format; this package exists because the SDK has no writer
// that takes Go values.
package plist

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"
)

const (
	header = `<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">` + "\n" +
		`<plist version="1.0">` + "\n"
	footer = "</plist>\n"
)

// ErrUnsupportedType reports a Go value with no property-list representation.
var ErrUnsupportedType = errors.New("plist: unsupported value type")

// Marshal renders v as a complete XML property list document.
func Marshal(v any) ([]byte, error) {
	body, err := MarshalValue(v)
	if err != nil {
		return nil, err
	}
	var b bytes.Buffer
	b.WriteString(header)
	b.Write(body)
	b.WriteString(footer)
	return b.Bytes(), nil
}

// MarshalValue renders v as an XML fragment (no document wrapper), which is
// what `defaults write` accepts for a structured value.
func MarshalValue(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeValue(&b, v, 0); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeValue(b *bytes.Buffer, v any, depth int) error {
	indent := func() {
		for i := 0; i < depth; i++ {
			b.WriteByte('\t')
		}
	}
	switch x := v.(type) {
	case nil:
		return fmt.Errorf("%w: nil", ErrUnsupportedType)
	case string:
		indent()
		b.WriteString("<string>")
		escape(b, x)
		b.WriteString("</string>\n")
	case bool:
		indent()
		if x {
			b.WriteString("<true/>\n")
		} else {
			b.WriteString("<false/>\n")
		}
	case int:
		indent()
		fmt.Fprintf(b, "<integer>%d</integer>\n", x)
	case int64:
		indent()
		fmt.Fprintf(b, "<integer>%d</integer>\n", x)
	case uint32:
		indent()
		fmt.Fprintf(b, "<integer>%d</integer>\n", x)
	case float64:
		indent()
		// JSON numbers arrive as float64; a whole number is almost always an
		// integer in intent (a port, an interval), and launchd rejects a <real>
		// where it expects an <integer>.
		if x == float64(int64(x)) {
			fmt.Fprintf(b, "<integer>%d</integer>\n", int64(x))
		} else {
			fmt.Fprintf(b, "<real>%s</real>\n", strconv.FormatFloat(x, 'f', -1, 64))
		}
	case time.Time:
		indent()
		fmt.Fprintf(b, "<date>%s</date>\n", x.UTC().Format("2006-01-02T15:04:05Z"))
	case []byte:
		indent()
		fmt.Fprintf(b, "<data>%s</data>\n", base64.StdEncoding.EncodeToString(x))
	case []any:
		indent()
		b.WriteString("<array>\n")
		for _, e := range x {
			if err := writeValue(b, e, depth+1); err != nil {
				return err
			}
		}
		indent()
		b.WriteString("</array>\n")
	case []string:
		indent()
		b.WriteString("<array>\n")
		for _, e := range x {
			if err := writeValue(b, e, depth+1); err != nil {
				return err
			}
		}
		indent()
		b.WriteString("</array>\n")
	case map[string]any:
		indent()
		b.WriteString("<dict>\n")
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			for i := 0; i < depth+1; i++ {
				b.WriteByte('\t')
			}
			b.WriteString("<key>")
			escape(b, k)
			b.WriteString("</key>\n")
			if err := writeValue(b, x[k], depth+1); err != nil {
				return err
			}
		}
		indent()
		b.WriteString("</dict>\n")
	default:
		return fmt.Errorf("%w: %T", ErrUnsupportedType, v)
	}
	return nil
}

func escape(b *bytes.Buffer, s string) {
	for _, r := range s {
		switch r {
		case '&':
			b.WriteString("&amp;")
		case '<':
			b.WriteString("&lt;")
		case '>':
			b.WriteString("&gt;")
		default:
			b.WriteRune(r)
		}
	}
}
