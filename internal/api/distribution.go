package api

import (
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"els-feedback-proxy/internal/store"
)

const distributionMultipartOverhead = 1 << 20

type publicDistributionManifest struct {
	Version   int                       `json:"version"`
	Downloads []publicDistributionEntry `json:"downloads"`
}

type publicDistributionEntry struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	FileName string `json:"file_name"`
	SHA256   string `json:"sha256"`
	Size     int64  `json:"size"`
}

func (s *Server) registerDistributionRoutes() {
	if s.distribution == nil {
		return
	}
	s.engine.GET("/v1/distribution/manifest", s.handleDistributionManifest)
	s.engine.GET("/v1/distribution/files/:checksum/:fileName", s.handleDistributionFile)
}

func (s *Server) registerDistributionAdminRoutes() {
	adminAPI := s.adminEngine.Group("/v1/admin/distribution")
	adminAPI.Use(s.requireAdmin)
	adminAPI.GET("", s.handleAdminListDistribution)
	adminAPI.POST("", s.handleAdminCreateDistribution)
	adminAPI.PUT("/:key", s.handleAdminUpdateDistribution)
	adminAPI.DELETE("/:key", s.handleAdminDeleteDistribution)
}

func (s *Server) handleDistributionManifest(c *gin.Context) {
	records := s.distribution.PublicList()
	downloads := make([]publicDistributionEntry, 0, len(records))
	for _, record := range records {
		downloads = append(downloads, publicDistributionEntry{
			Name: record.Name,
			Path: record.DestinationPath,
			URL: fmt.Sprintf(
				"/v1/distribution/files/%s/%s",
				record.SHA256,
				url.PathEscape(record.FileName),
			),
			FileName: record.FileName,
			SHA256:   record.SHA256,
			Size:     record.Size,
		})
	}

	payload, err := json.Marshal(publicDistributionManifest{
		Version:   1,
		Downloads: downloads,
	})
	if err != nil {
		writeError(c, http.StatusInternalServerError, "编码官方数据清单失败")
		return
	}

	etag := payloadETag(payload)
	c.Header("Cache-Control", "public, max-age=60, stale-if-error=86400")
	c.Header(
		"Cloudflare-CDN-Cache-Control",
		"public, max-age=300, stale-while-revalidate=60, stale-if-error=86400",
	)
	c.Header("ETag", etag)
	c.Header("Vary", "Accept-Encoding")
	c.Header("X-Content-Type-Options", "nosniff")
	if etagMatches(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json; charset=utf-8", payload)
}

func (s *Server) handleDistributionFile(c *gin.Context) {
	record, filePath, ok := s.distribution.PublicFile(
		c.Param("checksum"),
		c.Param("fileName"),
	)
	if !ok {
		c.Status(http.StatusNotFound)
		return
	}

	disposition := mime.FormatMediaType("attachment", map[string]string{
		"filename": record.FileName,
	})
	c.Header("Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Cloudflare-CDN-Cache-Control", "public, max-age=31536000, immutable")
	c.Header("Content-Disposition", disposition)
	c.Header("Content-Length", strconv.FormatInt(record.Size, 10))
	c.Header("ETag", `"`+record.SHA256+`"`)
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Content-Type", record.ContentType)
	c.File(filePath)
}

func (s *Server) handleAdminListDistribution(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"records": s.distribution.List(),
	})
}

func (s *Server) handleAdminCreateDistribution(c *gin.Context) {
	input, upload, cleanup, err := decodeDistributionMultipart(c, true)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	created, err := s.distribution.Create(input, *upload)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusCreated, gin.H{
		"success": true,
		"record":  created,
	})
}

func (s *Server) handleAdminUpdateDistribution(c *gin.Context) {
	input, upload, cleanup, err := decodeDistributionMultipart(c, false)
	if cleanup != nil {
		defer cleanup()
	}
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	updated, err := s.distribution.Update(c.Param("key"), input, upload)
	if err != nil {
		writeDistributionStoreError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"record":  updated,
	})
}

func (s *Server) handleAdminDeleteDistribution(c *gin.Context) {
	if err := s.distribution.Delete(c.Param("key")); err != nil {
		writeDistributionStoreError(c, err)
		return
	}
	c.Header("Cache-Control", "no-store")
	c.Status(http.StatusNoContent)
}

func decodeDistributionMultipart(
	c *gin.Context,
	fileRequired bool,
) (store.DistributionInput, *store.DistributionUpload, func(), error) {
	c.Request.Body = http.MaxBytesReader(
		c.Writer,
		c.Request.Body,
		store.MaxDistributionFileSize+distributionMultipartOverhead,
	)
	if err := c.Request.ParseMultipartForm(8 << 20); err != nil {
		return store.DistributionInput{}, nil, nil, fmt.Errorf("解析上传表单失败: %w", err)
	}

	cleanup := func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}
	enabled, err := strconv.ParseBool(strings.TrimSpace(c.Request.FormValue("enabled")))
	if err != nil {
		return store.DistributionInput{}, nil, cleanup, fmt.Errorf("发布状态无效")
	}
	input := store.DistributionInput{
		Name:            c.Request.FormValue("name"),
		DestinationPath: c.Request.FormValue("destination_path"),
		Enabled:         enabled,
	}

	files := c.Request.MultipartForm.File["file"]
	if len(files) == 0 {
		if fileRequired {
			return store.DistributionInput{}, nil, cleanup, fmt.Errorf("必须选择上传文件")
		}
		return input, nil, cleanup, nil
	}
	if len(files) != 1 {
		return store.DistributionInput{}, nil, cleanup, fmt.Errorf("每次只能上传一个文件")
	}

	file, err := files[0].Open()
	if err != nil {
		return store.DistributionInput{}, nil, cleanup, fmt.Errorf("打开上传文件失败: %w", err)
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, store.MaxDistributionFileSize+1))
	if err != nil {
		return store.DistributionInput{}, nil, cleanup, fmt.Errorf("读取上传文件失败: %w", err)
	}
	if len(data) > store.MaxDistributionFileSize {
		return store.DistributionInput{}, nil, cleanup, fmt.Errorf("上传文件不能超过 32 MiB")
	}

	contentType := strings.TrimSpace(files[0].Header.Get("Content-Type"))
	if contentType == "" {
		contentType = http.DetectContentType(data)
	}
	return input, &store.DistributionUpload{
		FileName:    files[0].Filename,
		ContentType: contentType,
		Data:        data,
	}, cleanup, nil
}

func writeDistributionStoreError(c *gin.Context, err error) {
	if strings.Contains(err.Error(), "不存在") {
		writeError(c, http.StatusNotFound, err.Error())
		return
	}
	writeError(c, http.StatusBadRequest, err.Error())
}
