package shared

import "strings"

type SegmentKind string

const (
	SegmentKindText      SegmentKind = "text"
	SegmentKindReasoning SegmentKind = "reasoning"
)

type Segment struct {
	Kind SegmentKind
	Text string
}

type ThinkingSplitter struct {
	buffer     string
	inThinking bool
}

func (s *ThinkingSplitter) Feed(chunk string, flush bool) []Segment {
	s.buffer += chunk
	var segments []Segment

	for {
		if !s.inThinking {
			idx := strings.Index(s.buffer, "<thinking>")
			if idx >= 0 {
				if idx > 0 {
					segments = append(segments, Segment{Kind: SegmentKindText, Text: s.buffer[:idx]})
				}
				s.buffer = s.buffer[idx+len("<thinking>"):]
				s.inThinking = true
				continue
			}

			emitted, keep := splitSafePrefix(s.buffer, flush)
			if emitted != "" {
				segments = append(segments, Segment{Kind: SegmentKindText, Text: emitted})
			}
			s.buffer = keep
			break
		}

		idx := strings.Index(s.buffer, "</thinking>")
		if idx >= 0 {
			if idx > 0 {
				segments = append(segments, Segment{Kind: SegmentKindReasoning, Text: s.buffer[:idx]})
			}
			s.buffer = s.buffer[idx+len("</thinking>"):]
			s.inThinking = false
			continue
		}

		emitted, keep := splitSafePrefix(s.buffer, flush)
		if emitted != "" {
			segments = append(segments, Segment{Kind: SegmentKindReasoning, Text: emitted})
		}
		s.buffer = keep
		break
	}

	return segments
}

func splitSafePrefix(text string, flush bool) (string, string) {
	if text == "" {
		return "", ""
	}

	runes := []rune(text)
	if flush {
		return text, ""
	}

	if len(runes) <= 15 {
		return "", text
	}

	safe := string(runes[:len(runes)-15])
	keep := string(runes[len(runes)-15:])
	return safe, keep
}
