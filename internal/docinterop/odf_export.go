package docinterop

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"

	"example.com/parchment/internal/document"
	"github.com/yuin/goldmark/ast"
)

// ODFExporter implements Exporter for OpenDocument Text (.odt).
type ODFExporter struct{}

// Export writes the document as an .odt archive to w.
func (e ODFExporter) Export(w io.Writer, doc document.Document) ([]error, error) {
	var warnings []error

	zw := zip.NewWriter(w)
	defer zw.Close()

	// 1. mimetype (must be first and uncompressed per ODF spec)
	if err := writeMimetype(zw); err != nil {
		return warnings, err
	}

	// 2. META-INF/manifest.xml
	if err := writeManifest(zw); err != nil {
		return warnings, err
	}

	// 3. styles.xml
	if err := writeStyles(zw, doc); err != nil {
		return warnings, err
	}

	// 4. content.xml
	if err := writeContent(zw, doc); err != nil {
		return warnings, err
	}

	return warnings, nil
}

func writeContent(zw *zip.Writer, doc document.Document) error {
	w, err := zw.Create("content.xml")
	if err != nil {
		return err
	}

	node := ParseMarkdown(doc.Body)

	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<office:document-content xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" office:version="1.2">
 <office:body>
  <office:text>
`)

	bodyBytes := []byte(doc.Body)

	ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		switch v := n.(type) {
		case *ast.Paragraph:
			if entering {
				buf.WriteString(`   <text:p>`)
			} else {
				buf.WriteString(`</text:p>
`)
			}
		case *ast.Heading:
			if entering {
				buf.WriteString(`   <text:h text:outline-level="`)
				buf.WriteRune(rune(v.Level + '0'))
				buf.WriteString(`">`)
			} else {
				buf.WriteString(`</text:h>
`)
			}
		case *ast.List:
			if entering {
				buf.WriteString(`   <text:list>
`)
			} else {
				buf.WriteString(`   </text:list>
`)
			}
		case *ast.ListItem:
			if entering {
				buf.WriteString(`    <text:list-item>
`)
			} else {
				buf.WriteString(`    </text:list-item>
`)
			}
		case *ast.Text:
			if entering {
				xml.EscapeText(&buf, v.Segment.Value(bodyBytes))
			}
		}
		return ast.WalkContinue, nil
	})

	buf.WriteString(`  </office:text>
 </office:body>
</office:document-content>`)

	_, err = io.WriteString(w, buf.String())
	return err
}

func writeManifest(zw *zip.Writer) error {
	w, err := zw.Create("META-INF/manifest.xml")
	if err != nil {
		return err
	}

	const manifestXML = `<?xml version="1.0" encoding="UTF-8"?>
<manifest:manifest xmlns:manifest="urn:oasis:names:tc:opendocument:xmlns:manifest:1.0" manifest:version="1.2">
 <manifest:file-entry manifest:full-path="/" manifest:media-type="application/vnd.oasis.opendocument.text"/>
 <manifest:file-entry manifest:full-path="content.xml" manifest:media-type="text/xml"/>
 <manifest:file-entry manifest:full-path="styles.xml" manifest:media-type="text/xml"/>
</manifest:manifest>`

	_, err = io.WriteString(w, manifestXML)
	return err
}

func writeStyles(zw *zip.Writer, doc document.Document) error {
	w, err := zw.Create("styles.xml")
	if err != nil {
		return err
	}

	const stylesXML = `<?xml version="1.0" encoding="UTF-8"?>
<office:document-styles xmlns:office="urn:oasis:names:tc:opendocument:xmlns:office:1.0" xmlns:style="urn:oasis:names:tc:opendocument:xmlns:style:1.0" xmlns:fo="urn:oasis:names:tc:opendocument:xmlns:xsl-fo-compatible:1.0" xmlns:text="urn:oasis:names:tc:opendocument:xmlns:text:1.0" office:version="1.2">
 <office:styles>
  <style:default-style style:family="paragraph">
   <style:paragraph-properties fo:margin-top="0in" fo:margin-bottom="0in"/>
  </style:default-style>
 </office:styles>
</office:document-styles>`

	_, err = io.WriteString(w, stylesXML)
	return err
}

func writeMimetype(zw *zip.Writer) error {
	header := &zip.FileHeader{
		Name:   "mimetype",
		Method: zip.Store, // Uncompressed
	}
	writer, err := zw.CreateHeader(header)
	if err != nil {
		return err
	}
	_, err = writer.Write([]byte("application/vnd.oasis.opendocument.text"))
	return err
}
