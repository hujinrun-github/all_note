package handler

import (
	"database/sql"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/model"
	"github.com/hujinrun/flowspace/internal/objectstore"
	"github.com/hujinrun/flowspace/internal/service"
	"github.com/hujinrun/flowspace/internal/storage"
)

func ListNoteAttachments(store storage.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		attachments, err := service.ListNoteAttachments(c.Request.Context(), store, c.Param("id"))
		if err != nil {
			handleNoteAttachmentError(c, err)
			return
		}
		success(c, gin.H{"attachments": attachments})
	}
}

func UploadNoteAttachment(store storage.Store, objects objectstore.Store, maxBytes int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		originalName, err := url.QueryUnescape(strings.TrimSpace(c.GetHeader("X-File-Name")))
		if err != nil {
			badRequest(c, "invalid attachment file name")
			return
		}
		attachment, err := service.UploadNoteAttachment(
			c.Request.Context(),
			store,
			objects,
			c.Param("id"),
			originalName,
			c.GetHeader("Content-Type"),
			c.Request.Body,
			c.Request.ContentLength,
			maxBytes,
		)
		if err != nil {
			handleNoteAttachmentError(c, err)
			return
		}
		created(c, gin.H{"attachment": attachment})
	}
}

func GetNoteAttachmentContent(store storage.Store, objects objectstore.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		attachment, object, err := service.GetNoteAttachment(
			c.Request.Context(), store, objects, c.Param("id"), c.Param("attachmentID"),
		)
		if err != nil {
			handleNoteAttachmentError(c, err)
			return
		}
		defer object.Body.Close()

		contentType := strings.TrimSpace(object.ContentType)
		if contentType == "" {
			contentType = attachment.MimeType
		}
		disposition := "inline"
		if c.Query("download") == "1" || attachment.Kind == model.NoteAttachmentKindFile {
			disposition = "attachment"
		}
		headers := map[string]string{
			"Accept-Ranges":          "bytes",
			"Cache-Control":          "private, max-age=3600",
			"Content-Disposition":    mime.FormatMediaType(disposition, map[string]string{"filename": attachment.OriginalName}),
			"ETag":                   object.ETag,
			"X-Content-Type-Options": "nosniff",
		}
		if c.Request.Method == http.MethodHead {
			for key, value := range headers {
				c.Header(key, value)
			}
			c.Header("Content-Type", contentType)
			c.Header("Content-Length", strconv.FormatInt(object.Size, 10))
			c.Status(http.StatusOK)
			return
		}
		start, end, partial, rangeErr := parseAttachmentRange(c.GetHeader("Range"), object.Size)
		if rangeErr != nil {
			c.Header("Content-Range", fmt.Sprintf("bytes */%d", object.Size))
			c.Status(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		if !partial {
			c.DataFromReader(http.StatusOK, object.Size, contentType, object.Body, headers)
			return
		}
		if start > 0 {
			if _, err := io.CopyN(io.Discard, object.Body, start); err != nil {
				internalError(c, "failed to read attachment")
				return
			}
		}
		length := end - start + 1
		headers["Content-Range"] = fmt.Sprintf("bytes %d-%d/%d", start, end, object.Size)
		c.DataFromReader(
			http.StatusPartialContent,
			length,
			contentType,
			io.LimitReader(object.Body, length),
			headers,
		)
	}
}

func DeleteNoteAttachment(store storage.Store, objects objectstore.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := service.DeleteNoteAttachment(
			c.Request.Context(), store, objects, c.Param("id"), c.Param("attachmentID"),
		); err != nil {
			handleNoteAttachmentError(c, err)
			return
		}
		noContent(c)
	}
}

func handleNoteAttachmentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, sql.ErrNoRows), errors.Is(err, objectstore.ErrNotFound):
		notFound(c, "note or attachment not found")
	case errors.Is(err, service.ErrAttachmentInvalidMetadata):
		badRequest(c, err.Error())
	case errors.Is(err, service.ErrAttachmentTooLarge):
		errorResponse(c, http.StatusRequestEntityTooLarge, "ATTACHMENT_TOO_LARGE", err.Error())
	case errors.Is(err, service.ErrAttachmentLimitReached):
		conflict(c, "ATTACHMENT_LIMIT_REACHED", err.Error())
	case errors.Is(err, service.ErrAttachmentReadOnly):
		conflict(c, "ATTACHMENT_READ_ONLY", err.Error())
	case errors.Is(err, service.ErrAttachmentStorage),
		errors.Is(err, storage.ErrNoteAttachmentStoreUnavailable),
		errors.Is(err, objectstore.ErrUnavailable):
		errorResponse(c, http.StatusServiceUnavailable, "ATTACHMENT_STORAGE_UNAVAILABLE", "attachment storage is unavailable")
	default:
		internalError(c, "attachment operation failed")
	}
}

func parseAttachmentRange(value string, size int64) (start, end int64, partial bool, err error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, size - 1, false, nil
	}
	if size <= 0 || !strings.HasPrefix(value, "bytes=") || strings.Contains(value, ",") {
		return 0, 0, false, errors.New("invalid range")
	}
	parts := strings.SplitN(strings.TrimPrefix(value, "bytes="), "-", 2)
	if len(parts) != 2 {
		return 0, 0, false, errors.New("invalid range")
	}
	if parts[0] == "" {
		suffix, parseErr := strconv.ParseInt(parts[1], 10, 64)
		if parseErr != nil || suffix <= 0 {
			return 0, 0, false, errors.New("invalid range")
		}
		if suffix > size {
			suffix = size
		}
		return size - suffix, size - 1, true, nil
	}
	start, err = strconv.ParseInt(parts[0], 10, 64)
	if err != nil || start < 0 || start >= size {
		return 0, 0, false, errors.New("invalid range")
	}
	end = size - 1
	if parts[1] != "" {
		end, err = strconv.ParseInt(parts[1], 10, 64)
		if err != nil || end < start {
			return 0, 0, false, errors.New("invalid range")
		}
		if end >= size {
			end = size - 1
		}
	}
	return start, end, true, nil
}
