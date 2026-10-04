package artifactfile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"example.com/parchment/internal/artifact"
)

const metadataBlock = "parchment-meta"
const bodyBoundary = "<!-- parchment-body -->"
const envelopeFormat = "parchment-single-file-v1"
const envelopeFormatField = "parchment_format"

const MaxFileSize = 64 << 20

// ErrMetadataMissing indicates the file has no embedded Parchment metadata.
var ErrMetadataMissing = errors.New("Parchment metadata block is missing")

// File is a Markdown artifact with shared metadata and optional structured
// payloads stored in hidden Parchment code fences.
type File struct {
	Artifact artifact.Artifact
	Body     string
	Blocks   map[string]json.RawMessage
}

type metadataEnvelope struct {
	Format string `json:"parchment_format,omitempty"`
	artifact.Artifact
}

// Encode serializes an artifact as Markdown. Payload blocks are emitted before
// the visible body so ordinary Markdown readers can ignore them.
func Encode(item artifact.Artifact, body string, blocks map[string]any) ([]byte, error) {
	if err := item.Validate(); err != nil {
		return nil, fmt.Errorf("validate artifact metadata: %w", err)
	}
	metadata, err := json.MarshalIndent(metadataEnvelope{Format: envelopeFormat, Artifact: item}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode artifact metadata: %w", err)
	}
	var output bytes.Buffer
	writeBlock(&output, metadataBlock, metadata)
	names := make([]string, 0, len(blocks))
	for name := range blocks {
		if !validBlockName(name) || name == metadataBlock {
			return nil, fmt.Errorf("invalid Parchment block name %q", name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		var payload []byte
		if raw, ok := blocks[name].(json.RawMessage); ok {
			if !json.Valid(raw) {
				return nil, fmt.Errorf("encode %s block: invalid JSON", name)
			}
			payload = append([]byte(nil), raw...)
		} else {
			var err error
			payload, err = json.MarshalIndent(blocks[name], "", "  ")
			if err != nil {
				return nil, fmt.Errorf("encode %s block: %w", name, err)
			}
		}
		writeBlock(&output, name, payload)
	}
	output.WriteByte('\n')
	output.WriteString(bodyBoundary)
	output.WriteByte('\n')
	output.WriteString(body)
	if output.Len() > MaxFileSize {
		return nil, fmt.Errorf("artifact file is larger than %d MiB", MaxFileSize>>20)
	}
	return output.Bytes(), nil
}

// Decode reads an artifact envelope. Metadata and structured blocks must
// precede the visible Markdown body.
func Decode(data []byte) (File, error) {
	if len(data) > MaxFileSize {
		return File{}, fmt.Errorf("artifact file is larger than %d MiB", MaxFileSize>>20)
	}
	source := string(data)
	item, offset, err := readMetadata(source)
	if err != nil {
		return File{}, err
	}
	file := File{Artifact: item, Blocks: make(map[string]json.RawMessage)}
	for offset < len(source) {
		probe := offset
		for {
			line, end := nextLine(source, probe)
			if !isBlankLine(line) {
				break
			}
			probe = end
			if probe == len(source) {
				break
			}
		}
		if probe == len(source) {
			break
		}
		line, end := nextLine(source, probe)
		if strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r") == bodyBoundary {
			offset = end
			break
		}
		block, isBlock := parseOpening(line)
		if !isBlock || !strings.HasPrefix(block.name, "parchment-") {
			break
		}
		if block.name == metadataBlock {
			return File{}, errors.New("duplicate Parchment metadata block")
		}
		if _, exists := file.Blocks[block.name]; exists {
			return File{}, fmt.Errorf("duplicate %s block", block.name)
		}
		payload, after, err := readBlock(source, probe, block)
		if err != nil {
			return File{}, fmt.Errorf("read %s block: %w", block.name, err)
		}
		if !json.Valid(payload) {
			return File{}, fmt.Errorf("%s block must contain JSON", block.name)
		}
		file.Blocks[block.name] = append(json.RawMessage(nil), payload...)
		offset = after
	}
	file.Body = strings.Clone(source[offset:])
	return file, nil
}

// ReadMetadata reads and validates the leading metadata block without
// interpreting kind-specific payloads or the Markdown body.
func ReadMetadata(data []byte) (artifact.Artifact, error) {
	if len(data) > MaxFileSize {
		return artifact.Artifact{}, fmt.Errorf("artifact file is larger than %d MiB", MaxFileSize>>20)
	}
	source := string(data)
	item, _, err := readMetadata(source)
	if (err != nil || !metadataHasFormatMarker(source)) && isLegacyMetadataExample(source) {
		return artifact.Artifact{}, fmt.Errorf("%w: it must be the first Markdown block", ErrMetadataMissing)
	}
	return item, err
}

// ReadMetadataEnvelope reads the leading metadata of a single-file artifact,
// including older envelopes that omit a body separator.
func ReadMetadataEnvelope(data []byte) (artifact.Artifact, error) {
	if len(data) > MaxFileSize {
		return artifact.Artifact{}, fmt.Errorf("artifact file is larger than %d MiB", MaxFileSize>>20)
	}
	source := string(data)
	item, _, err := readMetadata(source)
	if err != nil && isLegacyMetadataExample(source) && !metadataHasFormatMarker(source) {
		return artifact.Artifact{}, fmt.Errorf("%w: it must be the first Markdown block", ErrMetadataMissing)
	}
	return item, err
}

// HasFormatMarker reports whether data begins with the current single-file
// artifact metadata marker.
func HasFormatMarker(data []byte) bool {
	return metadataHasFormatMarker(string(data))
}

// HasFormatMarkerFrom inspects only the leading metadata block for its format
// marker.
func HasFormatMarkerFrom(input io.Reader) (bool, error) {
	reader := bufio.NewReader(io.LimitReader(input, MaxFileSize+1))
	var prefix strings.Builder
	var consumed int64
	var block opening
	haveBlock := false
	for {
		line, err := reader.ReadString('\n')
		consumed += int64(len(line))
		if consumed > MaxFileSize {
			return false, fmt.Errorf("artifact metadata is larger than %d MiB", MaxFileSize>>20)
		}
		if !haveBlock {
			if isBlankLine(line) {
				prefix.WriteString(line)
			} else {
				var ok bool
				block, ok = parseOpening(line)
				if !ok || block.name != metadataBlock {
					return false, nil
				}
				haveBlock = true
				prefix.WriteString(line)
			}
		} else {
			prefix.WriteString(line)
			if isFenceClose(line, block.marker[:1], len(block.marker)) {
				return metadataHasFormatMarker(prefix.String()), nil
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, fmt.Errorf("read Parchment metadata marker: %w", err)
		}
	}
}

// ReadMetadataFrom reads only the leading metadata block from an artifact
// stream, without loading its structured payloads or Markdown body.
func ReadMetadataFrom(input io.Reader) (artifact.Artifact, error) {
	return readMetadataFrom(input, false)
}

// ReadMetadataEnvelopeFrom reads leading metadata while also accepting
// separator-free single-file artifact envelopes.
func ReadMetadataEnvelopeFrom(input io.Reader) (artifact.Artifact, error) {
	return readMetadataFrom(input, true)
}

func readMetadataFrom(input io.Reader, allowSeparatorFree bool) (artifact.Artifact, error) {
	reader := bufio.NewReader(io.LimitReader(input, MaxFileSize+1))
	var prefix strings.Builder
	var lineBuffer strings.Builder
	var block opening
	var consumed int64
	haveBlock := false
	openingLineIncomplete := false
	for {
		lineBytes, err := reader.ReadSlice('\n')
		consumed += int64(len(lineBytes))
		if consumed > MaxFileSize {
			return artifact.Artifact{}, fmt.Errorf("artifact metadata is larger than %d MiB", MaxFileSize>>20)
		}
		lineBuffer.Write(lineBytes)
		if !haveBlock {
			if errors.Is(err, bufio.ErrBufferFull) {
				line := lineBuffer.String()
				if isBlankLine(line) {
					lineBuffer.Reset()
					prefix.Reset()
					prefix.WriteByte('\n')
					continue
				}
				block, haveBlock = parseOpening(line)
				if !haveBlock || block.name != metadataBlock {
					prefix.WriteString(line)
					return readMetadataBytes(prefix.String(), allowSeparatorFree)
				}
				openingLineIncomplete = true
				continue
			}
			line := lineBuffer.String()
			lineBuffer.Reset()
			if isBlankLine(line) {
				prefix.Reset()
				prefix.WriteByte('\n')
			} else {
				var ok bool
				block, ok = parseOpening(line)
				if !ok || block.name != metadataBlock {
					prefix.WriteString(line)
					return readMetadataBytes(prefix.String(), allowSeparatorFree)
				}
				haveBlock = true
				prefix.WriteString(line)
			}
		} else {
			if errors.Is(err, bufio.ErrBufferFull) {
				continue
			}
			line := lineBuffer.String()
			lineBuffer.Reset()
			prefix.WriteString(line)
			if openingLineIncomplete {
				openingLineIncomplete = false
			} else if isFenceClose(line, block.marker[:1], len(block.marker)) {
				item, _, metadataErr := readMetadata(prefix.String())
				if metadataErr == nil {
					if metadataHasFormatMarker(prefix.String()) || allowSeparatorFree {
						return item, nil
					}
					continuation, probeErr := hasEnvelopeContinuationFrom(reader, &consumed)
					if probeErr != nil {
						return artifact.Artifact{}, fmt.Errorf("inspect artifact format: %w", probeErr)
					}
					if continuation {
						return item, nil
					}
					return artifact.Artifact{}, fmt.Errorf("%w: it must be the first Markdown block", ErrMetadataMissing)
				}
				if metadataHasFormatMarker(prefix.String()) {
					return artifact.Artifact{}, metadataErr
				}
				continuation, probeErr := hasEnvelopeContinuationFrom(reader, &consumed)
				if probeErr != nil {
					return artifact.Artifact{}, fmt.Errorf("inspect artifact format: %w", probeErr)
				}
				if !continuation {
					return artifact.Artifact{}, fmt.Errorf("%w: it must be the first Markdown block", ErrMetadataMissing)
				}
				return artifact.Artifact{}, metadataErr
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return readMetadataBytes(prefix.String(), allowSeparatorFree)
			}
			return artifact.Artifact{}, fmt.Errorf("read Parchment metadata: %w", err)
		}
	}
}

func readMetadataBytes(source string, allowSeparatorFree bool) (artifact.Artifact, error) {
	if allowSeparatorFree {
		item, _, err := readMetadata(source)
		if err != nil && isLegacyMetadataExample(source) && !metadataHasFormatMarker(source) {
			return artifact.Artifact{}, fmt.Errorf("%w: it must be the first Markdown block", ErrMetadataMissing)
		}
		return item, err
	}
	return ReadMetadata([]byte(source))
}

func isLegacyMetadataExample(source string) bool {
	start := 0
	for start < len(source) {
		line, end := nextLine(source, start)
		if !isBlankLine(line) {
			break
		}
		start = end
	}
	if start == len(source) {
		return false
	}
	line, _ := nextLine(source, start)
	block, ok := parseOpening(line)
	if !ok || block.name != metadataBlock {
		return false
	}
	_, after, err := readBlock(source, start, block)
	return err == nil && !hasEnvelopeContinuation(source, after)
}

func metadataHasFormatMarker(source string) bool {
	start := 0
	for start < len(source) {
		line, end := nextLine(source, start)
		if !isBlankLine(line) {
			break
		}
		start = end
	}
	if start == len(source) {
		return false
	}
	line, _ := nextLine(source, start)
	block, ok := parseOpening(line)
	if !ok || block.name != metadataBlock {
		return false
	}
	payload, _, err := readBlock(source, start, block)
	if err != nil {
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return false
	}
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return false
		}
		key, ok := keyToken.(string)
		if !ok {
			return false
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return false
		}
		if key == envelopeFormatField {
			var format string
			return json.Unmarshal(value, &format) == nil && format == envelopeFormat
		}
	}
	return false
}

func hasEnvelopeContinuation(source string, offset int) bool {
	for offset < len(source) {
		line, next := nextLine(source, offset)
		offset = next
		if isBlankLine(line) {
			continue
		}
		if strings.TrimSpace(line) == bodyBoundary {
			return true
		}
		block, ok := parseOpening(line)
		if !ok || !strings.HasPrefix(block.name, "parchment-") {
			return false
		}
		_, after, err := readBlock(source, offset, block)
		if err != nil {
			return false
		}
		offset = after
	}
	return false
}

func hasEnvelopeContinuationFrom(reader *bufio.Reader, consumed *int64) (bool, error) {
	for {
		line, complete, err := readProbeLine(reader, consumed)
		if !complete {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			return false, err
		}
		if isBlankLine(line) {
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
			continue
		}
		if strings.TrimSpace(line) == bodyBoundary {
			return true, nil
		}
		block, ok := parseOpening(line)
		if !ok || !strings.HasPrefix(block.name, "parchment-") {
			return false, nil
		}
		for {
			line, complete, err = readProbeLine(reader, consumed)
			if !complete {
				if errors.Is(err, io.EOF) {
					return false, nil
				}
				if err != nil {
					return false, err
				}
				continue
			}
			if isFenceClose(line, block.marker[:1], len(block.marker)) {
				break
			}
			if errors.Is(err, io.EOF) {
				return false, nil
			}
			if err != nil {
				return false, err
			}
		}
	}
}

func readProbeLine(reader *bufio.Reader, consumed *int64) (string, bool, error) {
	var line strings.Builder
	oversized := false
	for {
		part, err := reader.ReadSlice('\n')
		*consumed += int64(len(part))
		if *consumed > MaxFileSize {
			return "", false, fmt.Errorf("artifact metadata is larger than %d MiB", MaxFileSize>>20)
		}
		if !oversized && line.Len()+len(part) <= 4<<10 {
			line.Write(part)
		} else {
			oversized = true
			line.Reset()
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return line.String(), !oversized, err
	}
}

func readMetadata(source string) (artifact.Artifact, int, error) {
	start := 0
	for start < len(source) {
		line, end := nextLine(source, start)
		if !isBlankLine(line) {
			break
		}
		start = end
	}
	if start == len(source) {
		return artifact.Artifact{}, 0, fmt.Errorf("%w: it must be the first Markdown block", ErrMetadataMissing)
	}
	line, _ := nextLine(source, start)
	opening, ok := parseOpening(line)
	if !ok || opening.name != metadataBlock {
		if _, info, isFence := parseFence(line); isFence {
			fields := strings.Fields(info)
			if len(fields) > 0 && fields[0] == metadataBlock {
				return artifact.Artifact{}, 0, errors.New("Parchment metadata block must be the first Markdown block")
			}
		}
		return artifact.Artifact{}, 0, fmt.Errorf("%w: it must be the first Markdown block", ErrMetadataMissing)
	}
	metadataBytes, offset, err := readBlock(source, start, opening)
	if err != nil {
		return artifact.Artifact{}, 0, fmt.Errorf("read Parchment metadata: %w", err)
	}
	var envelope metadataEnvelope
	if err := json.Unmarshal(metadataBytes, &envelope); err != nil {
		return artifact.Artifact{}, 0, fmt.Errorf("decode Parchment metadata: %w", err)
	}
	if envelope.Format != "" && envelope.Format != envelopeFormat {
		return artifact.Artifact{}, 0, fmt.Errorf("unsupported artifact format %q", envelope.Format)
	}
	item := envelope.Artifact
	if err := item.Validate(); err != nil {
		return artifact.Artifact{}, 0, fmt.Errorf("validate Parchment metadata: %w", err)
	}
	item.CreatedAt = item.CreatedAt.UTC()
	item.ModifiedAt = item.ModifiedAt.UTC()
	return item, offset, nil
}

// StripPrivateBlocks removes all fenced blocks whose info string starts with
// "parchment-". Ordinary Markdown code fences are preserved.
func StripPrivateBlocks(markdown string) string {
	var output strings.Builder
	fence := ""
	var containers fenceContainers
	var lists []listContainer
	skipping := false
	for offset := 0; offset < len(markdown); {
		line, next := nextLine(markdown, offset)
		containerLine, blockquotes := stripBlockquotePrefixes(line)
		if fence != "" {
			if !fenceContainerActive(line, containers) {
				fence, containers, skipping = "", fenceContainers{}, false
			} else if isFenceCloseInContainers(line, containers, fence[:1], len(fence)) {
				if !skipping {
					output.WriteString(line)
				}
				fence, containers, skipping = "", fenceContainers{}, false
				offset = next
				continue
			} else {
				if !skipping {
					output.WriteString(line)
				}
				offset = next
				continue
			}
		}
		lists = updateListContainers(lists, containerLine, blockquotes)
		block, context, ok := parseListContinuationFence(lists, line)
		if !ok {
			block, context, ok = parseContainerFence(line)
		}
		if ok {
			fence = block.marker
			containers = context
			skipping = strings.HasPrefix(block.name, "parchment-")
			if !skipping {
				output.WriteString(line)
			}
			offset = next
			continue
		}
		output.WriteString(line)
		offset = next
	}
	return output.String()
}

type fenceContainers struct {
	blockquotes int
	listIndent  int
}

type listContainer struct {
	blockquotes int
	indent      int
}

func parseContainerFence(line string) (opening, fenceContainers, bool) {
	var blockquotes, listIndent int
	for {
		stripped, depth := stripBlockquotePrefixes(line)
		if depth > 0 {
			blockquotes += depth
			line = stripped
			continue
		}
		if marker, info, ok := parseFence(line); ok {
			return opening{marker: marker, name: info},
				fenceContainers{blockquotes: blockquotes, listIndent: listIndent}, true
		}
		content, indent, ok := stripListMarker(line)
		if !ok {
			return opening{}, fenceContainers{}, false
		}
		listIndent += indent
		line = content
	}
}

func updateListContainers(lists []listContainer, line string, blockquotes int) []listContainer {
	for len(lists) > 0 && lists[len(lists)-1].blockquotes > blockquotes {
		lists = lists[:len(lists)-1]
	}
	if isBlankLine(line) {
		return lists
	}
	if _, indent, ok := stripListMarker(line); ok {
		for len(lists) > 0 && lists[len(lists)-1].blockquotes == blockquotes &&
			lists[len(lists)-1].indent >= indent {
			lists = lists[:len(lists)-1]
		}
		return append(lists, listContainer{blockquotes: blockquotes, indent: indent})
	}
	leading := leadingSpaces(line)
	for len(lists) > 0 && lists[len(lists)-1].blockquotes == blockquotes &&
		lists[len(lists)-1].indent > leading {
		lists = lists[:len(lists)-1]
	}
	return lists
}

func parseListContinuationFence(lists []listContainer, line string) (opening, fenceContainers, bool) {
	for i := len(lists) - 1; i >= 0; i-- {
		container := lists[i]
		if leadingSpaces(line) >= container.indent {
			content := stripIndent(line, container.indent)
			content, blockquotes := stripBlockquotePrefixes(content)
			if blockquotes >= container.blockquotes {
				if marker, info, ok := parseFence(content); ok {
					return opening{marker: marker, name: info},
						fenceContainers{blockquotes: blockquotes, listIndent: container.indent}, true
				}
			}
		}
		content, leadingBlockquotes := stripBlockquotePrefixes(line)
		if leadingBlockquotes >= container.blockquotes && leadingSpaces(content) >= container.indent {
			content = stripIndent(content, container.indent)
			content, nestedBlockquotes := stripBlockquotePrefixes(content)
			if marker, info, ok := parseFence(content); ok {
				return opening{marker: marker, name: info},
					fenceContainers{
						blockquotes: leadingBlockquotes + nestedBlockquotes,
						listIndent:  container.indent,
					}, true
			}
		}
	}
	return opening{}, fenceContainers{}, false
}

func leadingSpaces(line string) int {
	text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	indent := 0
	for indent < len(text) && text[indent] == ' ' {
		indent++
	}
	return indent
}

func stripIndent(line string, indent int) string {
	if indent == 0 {
		return line
	}
	return line[indent:]
}

func stripListMarker(line string) (string, int, bool) {
	text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	indent := 0
	for indent < len(text) && indent < 4 && text[indent] == ' ' {
		indent++
	}
	if indent > 3 || indent == len(text) {
		return "", 0, false
	}
	end := indent
	if text[end] == '-' || text[end] == '+' || text[end] == '*' {
		end++
	} else {
		digits := end
		for end < len(text) && text[end] >= '0' && text[end] <= '9' && end-digits < 10 {
			end++
		}
		if end == digits || end == len(text) || text[end] != '.' && text[end] != ')' {
			return "", 0, false
		}
		end++
	}
	if end == len(text) || text[end] != ' ' && text[end] != '\t' {
		return "", 0, false
	}
	for end < len(text) && (text[end] == ' ' || text[end] == '\t') {
		end++
	}
	ending := line[len(text):]
	return text[end:] + ending, end, true
}

func isFenceCloseInContainers(line string, containers fenceContainers, marker string, minLength int) bool {
	line, blockquotes, ok := stripFenceContainers(line, containers)
	return ok && blockquotes == containers.blockquotes && isFenceClose(line, marker, minLength)
}

func fenceContainerActive(line string, containers fenceContainers) bool {
	if containers.listIndent > 0 {
		content, blockquotes := stripBlockquotePrefixes(line)
		if isBlankLine(content) {
			return blockquotes >= containers.blockquotes
		}
	}
	_, blockquotes, ok := stripFenceContainers(line, containers)
	return ok && blockquotes >= containers.blockquotes
}

func stripFenceContainers(line string, containers fenceContainers) (string, int, bool) {
	if containers.listIndent == 0 {
		content, blockquotes := stripBlockquotePrefixes(line)
		return content, blockquotes, true
	}
	if leadingSpaces(line) >= containers.listIndent {
		content := stripIndent(line, containers.listIndent)
		content, blockquotes := stripBlockquotePrefixes(content)
		if blockquotes >= containers.blockquotes {
			return content, blockquotes, true
		}
	}
	content, leadingBlockquotes := stripBlockquotePrefixes(line)
	if leadingSpaces(content) >= containers.listIndent {
		content = stripIndent(content, containers.listIndent)
		content, nestedBlockquotes := stripBlockquotePrefixes(content)
		return content, leadingBlockquotes + nestedBlockquotes, true
	}
	return "", 0, false
}

func stripBlockquotePrefixes(line string) (string, int) {
	endingStart := len(line)
	for endingStart > 0 && (line[endingStart-1] == '\n' || line[endingStart-1] == '\r') {
		endingStart--
	}
	ending := line[endingStart:]
	text := line[:endingStart]
	depth := 0
	for {
		spaces := 0
		for spaces < len(text) && spaces < 4 && text[spaces] == ' ' {
			spaces++
		}
		if spaces > 3 || spaces == len(text) || text[spaces] != '>' {
			if depth == 0 {
				return line, depth
			}
			return text + ending, depth
		}
		text = text[spaces+1:]
		if strings.HasPrefix(text, " ") || strings.HasPrefix(text, "\t") {
			text = text[1:]
		}
		depth++
	}
}

type opening struct {
	name   string
	marker string
}

func nextLine(source string, start int) (string, int) {
	if start >= len(source) {
		return "", start
	}
	offset := strings.IndexByte(source[start:], '\n')
	if offset < 0 {
		return source[start:], len(source)
	}
	offset += start + 1
	return source[start:offset], offset
}

func parseOpening(line string) (opening, bool) {
	marker, info, ok := parseFence(line)
	if !ok {
		return opening{}, false
	}
	name := strings.Fields(info)
	if len(name) != 1 || !validBlockName(name[0]) {
		return opening{}, false
	}
	return opening{name: name[0], marker: marker}, true
}

func parseFence(line string) (string, string, bool) {
	text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	trimmed := strings.TrimLeft(text, " ")
	if len(text)-len(trimmed) > 3 || len(trimmed) < 3 {
		return "", "", false
	}
	markerChar := trimmed[0]
	if markerChar != '`' && markerChar != '~' {
		return "", "", false
	}
	count := 0
	for count < len(trimmed) && trimmed[count] == markerChar {
		count++
	}
	if count < 3 || (markerChar == '`' && strings.Contains(trimmed[count:], "`")) {
		return "", "", false
	}
	info := strings.TrimSpace(trimmed[count:])
	return strings.Repeat(string(markerChar), count), info, true
}

func readBlock(source string, start int, block opening) ([]byte, int, error) {
	_, payloadStart := nextLine(source, start)
	for offset := payloadStart; offset < len(source); {
		line, next := nextLine(source, offset)
		if isFenceClose(line, block.marker[:1], len(block.marker)) {
			payload := strings.TrimSuffix(source[payloadStart:offset], "\n")
			payload = strings.TrimSuffix(payload, "\r")
			return []byte(payload), next, nil
		}
		offset = next
	}
	return nil, 0, errors.New("closing code fence is missing")
}

func isFenceClose(line, marker string, minLength int) bool {
	text := strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")
	trimmed := strings.TrimLeft(text, " ")
	if len(text)-len(trimmed) > 3 || !strings.HasPrefix(trimmed, marker) {
		return false
	}
	i := 0
	for i < len(trimmed) && trimmed[i] == marker[0] {
		i++
	}
	return i >= minLength && strings.TrimSpace(trimmed[i:]) == ""
}

func isBlankLine(line string) bool {
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(line, "\n"), "\r")) == ""
}

func validBlockName(name string) bool {
	if name == "" {
		return false
	}
	for i, r := range name {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || i > 0 && r == '-' {
			continue
		}
		return false
	}
	return strings.HasPrefix(name, "parchment-")
}

func writeBlock(output *bytes.Buffer, name string, payload []byte) {
	if output.Len() > 0 {
		output.WriteByte('\n')
	}
	fmt.Fprintf(output, "```%s\n", name)
	output.Write(payload)
	output.WriteString("\n```\n")
}
