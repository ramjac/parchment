// Package notetext handles lossless external note text without filesystem I/O.
package notetext

import (
	"bytes"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding"
	"golang.org/x/text/encoding/charmap"
	"golang.org/x/text/encoding/japanese"
)

const (
	Auto        = "auto"
	UTF8        = "utf-8"
	UTF16LE     = "utf-16le"
	UTF16BE     = "utf-16be"
	Windows1252 = "windows-1252"
	Latin1      = "iso-8859-1"
	ShiftJIS    = "shift-jis"
	MaxText     = 1_000_000
	MaxLines    = 10_000
)

type Ending string

const (
	LF   Ending = "\n"
	CRLF Ending = "\r\n"
	CR   Ending = "\r"
)

type Format struct {
	Encoding string `json:"encoding"`
	BOM      bool   `json:"bom,omitempty"`
}

// Document separates logical LF text from the original line boundaries.
type Document struct {
	Text    string   `json:"text"`
	Endings []Ending `json:"endings,omitempty"`
}

func ParseEncoding(name string) (string, error) {
	switch strings.ToLower(name) {
	case "", Auto:
		return Auto, nil
	case UTF8, UTF16LE, UTF16BE, Windows1252, Latin1, ShiftJIS:
		return strings.ToLower(name), nil
	default:
		return "", fmt.Errorf("unknown encoding %q (use auto, utf-8, utf-16le, utf-16be, windows-1252, iso-8859-1, or shift-jis)", name)
	}
}

func (d Document) Clone() Document {
	return Document{Text: d.Text, Endings: slices.Clone(d.Endings)}
}

func (d Document) Equal(other Document) bool {
	return d.Text == other.Text && slices.Equal(d.Endings, other.Endings)
}

func Split(source string) Document {
	var text strings.Builder
	var endings []Ending
	for i := 0; i < len(source); i++ {
		switch source[i] {
		case '\r':
			ending := CR
			if i+1 < len(source) && source[i+1] == '\n' {
				ending = CRLF
				i++
			}
			endings = append(endings, ending)
			text.WriteByte('\n')
		case '\n':
			endings = append(endings, LF)
			text.WriteByte('\n')
		default:
			text.WriteByte(source[i])
		}
	}
	return Document{Text: text.String(), Endings: endings}
}

func ValidateText(text string) error {
	if !utf8.ValidString(text) {
		return errors.New("note text is not valid UTF-8")
	}
	if len(text) > MaxText || strings.Count(text, "\n")+1 > MaxLines {
		return errors.New("note exceeds editor limits (1,000,000 UTF-8 bytes or 10,000 lines)")
	}
	for _, r := range text {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return fmt.Errorf("note contains unsupported control character U+%04X", r)
		}
	}
	return nil
}

func (d Document) Source() (string, error) {
	if err := ValidateText(d.Text); err != nil {
		return "", err
	}
	if strings.Count(d.Text, "\n") != len(d.Endings) {
		return "", errors.New("invalid note line-ending metadata")
	}
	var source strings.Builder
	start := 0
	for _, ending := range d.Endings {
		if ending != LF && ending != CRLF && ending != CR {
			return "", errors.New("invalid note line ending")
		}
		end := start + strings.IndexByte(d.Text[start:], '\n')
		source.WriteString(d.Text[start:end])
		source.WriteString(string(ending))
		start = end + 1
	}
	source.WriteString(d.Text[start:])
	return source.String(), nil
}

func legacy(name string) encoding.Encoding {
	switch name {
	case Windows1252:
		return charmap.Windows1252
	case Latin1:
		return charmap.ISO8859_1
	case ShiftJIS:
		return japanese.ShiftJIS
	}
	return nil
}

func bom(data []byte) (string, int, error) {
	switch {
	case bytes.HasPrefix(data, []byte{0xff, 0xfe, 0, 0}), bytes.HasPrefix(data, []byte{0, 0, 0xfe, 0xff}):
		return "", 0, errors.New("UTF-32 notes are not supported")
	case bytes.HasPrefix(data, []byte{0xef, 0xbb, 0xbf}):
		return UTF8, 3, nil
	case bytes.HasPrefix(data, []byte{0xff, 0xfe}):
		return UTF16LE, 2, nil
	case bytes.HasPrefix(data, []byte{0xfe, 0xff}):
		return UTF16BE, 2, nil
	}
	return "", 0, nil
}

func Decode(data []byte, requested string) (Document, Format, error) {
	if len(data) > MaxText*4 {
		return Document{}, Format{}, errors.New("note exceeds editor limits")
	}
	name, err := ParseEncoding(requested)
	if err != nil {
		return Document{}, Format{}, err
	}
	marked, offset, err := bom(data)
	if err != nil {
		return Document{}, Format{}, err
	}
	if marked != "" {
		if name != Auto && name != marked {
			return Document{}, Format{}, fmt.Errorf("--encoding %s conflicts with the %s BOM", name, marked)
		}
		name = marked
	}
	if name == Auto && bytes.IndexByte(data, 0) >= 0 {
		return Document{}, Format{}, errors.New("note contains a NUL byte; if it is BOM-less UTF-16, reopen with --encoding utf-16le or --encoding utf-16be")
	}
	if name == Auto && utf8.Valid(data) {
		name = UTF8
	}
	if name != Auto {
		doc, err := decode(data[offset:], name)
		return doc, Format{Encoding: name, BOM: offset != 0}, err
	}
	var candidates []string
	var selected Document
	for _, candidate := range []string{Windows1252, Latin1, ShiftJIS} {
		if doc, err := decode(data, candidate); err == nil {
			candidates = append(candidates, candidate)
			selected = doc
		}
	}
	if len(candidates) == 1 {
		return selected, Format{Encoding: candidates[0]}, nil
	}
	if len(candidates) > 1 {
		return Document{}, Format{}, fmt.Errorf("ambiguous note encoding (%s); reopen with --encoding <encoding>", strings.Join(candidates, ", "))
	}
	return Document{}, Format{}, errors.New("unsupported or malformed note text; BOM-less UTF-16 requires --encoding utf-16le or --encoding utf-16be")
}

func decode(data []byte, name string) (Document, error) {
	var source string
	switch name {
	case UTF8:
		if !utf8.Valid(data) {
			return Document{}, errors.New("malformed UTF-8 note")
		}
		source = string(data)
	case UTF16LE, UTF16BE:
		if len(data)%2 != 0 {
			return Document{}, errors.New("malformed UTF-16 note: odd byte length")
		}
		var text strings.Builder
		for i := 0; i < len(data); i += 2 {
			unit := readUnit(data[i:], name)
			switch {
			case unit >= 0xd800 && unit <= 0xdbff:
				if i+3 >= len(data) {
					return Document{}, errors.New("malformed UTF-16 note: unpaired surrogate")
				}
				next := readUnit(data[i+2:], name)
				if next < 0xdc00 || next > 0xdfff {
					return Document{}, errors.New("malformed UTF-16 note: unpaired surrogate")
				}
				text.WriteRune(utf16.DecodeRune(rune(unit), rune(next)))
				i += 2
			case unit >= 0xdc00 && unit <= 0xdfff:
				return Document{}, errors.New("malformed UTF-16 note: unpaired surrogate")
			default:
				text.WriteRune(rune(unit))
			}
			if text.Len() > MaxText*2 {
				return Document{}, errors.New("decoded note exceeds editor limits")
			}
		}
		source = text.String()
	default:
		codec := legacy(name)
		if codec == nil {
			return Document{}, fmt.Errorf("unsupported note encoding %q", name)
		}
		decoded, err := codec.NewDecoder().Bytes(data)
		if err != nil {
			return Document{}, fmt.Errorf("decode %s: %w", name, err)
		}
		roundTrip, err := codec.NewEncoder().Bytes(decoded)
		if err != nil || !bytes.Equal(roundTrip, data) {
			return Document{}, fmt.Errorf("malformed or non-round-trippable %s note", name)
		}
		source = string(decoded)
	}
	doc := Split(source)
	if err := ValidateText(doc.Text); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func readUnit(data []byte, name string) uint16 {
	if name == UTF16LE {
		return uint16(data[0]) | uint16(data[1])<<8
	}
	return uint16(data[0])<<8 | uint16(data[1])
}

func Encode(doc Document, format Format) ([]byte, error) {
	source, err := doc.Source()
	if err != nil {
		return nil, err
	}
	var data []byte
	switch format.Encoding {
	case UTF8:
		if format.BOM {
			data = append(data, 0xef, 0xbb, 0xbf)
		}
		data = append(data, source...)
	case UTF16LE, UTF16BE:
		if format.BOM {
			if format.Encoding == UTF16LE {
				data = append(data, 0xff, 0xfe)
			} else {
				data = append(data, 0xfe, 0xff)
			}
		}
		for _, unit := range utf16.Encode([]rune(source)) {
			if format.Encoding == UTF16LE {
				data = append(data, byte(unit), byte(unit>>8))
			} else {
				data = append(data, byte(unit>>8), byte(unit))
			}
		}
	default:
		codec := legacy(format.Encoding)
		if codec == nil || format.BOM {
			return nil, errors.New("invalid note encoding or BOM")
		}
		data, err = codec.NewEncoder().Bytes([]byte(source))
		if err != nil {
			return nil, fmt.Errorf("text cannot be saved as %s without losing characters: %w", format.Encoding, err)
		}
	}
	roundTrip, actual, err := Decode(data, format.Encoding)
	if err != nil || actual != format || !doc.Equal(roundTrip) {
		return nil, fmt.Errorf("text cannot be saved as %s without changing its encoding, BOM, or characters", format.Encoding)
	}
	return data, nil
}
