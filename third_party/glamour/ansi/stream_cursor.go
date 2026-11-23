package ansi

import (
	"bufio"
	"context"
	"io"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

type streamRegistry map[ast.NodeKind]renderer.NodeRendererFunc

func (r streamRegistry) Register(kind ast.NodeKind, fn renderer.NodeRendererFunc) {
	r[kind] = fn
}

// StreamingCursor renders one top-level Markdown block at a time.
type StreamingCursor struct {
	ctx       context.Context
	source    []byte
	root      ast.Node
	next      ast.Node
	renderer  *ANSIRenderer
	functions streamRegistry
	writer    util.BufWriter
	started   bool
	done      bool
	err       error
}

// StreamingCheckpoint identifies a safe top-level restart point. The source,
// tree and options must remain unchanged while any checkpoint is in use.
type StreamingCheckpoint struct {
	source  []byte
	root    ast.Node
	next    ast.Node
	options Options
}

// SetSource rebinds a checkpoint to the final source after append-only
// additions. Existing bytes and the completed tree must remain unchanged
// while the checkpoint is in use.
func (p *StreamingCheckpoint) SetSource(source []byte) {
	p.source = source
}

// NewStreamingCursor creates a cursor over an already parsed document.
// The source and tree must remain unchanged while the cursor is in use.
func NewStreamingCursor(ctx context.Context, options Options, source []byte, root ast.Node, output io.Writer) *StreamingCursor {
	r := NewStreamingRenderer(options)
	functions := make(streamRegistry)
	r.RegisterFuncs(functions)
	writer, ok := output.(util.BufWriter)
	if !ok {
		writer = bufio.NewWriter(output)
	}
	return &StreamingCursor{
		ctx: ctx, source: source, root: root, next: root.FirstChild(),
		renderer: r, functions: functions, writer: writer,
	}
}

func (c *StreamingCursor) call(node ast.Node, entering bool) (ast.WalkStatus, error) {
	if err := c.ctx.Err(); err != nil {
		return ast.WalkStop, err
	}
	if fn := c.functions[node.Kind()]; fn != nil {
		return fn(c.writer, c.source, node, entering)
	}
	return ast.WalkContinue, nil
}

// Step renders the next block, returning true after the document is finished.
func (c *StreamingCursor) Step() (bool, error) {
	if c.err != nil {
		return false, c.err
	}
	if c.done {
		return true, nil
	}
	if !c.started {
		if _, err := c.call(c.root, true); err != nil {
			c.err = err
			return false, err
		}
		c.started = true
	}
	if c.next != nil {
		node := c.next
		c.next = node.NextSibling()
		if err := ast.Walk(node, c.call); err != nil {
			c.err = err
			return false, err
		}
		return false, nil
	}
	if _, err := c.call(c.root, false); err != nil {
		c.err = err
		return false, err
	}
	if err := c.writer.Flush(); err != nil {
		c.err = err
		return false, err
	}
	c.done = true
	return true, nil
}

// Flush publishes completed buffered output without finishing the document.
func (c *StreamingCursor) Flush() error {
	if c.err != nil {
		return c.err
	}
	c.err = c.writer.Flush()
	return c.err
}

// NextNode is the next top-level node to be rendered, or nil at the end.
func (c *StreamingCursor) NextNode() ast.Node {
	return c.next
}

// SetSource updates the source after appending segments for unrendered nodes.
func (c *StreamingCursor) SetSource(source []byte) {
	c.source = source
}

// AtLineBoundary reports whether the root has no unfinished output line.
func (c *StreamingCursor) AtLineBoundary() bool {
	return c.started && !c.done && c.err == nil && c.ctx.Err() == nil &&
		c.renderer.context.blockStack.Len() == 1 && c.renderer.context.blockStack.Current().Block.Len() == 0
}

// EnableCheckpoints enables checkpoint tracking before the first step.
func (c *StreamingCursor) EnableCheckpoints() bool {
	if c.started || c.done {
		return false
	}
	c.renderer.streamCheckpoints = true
	return true
}

// Checkpoint returns a restart point only when the root buffer and both
// margin writers are at their initial state. Call Flush before using the
// corresponding output position.
func (c *StreamingCursor) Checkpoint() (*StreamingCheckpoint, bool) {
	r := c.renderer
	if !r.streamCheckpoints || !c.AtLineBoundary() || r.streamMargin == nil || !r.streamLineStart || r.streamANSI || r.streamDirty {
		return nil, false
	}
	return &StreamingCheckpoint{source: c.source, root: c.root, next: c.next, options: r.context.options}, true
}

// ResumeStreamingCursor starts at a checkpoint without emitting the document
// prefix again. It emits the usual suffix when the cursor reaches the end.
func ResumeStreamingCursor(ctx context.Context, checkpoint *StreamingCheckpoint, output io.Writer) (*StreamingCursor, error) {
	c := NewStreamingCursor(ctx, checkpoint.options, checkpoint.source, checkpoint.root, io.Discard)
	if _, err := c.call(c.root, true); err != nil {
		return nil, err
	}
	c.renderer.context.blockStack.Current().Block.Reset()
	c.renderer.streamCheckpoints = true
	c.started = true
	c.next = checkpoint.next
	if writer, ok := output.(util.BufWriter); ok {
		c.writer = writer
	} else {
		c.writer = bufio.NewWriter(output)
	}
	return c, nil
}
