package handler

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/auth"
)

const maxMobileV2CommandBytes int64 = 1024 * 1024

// MobileV2Identity is the authenticated, workspace-scoped caller forwarded to
// the protocol service. A watch token has DeviceID set and SessionID empty.
type MobileV2Identity struct {
	WorkspaceID string
	UserID      string
	SessionID   string
	DeviceID    string
}

type MobileV2SnapshotRequest struct {
	Identity           MobileV2Identity
	Scope              string
	PageToken          string
	ProjectionTimeZone string
}

type MobileV2ChangesRequest struct {
	Identity           MobileV2Identity
	Scope              string
	Cursor             string
	ProjectionTimeZone string
}

type MobileV2CommandRequest struct {
	Identity    MobileV2Identity
	RawEnvelope json.RawMessage
}

type MobileV2ReceiptRequest struct {
	Identity             MobileV2Identity
	OriginDeviceClientID string
	CommandID            string
}

// MobileV2Service is the transport boundary for the frozen mobile-v2
// protocol. Implementations own snapshot consistency, receipt-first command
// execution and durable cursors; HTTP handlers deliberately do not duplicate
// those rules.
type MobileV2Service interface {
	Capabilities(context.Context, MobileV2Identity) (any, error)
	Snapshot(context.Context, MobileV2SnapshotRequest) (any, error)
	Changes(context.Context, MobileV2ChangesRequest) (any, error)
	ApplyCommand(context.Context, MobileV2CommandRequest) (any, error)
	Receipt(context.Context, MobileV2ReceiptRequest) (any, error)
}

// MobileV2ProtocolError is safe to expose on the frozen wire contract.
type MobileV2ProtocolError struct {
	Status  int
	Code    string
	Message string
}

func (e *MobileV2ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	return e.Code + ": " + e.Message
}

func GetMobileV2Capabilities(service MobileV2Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := mobileV2Identity(c)
		if !ok {
			return
		}
		result, err := service.Capabilities(c.Request.Context(), identity)
		writeMobileV2Result(c, result, err)
	}
}

func GetMobileV2Snapshot(service MobileV2Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := mobileV2Identity(c)
		if !ok {
			return
		}
		result, err := service.Snapshot(c.Request.Context(), MobileV2SnapshotRequest{
			Identity:           identity,
			Scope:              strings.TrimSpace(c.Query("scope")),
			PageToken:          strings.TrimSpace(c.Query("page_token")),
			ProjectionTimeZone: strings.TrimSpace(c.Query("projection_time_zone")),
		})
		writeMobileV2Result(c, result, err)
	}
}

func ListMobileV2Changes(service MobileV2Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := mobileV2Identity(c)
		if !ok {
			return
		}
		result, err := service.Changes(c.Request.Context(), MobileV2ChangesRequest{
			Identity:           identity,
			Scope:              strings.TrimSpace(c.Query("scope")),
			Cursor:             strings.TrimSpace(c.Query("cursor")),
			ProjectionTimeZone: strings.TrimSpace(c.Query("projection_time_zone")),
		})
		writeMobileV2Result(c, result, err)
	}
}

func ApplyMobileV2Command(service MobileV2Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := mobileV2Identity(c)
		if !ok {
			return
		}
		if c.Request.ContentLength > maxMobileV2CommandBytes {
			writeMobileV2Error(c, &MobileV2ProtocolError{
				Status: http.StatusRequestEntityTooLarge, Code: "upgrade_required",
				Message: "mobile-v2 command envelope exceeds 1 MiB",
			})
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxMobileV2CommandBytes)
		decoder := json.NewDecoder(c.Request.Body)
		decoder.UseNumber()
		var envelope json.RawMessage
		if err := decoder.Decode(&envelope); err != nil || len(envelope) == 0 || envelope[0] != '{' {
			writeMobileV2Error(c, &MobileV2ProtocolError{
				Status: http.StatusUnprocessableEntity, Code: "upgrade_required",
				Message: "invalid mobile-v2 command envelope",
			})
			return
		}
		var extra any
		if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
			writeMobileV2Error(c, &MobileV2ProtocolError{
				Status: http.StatusUnprocessableEntity, Code: "upgrade_required",
				Message: "request body must contain one mobile-v2 command envelope",
			})
			return
		}
		result, err := service.ApplyCommand(c.Request.Context(), MobileV2CommandRequest{
			Identity: identity, RawEnvelope: append(json.RawMessage(nil), envelope...),
		})
		writeMobileV2Result(c, result, err)
	}
}

func GetMobileV2CommandReceipt(service MobileV2Service) gin.HandlerFunc {
	return func(c *gin.Context) {
		identity, ok := mobileV2Identity(c)
		if !ok {
			return
		}
		result, err := service.Receipt(c.Request.Context(), MobileV2ReceiptRequest{
			Identity:             identity,
			OriginDeviceClientID: strings.TrimSpace(c.Param("originDeviceClientID")),
			CommandID:            strings.TrimSpace(c.Param("commandID")),
		})
		writeMobileV2Result(c, result, err)
	}
}

func mobileV2Identity(c *gin.Context) (MobileV2Identity, bool) {
	workspaceID, err := auth.WorkspaceIDFromContext(c.Request.Context())
	identity, found := auth.IdentityFromContext(c.Request.Context())
	if err != nil || !found {
		writeMobileV2Error(c, &MobileV2ProtocolError{
			Status: http.StatusUnauthorized, Code: "upgrade_required", Message: "authentication is required",
		})
		return MobileV2Identity{}, false
	}
	return MobileV2Identity{
		WorkspaceID: workspaceID,
		UserID:      identity.UserID,
		SessionID:   identity.SessionID,
		DeviceID:    identity.DeviceID,
	}, true
}

func writeMobileV2Result(c *gin.Context, result any, err error) {
	if err != nil {
		writeMobileV2Error(c, err)
		return
	}
	c.JSON(http.StatusOK, result)
}

func writeMobileV2Error(c *gin.Context, err error) {
	var protocolError *MobileV2ProtocolError
	if errors.As(err, &protocolError) && protocolError != nil {
		status := protocolError.Status
		if status < 400 || status > 599 {
			status = http.StatusInternalServerError
		}
		c.JSON(status, gin.H{
			"schema_version": "mobile-v2",
			"code":           protocolError.Code,
			"message":        protocolError.Message,
		})
		return
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"schema_version": "mobile-v2",
		"code":           "upgrade_required",
		"message":        "mobile-v2 service is unavailable",
	})
}
