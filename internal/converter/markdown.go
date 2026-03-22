// Package converter turns TDLib Message objects into Markdown files with YAML
// front-matter.
package converter

import (
	"fmt"
	"sort"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/zelenin/go-tdlib/client"
)

// MediaRef describes a single media attachment referenced in a post.
type MediaRef struct {
	Type     string // "photo", "video", "audio", "voice_note", "document", "animation"
	File     string // local filename relative to the channel output directory
	Duration int32  // seconds (video / audio / voice_note)
	Width    int32  // pixels (video)
	Height   int32  // pixels (video)
	MimeType string
	FileName string // original name for documents
}

// PostData is the intermediate representation of a post before serialisation.
type PostData struct {
	ID            int64
	Date          int32
	AuthorName    string
	TelegramURL   string // link back to original post, e.g. https://t.me/channel/123
	EditDate      int32
	ViewCount     int32
	ForwardCount  int32
	CommentsCount int32
	ReplyToID     int64
	MediaAlbumID  int64
	Media         []MediaRef
	Reactions     map[string]int32
	Poll          *PollData
	Text          string // Markdown body
}

// PollData represents a poll embedded in a post.
type PollData struct {
	Question  string
	Type      string // "regular" or "quiz"
	Anonymous bool
	Closed    bool
	Options   []PollOption
}

// PollOption is one choice in a poll.
type PollOption struct {
	Text  string
	Votes int32
}

// ConvertMessage builds a PostData from a TDLib message.
// mediaRefs should already be populated by the caller after media download.
func ConvertMessage(msg *client.Message, chatTitle string, mediaRefs []MediaRef) PostData {
	pd := PostData{
		ID:         msg.Id,
		Date:       msg.Date,
		EditDate:   msg.EditDate,
		AuthorName: chatTitle,
		Media:      mediaRefs,
	}

	if msg.InteractionInfo != nil {
		pd.ViewCount = msg.InteractionInfo.ViewCount
		pd.ForwardCount = msg.InteractionInfo.ForwardCount
		if msg.InteractionInfo.ReplyInfo != nil {
			pd.CommentsCount = msg.InteractionInfo.ReplyInfo.ReplyCount
		}
		if msg.InteractionInfo.Reactions != nil {
			pd.Reactions = extractReactions(msg.InteractionInfo.Reactions)
		}
	}

	if msg.ReplyTo != nil {
		if replyToMsg, ok := msg.ReplyTo.(*client.MessageReplyToMessage); ok {
			pd.ReplyToID = replyToMsg.MessageId
		}
	}

	if msg.MediaAlbumId != 0 {
		pd.MediaAlbumID = int64(msg.MediaAlbumId)
	}

	switch c := msg.Content.(type) {
	case *client.MessageText:
		pd.Text = FormattedTextToMarkdown(c.Text)
	case *client.MessagePhoto:
		pd.Text = FormattedTextToMarkdown(c.Caption)
	case *client.MessageVideo:
		pd.Text = FormattedTextToMarkdown(c.Caption)
	case *client.MessageAudio:
		pd.Text = FormattedTextToMarkdown(c.Caption)
	case *client.MessageDocument:
		pd.Text = FormattedTextToMarkdown(c.Caption)
	case *client.MessageVoiceNote:
		pd.Text = FormattedTextToMarkdown(c.Caption)
	case *client.MessageAnimation:
		pd.Text = FormattedTextToMarkdown(c.Caption)
	case *client.MessagePoll:
		pd.Poll = convertPoll(c.Poll)
	}

	return pd
}

// Render serialises PostData into Markdown bytes (YAML front-matter + body).
func Render(pd PostData) []byte {
	var sb strings.Builder

	writeFrontmatter(&sb, pd)
	sb.WriteString("\n\n")

	if pd.Text != "" {
		sb.WriteString(pd.Text)
		sb.WriteString("\n\n")
	}

	for _, m := range pd.Media {
		switch m.Type {
		case "photo":
			fmt.Fprintf(&sb, "![photo](%s)\n\n", m.File)
		case "video":
			fmt.Fprintf(&sb, "[📹 Видео](%s)\n\n", m.File)
		case "audio":
			title := m.FileName
			if title == "" {
				title = "Аудио"
			}
			fmt.Fprintf(&sb, "[🎵 %s](%s)\n\n", title, m.File)
		case "voice_note":
			fmt.Fprintf(&sb, "[🎙 Голосовое сообщение](%s)\n\n", m.File)
		case "document":
			name := m.FileName
			if name == "" {
				name = m.File
			}
			fmt.Fprintf(&sb, "[📎 %s](%s)\n\n", name, m.File)
		case "animation":
			fmt.Fprintf(&sb, "[🎬 Анимация](%s)\n\n", m.File)
		}
	}

	return []byte(strings.TrimRight(sb.String(), "\n") + "\n")
}

func writeFrontmatter(sb *strings.Builder, pd PostData) {
	sb.WriteString("---\n")
	fmt.Fprintf(sb, "id: %d\n", pd.ID)
	fmt.Fprintf(sb, "date: \"%s\"\n", unixToISO(pd.Date))
	fmt.Fprintf(sb, "author: %s\n", yamlString(pd.AuthorName))
	if pd.TelegramURL != "" {
		fmt.Fprintf(sb, "telegram_url: %s\n", pd.TelegramURL)
	}

	if pd.EditDate > 0 {
		fmt.Fprintf(sb, "edited: \"%s\"\n", unixToISO(pd.EditDate))
	}
	if pd.ReplyToID != 0 {
		fmt.Fprintf(sb, "reply_to: %d\n", pd.ReplyToID)
	}
	if pd.MediaAlbumID != 0 {
		fmt.Fprintf(sb, "album_id: %d\n", pd.MediaAlbumID)
	}

	fmt.Fprintf(sb, "views: %d\n", pd.ViewCount)
	fmt.Fprintf(sb, "forwards: %d\n", pd.ForwardCount)
	fmt.Fprintf(sb, "comments: %d\n", pd.CommentsCount)

	if len(pd.Media) > 0 {
		sb.WriteString("has_media: true\n")
		sb.WriteString("media:\n")
		for _, m := range pd.Media {
			fmt.Fprintf(sb, "  - type: %s\n", m.Type)
			fmt.Fprintf(sb, "    file: %s\n", yamlString(m.File))
			if m.Duration > 0 {
				fmt.Fprintf(sb, "    duration: %d\n", m.Duration)
			}
			if m.Width > 0 {
				fmt.Fprintf(sb, "    width: %d\n", m.Width)
				fmt.Fprintf(sb, "    height: %d\n", m.Height)
			}
			if m.MimeType != "" {
				fmt.Fprintf(sb, "    mime_type: %s\n", yamlString(m.MimeType))
			}
			if m.FileName != "" && m.Type == "document" {
				fmt.Fprintf(sb, "    original_name: %s\n", yamlString(m.FileName))
			}
		}
	}

	if len(pd.Reactions) > 0 {
		sb.WriteString("reactions:\n")
		keys := make([]string, 0, len(pd.Reactions))
		for k := range pd.Reactions {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(sb, "  %s: %d\n", yamlString(k), pd.Reactions[k])
		}
	}

	if pd.Poll != nil {
		p := pd.Poll
		sb.WriteString("poll:\n")
		fmt.Fprintf(sb, "  question: %s\n", yamlString(p.Question))
		fmt.Fprintf(sb, "  type: %s\n", p.Type)
		fmt.Fprintf(sb, "  is_anonymous: %v\n", p.Anonymous)
		fmt.Fprintf(sb, "  is_closed: %v\n", p.Closed)
		sb.WriteString("  options:\n")
		for _, opt := range p.Options {
			fmt.Fprintf(sb, "    - text: %s\n", yamlString(opt.Text))
			fmt.Fprintf(sb, "      votes: %d\n", opt.Votes)
		}
	}

	sb.WriteString("---")
}

// ─── FormattedText → Markdown ──────────────────────────────────────────────

// FormattedTextToMarkdown converts a TDLib FormattedText (UTF-16 entity
// offsets) to a Markdown string.
func FormattedTextToMarkdown(ft *client.FormattedText) string {
	if ft == nil || ft.Text == "" {
		return ""
	}
	if len(ft.Entities) == 0 {
		return ft.Text
	}

	utf16Units := utf16.Encode([]rune(ft.Text))

	entities := make([]*client.TextEntity, len(ft.Entities))
	copy(entities, ft.Entities)
	sort.Slice(entities, func(i, j int) bool {
		if entities[i].Offset != entities[j].Offset {
			return entities[i].Offset < entities[j].Offset
		}
		return entities[i].Length > entities[j].Length
	})

	return applyEntities(utf16Units, 0, int32(len(utf16Units)), entities)
}

// applyEntities recursively applies entities within [start, end).
func applyEntities(text []uint16, start, end int32, entities []*client.TextEntity) string {
	var applicable []*client.TextEntity
	for _, e := range entities {
		if e.Offset >= start && e.Offset+e.Length <= end {
			applicable = append(applicable, e)
		}
	}

	if len(applicable) == 0 {
		return utf16Slice(text, start, end)
	}

	var sb strings.Builder
	pos := start

	for len(applicable) > 0 {
		e := applicable[0]
		applicable = applicable[1:]

		sb.WriteString(utf16Slice(text, pos, e.Offset))

		entityEnd := e.Offset + e.Length

		var nested []*client.TextEntity
		var remaining []*client.TextEntity
		for _, ie := range applicable {
			if ie.Offset >= e.Offset && ie.Offset+ie.Length <= entityEnd {
				nested = append(nested, ie)
			} else {
				remaining = append(remaining, ie)
			}
		}
		applicable = remaining

		innerText := applyEntities(text, e.Offset, entityEnd, nested)
		sb.WriteString(wrapEntity(e.Type, innerText))

		pos = entityEnd
	}

	sb.WriteString(utf16Slice(text, pos, end))
	return sb.String()
}

// wrapEntity wraps inner text with the Markdown syntax for an entity type.
func wrapEntity(t client.TextEntityType, inner string) string {
	switch et := t.(type) {
	case *client.TextEntityTypeBold:
		return "**" + inner + "**"
	case *client.TextEntityTypeItalic:
		return "*" + inner + "*"
	case *client.TextEntityTypeUnderline:
		return "<u>" + inner + "</u>"
	case *client.TextEntityTypeStrikethrough:
		return "~~" + inner + "~~"
	case *client.TextEntityTypeSpoiler:
		return "||" + inner + "||"
	case *client.TextEntityTypeCode:
		return "`" + inner + "`"
	case *client.TextEntityTypePre:
		return "\n```\n" + inner + "\n```\n"
	case *client.TextEntityTypePreCode:
		return "\n```" + et.Language + "\n" + inner + "\n```\n"
	case *client.TextEntityTypeBlockQuote:
		lines := strings.Split(inner, "\n")
		for i, l := range lines {
			lines[i] = "> " + l
		}
		return strings.Join(lines, "\n")
	case *client.TextEntityTypeTextUrl:
		return "[" + inner + "](" + et.Url + ")"
	case *client.TextEntityTypeUrl:
		return inner
	case *client.TextEntityTypeMention:
		return inner
	case *client.TextEntityTypeMentionName:
		return inner
	case *client.TextEntityTypeHashtag:
		return inner
	case *client.TextEntityTypeCashtag:
		return inner
	case *client.TextEntityTypeBotCommand:
		return "`" + inner + "`"
	case *client.TextEntityTypeEmailAddress:
		return "[" + inner + "](mailto:" + inner + ")"
	case *client.TextEntityTypePhoneNumber:
		return "[" + inner + "](tel:" + inner + ")"
	default:
		return inner
	}
}

func utf16Slice(text []uint16, start, end int32) string {
	if start >= end || int(start) >= len(text) {
		return ""
	}
	if int(end) > len(text) {
		end = int32(len(text))
	}
	return string(utf16.Decode(text[start:end]))
}

// ─── Poll ──────────────────────────────────────────────────────────────────

func convertPoll(p *client.Poll) *PollData {
	if p == nil {
		return nil
	}

	pd := &PollData{
		Question:  FormattedTextToMarkdown(p.Question),
		Anonymous: p.IsAnonymous,
		Closed:    p.IsClosed,
	}

	switch p.Type.(type) {
	case *client.PollTypeQuiz:
		pd.Type = "quiz"
	default:
		pd.Type = "regular"
	}

	for _, opt := range p.Options {
		pd.Options = append(pd.Options, PollOption{
			Text:  FormattedTextToMarkdown(opt.Text),
			Votes: opt.VoterCount,
		})
	}

	return pd
}

// ─── Reactions ─────────────────────────────────────────────────────────────

func extractReactions(mr *client.MessageReactions) map[string]int32 {
	if mr == nil || len(mr.Reactions) == 0 {
		return nil
	}
	result := make(map[string]int32, len(mr.Reactions))
	for _, r := range mr.Reactions {
		key := reactionKey(r.Type)
		result[key] += r.TotalCount
	}
	return result
}

func reactionKey(rt client.ReactionType) string {
	switch r := rt.(type) {
	case *client.ReactionTypeEmoji:
		return r.Emoji
	case *client.ReactionTypePaid:
		return "⭐paid"
	default:
		return "custom"
	}
}

// ─── YAML helpers ──────────────────────────────────────────────────────────

func yamlString(s string) string {
	needsQuote := strings.ContainsAny(s, `:"'{}[]|>&*!,%@`+"`"+"\n\r\t")
	if needsQuote {
		esc := strings.ReplaceAll(s, `\`, `\\`)
		esc = strings.ReplaceAll(esc, `"`, `\"`)
		esc = strings.ReplaceAll(esc, "\n", `\n`)
		return `"` + esc + `"`
	}
	return s
}

func unixToISO(ts int32) string {
	return time.Unix(int64(ts), 0).UTC().Format(time.RFC3339)
}
