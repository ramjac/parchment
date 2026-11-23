package tui

import (
	"context"
	"errors"
	"sort"
	"strings"
	"unsafe"

	"charm.land/lipgloss/v2"
	glamouransi "github.com/charmbracelet/glamour/ansi"
	"github.com/charmbracelet/x/ansi"
	"github.com/yuin/goldmark/ast"
)

const (
	maxReaderCheckpoints          = 4096
	maxReaderCheckpointBytes      = 8 << 20
	maxReaderCheckpointSource     = 16 << 20
	maxReaderCheckpointNodeVisits = 500000
	maxReaderCachedLinkBytes      = 8 << 20
	maxReaderCachedSlotBytes      = 8 << 20
	maxReaderSearchIndexBytes     = 16 << 20
	readerCheckpointRows          = 256
)

var errReaderWindowComplete = errors.New("reader window complete")

type readerCheckpoint struct {
	row, blanks int
	point       *glamouransi.StreamingCheckpoint
}

type readerSearchIndex struct {
	text    string
	offsets []uint32
}

// readerWindowSource is immutable after it is published to the reader.
type readerWindowSource struct {
	source     []byte
	root       ast.Node
	options    glamouransi.Options
	navigation map[rune]int
	slots      map[rune][]string
	links      []linkHitBox
	linkStyle  lipgloss.Style
	points     []readerCheckpoint
	interval   int
	nextRow    int
	search     *readerSearchIndex
}

func (s *readerWindowSource) add(row, blanks int, point *glamouransi.StreamingCheckpoint) {
	if row < s.nextRow {
		return
	}
	if s.interval == 0 {
		s.interval = readerCheckpointRows
	}
	entryBytes := int(unsafe.Sizeof(readerCheckpoint{})) + int(unsafe.Sizeof(glamouransi.StreamingCheckpoint{}))
	limit := min(maxReaderCheckpoints, maxReaderCheckpointBytes/entryBytes)
	if len(s.points) >= limit {
		for i := 1; i < len(s.points); i += 2 {
			s.points[i/2] = s.points[i]
		}
		s.points = s.points[:len(s.points)/2]
		s.interval *= 2
	}
	if len(s.points) == cap(s.points) {
		points := make([]readerCheckpoint, len(s.points), min(limit, max(8, 2*cap(s.points))))
		copy(points, s.points)
		s.points = points
	}
	s.points = append(s.points, readerCheckpoint{row: row, blanks: blanks, point: point})
	s.nextRow = row + s.interval
}

func (s *readerWindowSource) setLinks(boxes []linkHitBox, style lipgloss.Style) bool {
	if len(boxes) > maxReaderCachedLinkBytes/int(unsafe.Sizeof(linkHitBox{})) {
		return false
	}
	s.links = append([]linkHitBox(nil), boxes...)
	sort.SliceStable(s.links, func(i, j int) bool { return s.links[i].row < s.links[j].row })
	s.linkStyle = style
	return true
}

func (s *readerWindowSource) setSource(ctx context.Context, source []byte, slots map[rune][]string) bool {
	if len(source) > maxReaderCheckpointSource {
		return false
	}
	size := 0
	visited := 0
	for _, lines := range slots {
		size += 64
		if size > maxReaderCachedSlotBytes {
			return false
		}
		for _, line := range lines {
			if visited%256 == 0 && ctx.Err() != nil {
				return false
			}
			visited++
			lineSize := len(line) + int(unsafe.Sizeof(""))
			if lineSize > maxReaderCachedSlotBytes-size {
				return false
			}
			size += lineSize
		}
	}
	s.source, s.slots = source, slots
	for i, point := range s.points {
		if i%256 == 0 && ctx.Err() != nil {
			return false
		}
		point.point.SetSource(source)
	}
	return true
}

func (s *readerWindowSource) setSearchIndex(ctx context.Context, lines []string) {
	s.setSearchIndexLimit(ctx, lines, maxReaderSearchIndexBytes)
}

func (s *readerWindowSource) setSearchIndexLimit(ctx context.Context, lines []string, limit int) {
	offsetBytes := (len(lines)+1)*int(unsafe.Sizeof(uint32(0))) + int(unsafe.Sizeof(readerSearchIndex{}))
	if offsetBytes > limit {
		return
	}
	size := 0
	for i, line := range lines {
		if i%256 == 0 && ctx.Err() != nil {
			return
		}
		size += len(ansi.Strip(line))
		if size > limit-offsetBytes {
			return
		}
	}
	var text strings.Builder
	text.Grow(size)
	if text.Cap() > limit-offsetBytes {
		return
	}
	offsets := make([]uint32, len(lines)+1)
	for i, line := range lines {
		if i%256 == 0 && ctx.Err() != nil {
			return
		}
		text.WriteString(ansi.Strip(line))
		offsets[i+1] = uint32(text.Len())
	}
	s.search = &readerSearchIndex{text: text.String(), offsets: offsets}
}

func (s *readerWindowSource) lines(ctx context.Context, start, end int) ([]string, error) {
	var result []string
	err := s.scan(ctx, start, end, func(_ int, line string) error {
		result = append(result, line)
		return nil
	})
	return result, err
}

func (s *readerWindowSource) matches(ctx context.Context, query string, total, limit int) ([]renderedMatch, error) {
	var matches []renderedMatch
	matcher := newTextMatcher(query)
	match := func(row int, line string) error {
		return matcher.eachMatchContext(ctx, line, func(_, _ int) error {
			if len(matches) >= limit {
				return errReaderSearchLimit
			}
			matches = append(matches, renderedMatch{row: row})
			return nil
		})
	}
	var err error
	if s.search != nil && total == len(s.search.offsets)-1 {
		for row := 0; row < total; row++ {
			if row%256 == 0 {
				if err = ctx.Err(); err != nil {
					break
				}
			}
			if err = match(row, s.search.text[s.search.offsets[row]:s.search.offsets[row+1]]); err != nil {
				break
			}
		}
	} else {
		err = s.scan(ctx, 0, total, func(row int, line string) error {
			return match(row, ansi.Strip(line))
		})
	}
	if err != nil {
		return nil, err
	}
	return matches, nil
}

func (s *readerWindowSource) scan(ctx context.Context, start, end int, visit func(int, string) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if start < 0 || end <= start {
		return nil
	}
	row, blanks, emitted := 0, 0, false
	index := sort.Search(len(s.points), func(i int) bool { return s.points[i].row > start }) - 1
	if index >= 0 {
		row, blanks, emitted = s.points[index].row, s.points[index].blanks, true
	}
	emit := func(line string) error {
		if row >= start {
			line = strings.Map(func(r rune) rune {
				if _, ok := s.navigation[r]; ok {
					return -1
				}
				return r
			}, line)
			first := sort.Search(len(s.links), func(i int) bool { return s.links[i].row >= row })
			for i := first; i < len(s.links) && s.links[i].row == row; i++ {
				box := s.links[i]
				prefix := ansi.Truncate(line, box.startCol, "")
				middle := ansi.Truncate(ansi.TruncateLeft(line, box.startCol, ""), box.endCol-box.startCol, "")
				suffix := ansi.TruncateLeft(line, box.endCol, "")
				line = prefix + s.linkStyle.Render(ansi.Strip(middle)) + suffix
			}
			if err := visit(row, line); err != nil {
				return err
			}
		}
		row++
		if row >= end {
			return errReaderWindowComplete
		}
		return nil
	}
	writer := documentLineWriter{
		ctx: ctx, maxLine: maxReaderLineBytes, blanks: blanks, emitted: emitted,
		visit: func(line string) error {
			for _, marker := range line {
				if rows, ok := s.slots[marker]; ok {
					before, after, _ := strings.Cut(line, string(marker))
					if strings.TrimSpace(ansi.Strip(before)) != "" {
						if err := emit(before); err != nil {
							return err
						}
					}
					for _, imageRow := range rows {
						if err := emit("  " + imageRow); err != nil {
							return err
						}
					}
					line = after
				}
			}
			return emit(line)
		},
	}
	var cursor *glamouransi.StreamingCursor
	if index < 0 {
		cursor = glamouransi.NewStreamingCursor(ctx, s.options, s.source, s.root, &writer)
	} else {
		var err error
		cursor, err = glamouransi.ResumeStreamingCursor(ctx, s.points[index].point, &writer)
		if err != nil {
			return err
		}
	}
	for {
		done, err := cursor.Step()
		if err != nil {
			if errors.Is(err, errReaderWindowComplete) {
				return nil
			}
			return err
		}
		if done {
			if err := writer.finish(); err != nil && !errors.Is(err, errReaderWindowComplete) {
				return err
			}
			return nil
		}
		if err := cursor.Flush(); err != nil {
			if errors.Is(err, errReaderWindowComplete) {
				return nil
			}
			return err
		}
	}
}
