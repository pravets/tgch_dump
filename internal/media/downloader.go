// Package media provides a concurrent worker pool for downloading TDLib files
// to a local output directory.
package media

import (
	"fmt"
	"io"
	"log/slog"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/pravets/tgch_dump/internal/converter"
	"github.com/zelenin/go-tdlib/client"
)

// Downloader downloads TDLib files using a bounded worker pool.
type Downloader struct {
	tdClient  *client.Client
	semaphore chan struct{}
}

// New creates a Downloader with at most maxConcurrent simultaneous downloads.
func New(tdClient *client.Client, maxConcurrent int) *Downloader {
	if maxConcurrent <= 0 {
		maxConcurrent = 3
	}
	return &Downloader{
		tdClient:  tdClient,
		semaphore: make(chan struct{}, maxConcurrent),
	}
}

// DownloadRequest describes a single file to download.
type DownloadRequest struct {
	File      *client.File
	MediaType string
	MsgID     int64
	Index     int
	OrigName  string
	MimeType  string
	Duration  int32
	Width     int32
	Height    int32
}

// DownloadResult holds the result of a single file download.
type DownloadResult struct {
	Ref converter.MediaRef
	Err error
}

// DownloadAll downloads all files concurrently and returns results in input order.
func (d *Downloader) DownloadAll(outDir string, requests []DownloadRequest) []DownloadResult {
	results := make([]DownloadResult, len(requests))
	var wg sync.WaitGroup

	for i, req := range requests {
		wg.Add(1)
		go func(idx int, r DownloadRequest) {
			defer wg.Done()
			ref, err := d.download(outDir, r)
			results[idx] = DownloadResult{Ref: ref, Err: err}
		}(i, req)
	}

	wg.Wait()
	return results
}

// download downloads a single file and copies it into outDir.
func (d *Downloader) download(outDir string, req DownloadRequest) (converter.MediaRef, error) {
	d.semaphore <- struct{}{}
	defer func() { <-d.semaphore }()

	downloadedFile, err := d.tdClient.DownloadFile(&client.DownloadFileRequest{
		FileId:      req.File.Id,
		Priority:    32,
		Offset:      0,
		Limit:       0,
		Synchronous: true,
	})
	if err != nil {
		return converter.MediaRef{}, fmt.Errorf("download file %d: %w", req.File.Id, err)
	}

	if downloadedFile.Local == nil || downloadedFile.Local.Path == "" {
		return converter.MediaRef{}, fmt.Errorf("file %d: local path empty after download", req.File.Id)
	}

	srcPath := downloadedFile.Local.Path
	ext := fileExtension(srcPath, req.MimeType, req.OrigName)
	destName := fmt.Sprintf("%08d_%s_%d%s", req.MsgID, req.MediaType, req.Index, ext)
	destPath := filepath.Join(outDir, destName)

	if err := copyFile(srcPath, destPath); err != nil {
		return converter.MediaRef{}, fmt.Errorf("copy file to %s: %w", destPath, err)
	}

	slog.Debug("downloaded media", "file", destName, "src", srcPath)

	return converter.MediaRef{
		Type:     req.MediaType,
		File:     destName,
		Duration: req.Duration,
		Width:    req.Width,
		Height:   req.Height,
		MimeType: req.MimeType,
		FileName: req.OrigName,
	}, nil
}

func fileExtension(srcPath, mimeType, origName string) string {
	if ext := filepath.Ext(srcPath); ext != "" {
		return ext
	}
	if mimeType != "" {
		exts, _ := mime.ExtensionsByType(mimeType)
		if len(exts) > 0 {
			return preferredExt(exts)
		}
	}
	if origName != "" {
		if ext := filepath.Ext(origName); ext != "" {
			return ext
		}
	}
	return ""
}

func preferredExt(exts []string) string {
	prefer := map[string]int{
		".jpg": 10, ".mp4": 10, ".mp3": 10, ".ogg": 10, ".pdf": 10,
		".png": 9, ".webp": 8, ".jpeg": 7,
	}
	best, bestScore := exts[0], -1
	for _, e := range exts {
		if s, ok := prefer[e]; ok && s > bestScore {
			best, bestScore = e, s
		}
	}
	return best
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 — path from TDLib local filesystem
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err = io.Copy(out, in); err != nil {
		return err
	}
	return out.Sync()
}

// BuildRequests creates download requests from a TDLib message.
func BuildRequests(msg *client.Message, enabledTypes map[string]bool) []DownloadRequest {
	var reqs []DownloadRequest
	idx := 1

	addReq := func(f *client.File, mediaType, mime, origName string, dur, w, h int32) {
		if f == nil || !enabledTypes[mediaType] {
			return
		}
		reqs = append(reqs, DownloadRequest{
			File:      f,
			MediaType: mediaType,
			MsgID:     msg.Id,
			Index:     idx,
			OrigName:  origName,
			MimeType:  mime,
			Duration:  dur,
			Width:     w,
			Height:    h,
		})
		idx++
	}

	switch c := msg.Content.(type) {
	case *client.MessagePhoto:
		if c.Photo != nil && len(c.Photo.Sizes) > 0 {
			largest := c.Photo.Sizes[len(c.Photo.Sizes)-1]
			addReq(largest.Photo, "photo", "image/jpeg", "", 0, 0, 0)
		}
	case *client.MessageVideo:
		if c.Video != nil {
			addReq(c.Video.Video, "video", c.Video.MimeType, c.Video.FileName,
				c.Video.Duration, c.Video.Width, c.Video.Height)
		}
	case *client.MessageAudio:
		if c.Audio != nil {
			name := c.Audio.FileName
			if name == "" {
				name = strings.TrimSpace(c.Audio.Performer + " - " + c.Audio.Title)
			}
			addReq(c.Audio.Audio, "audio", c.Audio.MimeType, name, c.Audio.Duration, 0, 0)
		}
	case *client.MessageVoiceNote:
		if c.VoiceNote != nil {
			addReq(c.VoiceNote.Voice, "voice_note", c.VoiceNote.MimeType, "", c.VoiceNote.Duration, 0, 0)
		}
	case *client.MessageDocument:
		if c.Document != nil {
			addReq(c.Document.Document, "document", c.Document.MimeType, c.Document.FileName, 0, 0, 0)
		}
	case *client.MessageAnimation:
		if c.Animation != nil {
			addReq(c.Animation.Animation, "animation", c.Animation.MimeType, c.Animation.FileName,
				c.Animation.Duration, c.Animation.Width, c.Animation.Height)
		}
	}

	return reqs
}
