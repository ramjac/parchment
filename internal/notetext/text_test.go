package notetext

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncodingRoundTrips(t *testing.T) {
	for _, format := range []Format{
		{UTF8, false}, {UTF8, true}, {UTF16LE, true}, {UTF16BE, true},
		{UTF16LE, false}, {UTF16BE, false}, {Windows1252, false},
		{Latin1, false}, {ShiftJIS, false},
	} {
		t.Run(format.Encoding+map[bool]string{true: "-BOM"}[format.BOM], func(t *testing.T) {
			source := "first\r\nsecond\rthird\nlast"
			if format.Encoding == ShiftJIS {
				source += "日本語"
			} else {
				source += "é"
			}
			doc := Split(source)
			data, err := Encode(doc, format)
			if err != nil {
				t.Fatal(err)
			}
			got, gotFormat, err := Decode(data, format.Encoding)
			if err != nil || !got.Equal(doc) || gotFormat != format {
				t.Fatalf("decode = %#v, %#v, %v", got, gotFormat, err)
			}
			reencoded, err := Encode(got, gotFormat)
			if err != nil || !bytes.Equal(reencoded, data) {
				t.Fatalf("round trip = %x, %v; want %x", reencoded, err, data)
			}
		})
	}
}

func TestAutomaticDetectionAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		data []byte
		flag string
		want string
		err  string
	}{
		{"UTF8", []byte("hello\rworld"), Auto, UTF8, ""},
		{"replacement character", []byte("real \ufffd"), Auto, UTF8, ""},
		{"internal BOM", []byte("before\ufeffafter"), Auto, UTF8, ""},
		{"unique legacy", []byte("price \x80"), Auto, Windows1252, ""},
		{"ambiguous", []byte("caf\xe9"), Auto, "", "ambiguous"},
		{"malformed UTF8", []byte("\xff"), UTF8, "", "malformed"},
		{"undefined Windows byte", []byte("\x81"), Windows1252, "", "round-trippable"},
		{"UTF16 odd", []byte{0xff, 0xfe, 0x41}, Auto, "", "odd byte"},
		{"UTF16 high surrogate", []byte{0xfe, 0xff, 0xd8, 0}, Auto, "", "surrogate"},
		{"UTF16 low surrogate", []byte{0xff, 0xfe, 0, 0xdc}, Auto, "", "surrogate"},
		{"UTF32", []byte{0xff, 0xfe, 0, 0}, Auto, "", "UTF-32"},
		{"BOM conflict", []byte{0xef, 0xbb, 0xbf, 'x'}, Latin1, "", "conflicts"},
		{"BOMless UTF16", []byte{'h', 0, 'i', 0}, Auto, "", "--encoding utf-16le"},
		{"explicit BOMless UTF16", []byte{'h', 0, 'i', 0}, UTF16LE, UTF16LE, ""},
		{"NUL byte", []byte{'a', 0, 'b'}, Auto, "", "contains a NUL byte"},
		{"escape", []byte("a\x1bb"), Auto, "", "control character"},
		{"unknown", nil, "unknown", "", "unknown encoding"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, format, err := Decode(tc.data, tc.flag)
			if tc.err != "" {
				if err == nil || !strings.Contains(err.Error(), tc.err) {
					t.Fatalf("error = %v; want %q", err, tc.err)
				}
			} else if err != nil || format.Encoding != tc.want {
				t.Fatalf("format = %#v, error = %v", format, err)
			}
		})
	}
}

func TestEncodeRejectsLossAndInvalidBoundaries(t *testing.T) {
	for _, tc := range []struct {
		doc    Document
		format Format
	}{
		{Split("emoji 😀"), Format{Windows1252, false}},
		{Split("¥"), Format{ShiftJIS, false}},
		{Document{Text: "a\nb"}, Format{UTF8, false}},
		{Document{Text: "a\nb", Endings: []Ending{"bad"}}, Format{UTF8, false}},
		{Split("unsafe\x00"), Format{UTF8, false}},
		{Split("\ufefftext"), Format{UTF8, false}},
		{Split("\ufefftext"), Format{UTF16LE, false}},
		{Split("ï»¿text"), Format{Windows1252, false}},
	} {
		if _, err := Encode(tc.doc, tc.format); err == nil {
			t.Fatalf("accepted %#v in %#v", tc.doc, tc.format)
		}
	}
}

func TestSplitPreservesEmptyAndFinalLines(t *testing.T) {
	for _, source := range []string{"", "\r\n", "a", "a\n\n", "\r\r\n\n", "a\r\nb\rc\n"} {
		doc := Split(source)
		got, err := doc.Source()
		if err != nil || got != source {
			t.Fatalf("round trip %q = %q, %v", source, got, err)
		}
	}
}
