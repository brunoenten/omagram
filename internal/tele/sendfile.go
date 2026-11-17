package tele

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/gotd/td/telegram/uploader"
	"github.com/gotd/td/tg"
)

const maxUploadBytes = 100 << 20 // 100 MiB

// SendFile uploads path and sends it to k. Images (jpeg/png/webp) go as
// photos; everything else as a document. caption is optional; replyTo quotes
// an existing message when non-zero.
func (c *Client) SendFile(k PeerKey, path, caption string, replyTo int) {
	input, api, ok := c.inputPeer(k)
	if !ok {
		c.Store.SetStatus("send: not connected")
		return
	}
	caption = strings.TrimSpace(caption)
	name := filepath.Base(path)
	c.Store.SetStatus("uploading " + name + "…")

	go func() {
		if err := checkUpload(path); err != nil {
			c.Store.SetStatus("send: " + err.Error())
			return
		}
		mime, err := sniffMIME(path)
		if err != nil {
			c.Store.SetStatus("send: " + err.Error())
			return
		}
		file, err := uploader.NewUploader(api).FromPath(c.ctx, path)
		if err != nil {
			c.Store.SetStatus("send: " + err.Error())
			return
		}

		var media tg.InputMediaClass
		switch mime {
		case "image/jpeg", "image/png", "image/webp":
			media = &tg.InputMediaUploadedPhoto{File: file}
		default:
			if mime == "application/octet-stream" {
				if ext := strings.ToLower(filepath.Ext(name)); ext != "" {
					mime = mimeFromExt(ext)
				}
			}
			media = &tg.InputMediaUploadedDocument{
				File:     file,
				MimeType: mime,
				Attributes: []tg.DocumentAttributeClass{
					&tg.DocumentAttributeFilename{FileName: name},
				},
			}
		}

		req := &tg.MessagesSendMediaRequest{
			Peer:     input,
			Media:    media,
			Message:  caption,
			RandomID: randomID(),
		}
		if replyTo != 0 {
			req.ReplyTo = &tg.InputReplyToMessage{ReplyToMsgID: replyTo}
		}
		if _, err := api.MessagesSendMedia(c.ctx, req); err != nil {
			c.Store.SetStatus("send: " + err.Error())
			return
		}
		c.Store.SetStatus("sent " + name)
	}()
}

func checkUpload(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("is a directory")
	}
	if st.Size() > maxUploadBytes {
		return fmt.Errorf("file too large (max %d MiB)", maxUploadBytes>>20)
	}
	return nil
}

func sniffMIME(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	buf := make([]byte, 512)
	n, err := f.Read(buf)
	if err != nil && n == 0 {
		return "", err
	}
	return http.DetectContentType(buf[:n]), nil
}

func mimeFromExt(ext string) string {
	switch ext {
	case ".pdf":
		return "application/pdf"
	case ".zip":
		return "application/zip"
	case ".txt":
		return "text/plain"
	case ".csv":
		return "text/csv"
	case ".json":
		return "application/json"
	case ".mp4":
		return "video/mp4"
	case ".mp3":
		return "audio/mpeg"
	case ".ogg":
		return "audio/ogg"
	case ".opus":
		return "audio/ogg"
	case ".doc":
		return "application/msword"
	case ".docx":
		return "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	case ".xls":
		return "application/vnd.ms-excel"
	case ".xlsx":
		return "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet"
	case ".ppt":
		return "application/vnd.ms-powerpoint"
	case ".pptx":
		return "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	}
	return "application/octet-stream"
}
