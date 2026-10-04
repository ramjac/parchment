package artifactfile

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"example.com/parchment/internal/artifact"
)

const metadataBlock = "parchment-meta"

const MaxFileSize = 64 << 20

// File is a Markdown artifact with shared metadata and optional structured
// payloads stored in hidden Parchment code fences.
type File struct {
	Artifact artifact.Artifact
	Body     string
	Blocks   map[string]json.RawMessage
}

// Encode serializes an artifact as Markdown. Payload blocks are emitted before
// the visible body so ordinary Markdown readers can ignore them.
func Encode(item artifact.Artifact, body string, blocks map[string]any) ([]byte, error) {
	if err := item.Validate(); err != nil {
		return nil, fmt.Errorf("validate artifact metadata: %w", err)
	}
	metadata, err := json.MarshalIndent(item, "", "  ")
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
		payload, err := json.MarshalIndent(blocks[name], "", "  ")
		if err != nil {
			return nil, fmt.Errorf("encode %s block: %w", name, err)
		}
		writeBlock(&output, name, payload)
	}
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
		line, end := nextLine(source, probe)
		if isBlankLine(line) {
			probe = end
			if probe == len(source) {
				offset = probe
				break
			}
		}
		line, _ = nextLine(source, probe)
		block, isBlock := parseOpening(line)
		if !isBlock || !strings.HasPrefix(block.name, "parchment-") {
			offset = probe
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
	item, _, err := readMetadata(string(data))
	return item, err
}

func readMetadata(source string) (artifact.Artifact, int, error) {
	line, _ := nextLine(source, 0)
	opening, ok := parseOpening(line)
	if !ok || opening.name != metadataBlock {
		return artifact.Artifact{}, 0, errors.New("Parchment metadata block must be the first Markdown block")
	}
	metadataBytes, offset, err := readBlock(source, 0, opening)
	if err != nil {
		return artifact.Artifact{}, 0, fmt.Errorf("read Parchment metadata: %w", err)
	}
	var item artifact.Artifact
	if err := json.Unmarshal(metadataBytes, &item); err != nil {
		return artifact.Artifact{}, 0, fmt.Errorf("decode Parchment metadata: %w", err)
	}
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
	skipping := false
	for offset := 0; offset < len(markdown); {
		line, next := nextLine(markdown, offset)
		if fence != "" {
			if isFenceClose(line, fence[:1], len(fence)) {
				if !skipping {
					output.WriteString(line)
				}
				fence, skipping = "", false
			} else if !skipping {
				output.WriteString(line)
			}
			offset = next
			continue
		}
		marker, info, ok := parseFence(line)
		if ok {
			fence = marker
			skipping = strings.HasPrefix(info, "parchment-")
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
