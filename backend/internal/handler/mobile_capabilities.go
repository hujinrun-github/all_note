package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hujinrun/flowspace/internal/mobilecontract"
)

type MobileCapabilityFeatures struct {
	Sync              bool `json:"sync"`
	VoiceUpload       bool `json:"voice_upload"`
	TranscriptionJobs bool `json:"transcription_jobs"`
	WatchPairing      bool `json:"watch_pairing"`
}

type mobileCapabilitiesResponse struct {
	SchemaVersion  string                   `json:"schema_version"`
	ContractSHA256 string                   `json:"contract_sha256"`
	Features       MobileCapabilityFeatures `json:"features"`
}

func GetMobileCapabilities(features MobileCapabilityFeatures) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.JSON(http.StatusOK, mobileCapabilitiesResponse{
			SchemaVersion:  mobilecontract.SchemaVersion,
			ContractSHA256: mobilecontract.ContractSHA256,
			Features:       features,
		})
	}
}
