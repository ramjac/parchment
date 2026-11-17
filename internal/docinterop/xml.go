package docinterop

import (
	"encoding/xml"
	"io"
)

// NewXMLDecoder returns an XML decoder configured to disallow entity expansion
// by enabling strict mode and ensuring CharsetReader handles it or we simply
// use the default which in Go blocks resolving external entities.
func NewXMLDecoder(r io.Reader) *xml.Decoder {
	dec := xml.NewDecoder(r)
	dec.Strict = true
	// By default in Go 1.24+, external entities are not resolved without a custom Entity mapper.
	// We leave Entity nil to ensure external entities are blocked.
	return dec
}
