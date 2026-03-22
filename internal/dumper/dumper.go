// Package dumper orchestrates fetching messages from a Telegram channel and
// writing them to the output directory as Markdown files.
package dumper

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/pravets/tgch_dump/internal/config"
	"github.com/pravets/tgch_dump/internal/converter"
	"github.com/pravets/tgch_dump/internal/media"
	"github.com/pravets/tgch_dump/internal/state"
	"github.com/zelenin/go-tdlib/client"
)

// Dumper dumps one or more Telegram channels.
type Dumper struct {
	cfg          *config.Config
	tdClient     *client.Client
	downloader   *media.Downloader
	enabledMedia map[string]bool
}

// New creates a Dumper.
func New(cfg *config.Config, tdClient *client.Client) *Dumper {
	enabled := make(map[string]bool, len(cfg.Output.MediaTypes))
	if cfg.Output.DownloadMedia {
		for _, t := range cfg.Output.MediaTypes {
			enabled[strings.ToLower(t)] = true
		}
	}
	return &Dumper{
		cfg:          cfg,
		tdClient:     tdClient,
		downloader:   media.New(tdClient, cfg.Dump.MaxConcurrentDownloads),
		enabledMedia: enabled,
	}
}

// DumpAll dumps every channel listed in the config, in sequence.
func (d *Dumper) DumpAll(ctx context.Context) error {
	for _, ch := range d.cfg.Channels {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := d.DumpChannel(ctx, ch); err != nil {
			slog.Error("failed to dump channel", "channel", ch, "err", err)
		}
	}
	return nil
}

// DumpChannel resolves the given channel identifier and dumps its content.
func (d *Dumper) DumpChannel(ctx context.Context, channelID string) error {
	chat, err := d.resolveChannel(channelID)
	if err != nil {
		return fmt.Errorf("resolve channel %q: %w", channelID, err)
	}

	slog.Info("dumping channel", "title", chat.Title, "id", chat.Id)

	urlBase := channelURLBase(channelID)

	outDir := filepath.Join(d.cfg.Output.Dir, sanitizeName(chat.Title))
	if err := os.MkdirAll(outDir, 0750); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	st, err := state.Load(outDir)
	if err != nil {
		return fmt.Errorf("load state: %w", err)
	}

	var stopAtID int64
	if d.cfg.Dump.Incremental {
		stopAtID = st.LastMessageID
	}

	processed, maxID, err := d.fetchMessages(ctx, chat, outDir, stopAtID, urlBase)
	if err != nil {
		return err
	}

	if maxID > st.LastMessageID {
		st.ChannelID = chat.Id
		st.ChannelName = chat.Title
		st.LastMessageID = maxID
		st.LastDumpTime = time.Now().UTC()
		st.TotalMessagesDumped += int64(processed)
		if saveErr := state.Save(outDir, st); saveErr != nil {
			slog.Warn("failed to save state", "err", saveErr)
		}
	}

	slog.Info("channel dump complete",
		"channel", chat.Title,
		"new_messages", processed,
		"total", st.TotalMessagesDumped,
	)
	return nil
}

// albumBuffer accumulates consecutive messages that share a MediaAlbumId.
type albumBuffer struct {
	albumID  int64
	messages []*client.Message
}

// fetchMessages iterates through the channel history from newest to oldest.
func (d *Dumper) fetchMessages(
	ctx context.Context,
	chat *client.Chat,
	outDir string,
	stopAtID int64,
	urlBase string,
) (processed int, maxID int64, err error) {
	var fromMessageID int64
	delay := time.Duration(d.cfg.Dump.RequestDelayMs) * time.Millisecond

	// pending holds an in-progress album that may span batch boundaries.
	var pending *albumBuffer

	flushPending := func() {
		if pending == nil {
			return
		}
		if procErr := d.processAlbum(ctx, pending.messages, chat.Title, outDir, urlBase); procErr != nil {
			slog.Warn("skipping album", "album_id", pending.albumID, "err", procErr)
		} else {
			processed++
		}
		pending = nil
	}

	for {
		if err = ctx.Err(); err != nil {
			flushPending()
			return
		}

		resp, fetchErr := d.tdClient.GetChatHistory(&client.GetChatHistoryRequest{
			ChatId:        chat.Id,
			FromMessageId: fromMessageID,
			Offset:        0,
			Limit:         d.cfg.Dump.MessagesPerBatch,
			OnlyLocal:     false,
		})
		if fetchErr != nil {
			flushPending()
			err = fmt.Errorf("get chat history (from=%d): %w", fromMessageID, fetchErr)
			return
		}

		if len(resp.Messages) == 0 {
			break
		}

		done := false
		for _, msg := range resp.Messages {
			if stopAtID > 0 && msg.Id <= stopAtID {
				flushPending()
				done = true
				break
			}
			if msg.Id > maxID {
				maxID = msg.Id
			}
			fromMessageID = msg.Id

			albumID := int64(msg.MediaAlbumId)
			if albumID == 0 {
				// Regular message — flush any buffered album first.
				flushPending()
				if procErr := d.processMessage(ctx, msg, chat.Title, outDir, urlBase); procErr != nil {
					slog.Warn("skipping message", "id", msg.Id, "err", procErr)
				} else {
					processed++
				}
			} else {
				// Album message — accumulate into buffer.
				if pending != nil && pending.albumID != albumID {
					// Different album started — flush previous.
					flushPending()
				}
				if pending == nil {
					pending = &albumBuffer{albumID: albumID}
				}
				pending.messages = append(pending.messages, msg)
			}
		}

		if done {
			break
		}

		// Do NOT flush pending here — the album may continue in the next batch.

		slog.Debug("batch done",
			"channel", chat.Title,
			"batch_size", len(resp.Messages),
			"processed_total", processed,
			"from_next", fromMessageID,
		)

		time.Sleep(delay)
	}

	// Flush any album that ended with the last batch.
	flushPending()

	return
}

// processAlbum merges all messages of a media album into a single Markdown file.
func (d *Dumper) processAlbum(ctx context.Context, msgs []*client.Message, chatTitle, outDir, urlBase string) error {
	if len(msgs) == 0 {
		return nil
	}
	if len(msgs) == 1 {
		return d.processMessage(ctx, msgs[0], chatTitle, outDir, urlBase)
	}

	// Collect media requests from every message in the album.
	var allReqs []media.DownloadRequest
	for _, msg := range msgs {
		reqs := media.BuildRequests(msg, d.enabledMedia)
		allReqs = append(allReqs, reqs...)
	}

	var mediaRefs []converter.MediaRef
	if len(allReqs) > 0 {
		attachmentsDir := filepath.Join(outDir, "attachments")
		if err := os.MkdirAll(attachmentsDir, 0750); err != nil {
			return fmt.Errorf("create attachments dir: %w", err)
		}
		results := d.downloader.DownloadAll(attachmentsDir, allReqs)
		for _, r := range results {
			if r.Err != nil {
				slog.Warn("media download failed", "album_id", msgs[0].MediaAlbumId, "err", r.Err)
				continue
			}
			r.Ref.File = "attachments/" + r.Ref.File
			mediaRefs = append(mediaRefs, r.Ref)
		}
	}

	// msgs are ordered newest→oldest; the oldest (last element) is the first sent
	// and typically carries the caption.
	primary := msgs[len(msgs)-1]

	pd := converter.ConvertMessage(primary, chatTitle, mediaRefs)
	if urlBase != "" {
		pd.TelegramURL = fmt.Sprintf("%s/%d", urlBase, primary.Id>>20)
	}

	// If oldest message has no caption, search the rest for one.
	if pd.Text == "" {
		for i := len(msgs) - 2; i >= 0; i-- {
			candidate := converter.ConvertMessage(msgs[i], chatTitle, nil)
			if candidate.Text != "" {
				pd.Text = candidate.Text
				break
			}
		}
	}

	mdBytes := converter.Render(pd)

	msgDate := time.Unix(int64(primary.Date), 0).UTC()
	mdPath := filepath.Join(outDir, fmt.Sprintf("%s_%08d.md", msgDate.Format("2006-01-02"), primary.Id))
	if err := os.WriteFile(mdPath, mdBytes, 0640); err != nil {
		return fmt.Errorf("write %s: %w", mdPath, err)
	}

	return nil
}

// processMessage converts and writes a single message to disk.
func (d *Dumper) processMessage(ctx context.Context, msg *client.Message, chatTitle, outDir, urlBase string) error {
	mediaReqs := media.BuildRequests(msg, d.enabledMedia)
	var mediaRefs []converter.MediaRef

	if len(mediaReqs) > 0 {
		attachmentsDir := filepath.Join(outDir, "attachments")
		if err := os.MkdirAll(attachmentsDir, 0750); err != nil {
			return fmt.Errorf("create attachments dir: %w", err)
		}
		results := d.downloader.DownloadAll(attachmentsDir, mediaReqs)
		for _, r := range results {
			if r.Err != nil {
				slog.Warn("media download failed", "msg_id", msg.Id, "err", r.Err)
				continue
			}
			r.Ref.File = "attachments/" + r.Ref.File
			mediaRefs = append(mediaRefs, r.Ref)
		}
	}

	pd := converter.ConvertMessage(msg, chatTitle, mediaRefs)
	if urlBase != "" {
		pd.TelegramURL = fmt.Sprintf("%s/%d", urlBase, msg.Id>>20)
	}
	mdBytes := converter.Render(pd)

	msgDate := time.Unix(int64(msg.Date), 0).UTC()
	mdPath := filepath.Join(outDir, fmt.Sprintf("%s_%08d.md", msgDate.Format("2006-01-02"), msg.Id))
	if err := os.WriteFile(mdPath, mdBytes, 0640); err != nil {
		return fmt.Errorf("write %s: %w", mdPath, err)
	}

	return nil
}

// resolveChannel turns "@username" or numeric ID strings into a *client.Chat.
func (d *Dumper) resolveChannel(channelID string) (*client.Chat, error) {
	if isNumericID(channelID) {
		id, _ := strconv.ParseInt(channelID, 10, 64)
		return d.tdClient.GetChat(&client.GetChatRequest{ChatId: id})
	}
	username := strings.TrimPrefix(channelID, "@")
	return d.tdClient.SearchPublicChat(&client.SearchPublicChatRequest{
		Username: username,
	})
}

// ─── Helpers ───────────────────────────────────────────────────────────────

var (
	reUnsafe     = regexp.MustCompile(`[\\/:*?"<>|]`)
	reWhitespace = regexp.MustCompile(`\s+`)
)

func sanitizeName(title string) string {
	s := reUnsafe.ReplaceAllString(title, "_")
	s = reWhitespace.ReplaceAllString(s, "_")
	s = strings.Trim(s, "_.")
	if s == "" {
		return "channel"
	}
	return s
}

func isNumericID(s string) bool {
	s = strings.TrimPrefix(s, "-")
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// channelURLBase returns the t.me base URL for a channel identifier.
// Public  (@username)      → https://t.me/username
// Private (-1001234567890) → https://t.me/c/1234567890
func channelURLBase(channelID string) string {
	if strings.HasPrefix(channelID, "@") {
		return "https://t.me/" + channelID[1:]
	}
	// Supergroup/channel numeric IDs have the -100 prefix in TDLib.
	id := strings.TrimPrefix(channelID, "-100")
	id = strings.TrimPrefix(id, "-")
	if id != "" {
		return "https://t.me/c/" + id
	}
	return ""
}
