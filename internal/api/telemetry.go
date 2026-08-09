package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/telemetry"
)

const (
	maxTelemetryConfirmBody = 64 << 10
	maxTelemetryConfirmIDs  = 512
)

type telemetryConfirmRequest struct {
	PayloadIDs []string `json:"payload_ids"`
}

func (s *Server) registerTelemetryRoutes() {
	s.engine.POST("/v1/telemetry", s.handleTelemetryUpload)
}

func (s *Server) registerTelemetryAdminRoutes() {
	adminAPI := s.adminEngine.Group("/v1/admin/telemetry")
	adminAPI.Use(s.requireAdmin)
	adminAPI.GET("/status", s.handleTelemetryStatus)
	adminAPI.GET("/manifest", s.handleTelemetryManifest)
	adminAPI.GET("/files/:payloadID", s.handleTelemetryFile)
	adminAPI.POST("/confirm", s.handleTelemetryConfirm)
}

func (s *Server) telemetryAdminEnabled() bool {
	return s.telemetry != nil &&
		strings.TrimSpace(s.cfg.AnnouncementAdminToken) != "" &&
		strings.TrimSpace(s.cfg.AdminListenAddr) != ""
}

func (s *Server) handleTelemetryUpload(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	if !s.validateUA(c) {
		writeError(c, http.StatusForbidden, "无效客户端 UA")
		return
	}
	if !validTelemetryContentType(c.GetHeader("Content-Type")) {
		writeError(c, http.StatusUnsupportedMediaType, "Content-Type 必须是 application/json")
		return
	}
	contentEncoding := strings.TrimSpace(strings.ToLower(c.GetHeader("Content-Encoding")))
	if contentEncoding != "" && contentEncoding != "identity" {
		writeError(c, http.StatusUnsupportedMediaType, "遥测接口不接受压缩请求体")
		return
	}
	if s.telemetryLimiter != nil && !s.telemetryLimiter.Allow(
		"telemetry:"+c.ClientIP(),
		s.cfg.TelemetryRateLimit,
		time.Minute,
	) {
		writeError(c, http.StatusTooManyRequests, "遥测提交过于频繁")
		return
	}

	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, telemetry.MaxRequestBodyBytes)
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		var maxBytesError *http.MaxBytesError
		if errors.As(err, &maxBytesError) {
			writeError(c, http.StatusRequestEntityTooLarge, "遥测批次超过 4 MiB")
			return
		}
		writeError(c, http.StatusBadRequest, "读取遥测请求体失败")
		return
	}
	envelopes, err := telemetry.DecodeUploadRequest(body, time.Now().UTC())
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	results := make([]telemetry.UploadResult, 0, len(envelopes))
	for _, envelope := range envelopes {
		status, err := s.telemetry.Save(envelope)
		if err != nil {
			if errors.Is(err, telemetry.ErrPayloadExceedsCapacity) {
				writeError(c, http.StatusInsufficientStorage, "服务端暂时没有足够空间保存遥测")
				return
			}
			writeError(c, http.StatusInternalServerError, "保存遥测数据失败")
			return
		}
		results = append(results, telemetry.UploadResult{
			PayloadID: envelope.Envelope.PayloadID,
			Status:    status,
		})
	}
	c.JSON(http.StatusOK, telemetry.UploadResponse{
		SchemaVersion: envelopes[0].Envelope.SchemaVersion,
		Results:       results,
	})
}

func (s *Server) handleTelemetryStatus(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, s.telemetry.Status())
}

func (s *Server) handleTelemetryManifest(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, s.telemetry.Manifest())
}

func (s *Server) handleTelemetryFile(c *gin.Context) {
	entry, data, err := s.telemetry.Read(c.Param("payloadID"))
	if errors.Is(err, fs.ErrNotExist) {
		writeError(c, http.StatusNotFound, "遥测文件不存在")
		return
	}
	if err != nil {
		writeError(c, http.StatusInternalServerError, "读取遥测文件失败")
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Header("ETag", `"`+entry.FileSHA256+`"`)
	c.Header("X-ELS-Payload-ID", entry.PayloadID)
	c.Header("X-ELS-File-SHA256", entry.FileSHA256)
	c.Header("Content-Length", strconv.FormatInt(int64(len(data)), 10))
	c.Data(http.StatusOK, "application/json", data)
}

func (s *Server) handleTelemetryConfirm(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxTelemetryConfirmBody)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	var request telemetryConfirmRequest
	if err := decoder.Decode(&request); err != nil {
		writeError(c, http.StatusBadRequest, "确认请求体无效")
		return
	}
	if err := ensureJSONEOF(decoder); err != nil {
		writeError(c, http.StatusBadRequest, "确认请求体包含多余内容")
		return
	}
	if len(request.PayloadIDs) == 0 || len(request.PayloadIDs) > maxTelemetryConfirmIDs {
		writeError(c, http.StatusBadRequest, fmt.Sprintf(
			"payload_ids 数量必须在 1 到 %d 之间",
			maxTelemetryConfirmIDs,
		))
		return
	}
	result, err := s.telemetry.Confirm(request.PayloadIDs)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, result)
}

func validTelemetryContentType(raw string) bool {
	mediaType, _, err := mime.ParseMediaType(raw)
	return err == nil && strings.EqualFold(mediaType, "application/json")
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("存在多余 JSON")
		}
		return err
	}
	return nil
}
