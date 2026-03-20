package intent

import (
	"strings"
	"sync"
)

// StreamParser receives raw text chunks from the LLM SSE stream,
// looks for the JSON `speak` tool task, and extracts the `text` parameter
// values progressively, sending them to the TextStream channel.
type StreamParser struct {
	TextStream       chan string
	buffer           string
	lastExtractedLen int
	mu               sync.Mutex
	isClosed         bool
}

func NewStreamParser() *StreamParser {
	return &StreamParser{
		TextStream: make(chan string, 100),
	}
}

// Feed receives a raw string chunk from the LLM SSE stream.
func (p *StreamParser) Feed(chunk string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.isClosed {
		return
	}

	p.buffer += chunk

	toolIdx := strings.Index(p.buffer, `"tool"`)
	if toolIdx == -1 {
		return
	}
	speakIdx := strings.Index(p.buffer[toolIdx:], `"speak"`)
	if speakIdx == -1 {
		return // Not a speak tool or 'speak' not emitted yet
	}

	textKeyIdx := strings.Index(p.buffer[toolIdx:], `"text"`)
	if textKeyIdx == -1 {
		return // 'text' not emitted yet
	}
	textKeyIdx += toolIdx // Make absolute index

	// Search for the opening quote of the value
	// We look for : then "
	colonIdx := strings.Index(p.buffer[textKeyIdx:], ":")
	if colonIdx == -1 {
		return
	}
	colonIdx += textKeyIdx

	firstQuoteIdx := strings.Index(p.buffer[colonIdx:], `"`)
	if firstQuoteIdx == -1 {
		return
	}
	firstQuoteIdx += colonIdx
	startIdx := firstQuoteIdx + 1

	if len(p.buffer) <= startIdx {
		return // Value hasn't started yet
	}

	// Now unescape the string from startIdx to the end or the closing quote
	content := p.buffer[startIdx:]
	var unescapedBuilder strings.Builder
	var isEscaped bool
	var foundEndQuote bool

	for _, ch := range content {
		if isEscaped {
			switch ch {
			case 'n':
				unescapedBuilder.WriteRune('\n')
			case 't':
				unescapedBuilder.WriteRune('\t')
			case 'r':
				unescapedBuilder.WriteRune('\r')
			case '\\', '"':
				unescapedBuilder.WriteRune(ch)
			default:
				unescapedBuilder.WriteRune(ch)
			}
			isEscaped = false
		} else if ch == '\\' {
			isEscaped = true
		} else if ch == '"' {
			foundEndQuote = true
			break
		} else {
			unescapedBuilder.WriteRune(ch)
		}
	}

	currentExtracted := unescapedBuilder.String()

	// Only send the *new* part of the string
	if len(currentExtracted) > p.lastExtractedLen {
		delta := currentExtracted[p.lastExtractedLen:]
		p.lastExtractedLen = len(currentExtracted)
		p.TextStream <- delta
	}

	if foundEndQuote {
		p.isClosed = true
		close(p.TextStream)
	}
}

func (p *StreamParser) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.isClosed {
		p.isClosed = true
		close(p.TextStream)
	}
}
