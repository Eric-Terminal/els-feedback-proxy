package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"html/template"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	announcementAdminCookieName = "els_announcement_admin"
	announcementAdminSessionTTL = 12 * time.Hour
	maxAdminLoginBody           = 8 << 10
)

//go:embed web/*
var announcementAdminWeb embed.FS

var announcementLoginTemplate = template.Must(
	template.ParseFS(announcementAdminWeb, "web/login.html"),
)

func (s *Server) adminInterfaceEnabled() bool {
	return (s.announcements != nil || s.distribution != nil) &&
		strings.TrimSpace(s.cfg.AnnouncementAdminToken) != "" &&
		strings.TrimSpace(s.cfg.AdminListenAddr) != ""
}

func (s *Server) registerAdminUIRoutes() {
	s.adminEngine.GET("/admin", func(c *gin.Context) {
		c.Redirect(http.StatusTemporaryRedirect, "/admin/announcements")
	})
	s.adminEngine.GET("/admin/announcements", s.handleAnnouncementAdminPage)
	s.adminEngine.GET("/admin/distribution", s.handleDistributionAdminPage)
	s.adminEngine.POST("/admin/login", s.handleAnnouncementAdminLogin)
	s.adminEngine.POST("/admin/logout", s.handleAnnouncementAdminLogout)
	s.adminEngine.GET("/admin/assets/admin.css", serveAnnouncementAdminAsset("admin.css", "text/css; charset=utf-8"))
	s.adminEngine.GET(
		"/admin/assets/admin.js",
		serveAnnouncementAdminAsset("admin.js", "text/javascript; charset=utf-8"),
	)
	s.adminEngine.GET(
		"/admin/assets/distribution.js",
		serveAnnouncementAdminAsset("distribution.js", "text/javascript; charset=utf-8"),
	)
}

func (s *Server) handleAnnouncementAdminPage(c *gin.Context) {
	writeAnnouncementAdminPageHeaders(c)
	if !s.adminSessionIsValid(c.Request) {
		c.Status(http.StatusOK)
		if err := announcementLoginTemplate.ExecuteTemplate(c.Writer, "login.html", struct {
			LoginFailed bool
		}{
			LoginFailed: c.Query("login") == "failed",
		}); err != nil {
			c.Status(http.StatusInternalServerError)
		}
		return
	}

	data, err := announcementAdminWeb.ReadFile("web/admin.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "读取管理页面失败")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func (s *Server) handleDistributionAdminPage(c *gin.Context) {
	writeAnnouncementAdminPageHeaders(c)
	if !s.adminSessionIsValid(c.Request) {
		c.Redirect(http.StatusSeeOther, "/admin/announcements")
		return
	}

	data, err := announcementAdminWeb.ReadFile("web/distribution.html")
	if err != nil {
		c.String(http.StatusInternalServerError, "读取官方数据页面失败")
		return
	}
	c.Data(http.StatusOK, "text/html; charset=utf-8", data)
}

func (s *Server) handleAnnouncementAdminLogin(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxAdminLoginBody)
	if s.limiter != nil && !s.allowRate(
		"announcement-admin-login",
		c.ClientIP(),
		s.cfg.AdminLoginLimitPerWindow,
	) {
		writeAnnouncementAdminPageHeaders(c)
		c.String(http.StatusTooManyRequests, "登录尝试过于频繁，请稍后再试")
		return
	}
	if err := c.Request.ParseForm(); err != nil {
		c.Redirect(http.StatusSeeOther, "/admin/announcements?login=failed")
		return
	}

	provided := c.Request.FormValue("password")
	expected := strings.TrimSpace(s.cfg.AnnouncementAdminToken)
	if expected == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) != 1 {
		c.Redirect(http.StatusSeeOther, "/admin/announcements?login=failed")
		return
	}

	expiresAt := time.Now().UTC().Add(announcementAdminSessionTTL)
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     announcementAdminCookieName,
		Value:    s.makeAdminSession(expiresAt),
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   int(announcementAdminSessionTTL.Seconds()),
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	c.Redirect(http.StatusSeeOther, "/admin/announcements")
}

func (s *Server) handleAnnouncementAdminLogout(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name:     announcementAdminCookieName,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   c.Request.TLS != nil,
		SameSite: http.SameSiteStrictMode,
	})
	c.Redirect(http.StatusSeeOther, "/admin/announcements")
}

func (s *Server) requireAdmin(c *gin.Context) {
	if s.adminBearerIsValid(c.Request) {
		c.Next()
		return
	}
	if !s.adminSessionIsValid(c.Request) {
		writeError(c, http.StatusUnauthorized, "管理会话无效或已过期")
		c.Abort()
		return
	}
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead &&
		!isSameOriginAdminRequest(c.Request) {
		writeError(c, http.StatusForbidden, "管理请求来源无效")
		c.Abort()
		return
	}
	c.Next()
}

func (s *Server) makeAdminSession(expiresAt time.Time) string {
	expires := strconv.FormatInt(expiresAt.Unix(), 10)
	mac := hmac.New(sha256.New, []byte(s.cfg.AnnouncementAdminToken))
	mac.Write([]byte("announcement-admin-session\n" + expires))
	return expires + "." + hex.EncodeToString(mac.Sum(nil))
}

func (s *Server) adminSessionIsValid(request *http.Request) bool {
	cookie, err := request.Cookie(announcementAdminCookieName)
	if err != nil {
		return false
	}
	parts := strings.Split(cookie.Value, ".")
	if len(parts) != 2 {
		return false
	}
	expiresUnix, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || time.Now().UTC().Unix() >= expiresUnix {
		return false
	}

	expected := s.makeAdminSession(time.Unix(expiresUnix, 0).UTC())
	return subtle.ConstantTimeCompare([]byte(cookie.Value), []byte(expected)) == 1
}

func (s *Server) adminBearerIsValid(request *http.Request) bool {
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if len(authorization) < 8 || !strings.EqualFold(authorization[:7], "Bearer ") {
		return false
	}
	provided := strings.TrimSpace(authorization[7:])
	expected := strings.TrimSpace(s.cfg.AnnouncementAdminToken)
	return expected != "" && subtle.ConstantTimeCompare([]byte(provided), []byte(expected)) == 1
}

func isSameOriginAdminRequest(request *http.Request) bool {
	source := strings.TrimSpace(request.Header.Get("Origin"))
	if source == "" {
		source = strings.TrimSpace(request.Header.Get("Referer"))
	}
	if source == "" {
		return false
	}

	parsed, err := url.Parse(source)
	if err != nil {
		return false
	}
	return strings.EqualFold(parsed.Host, request.Host)
}

func serveAnnouncementAdminAsset(name, contentType string) gin.HandlerFunc {
	return func(c *gin.Context) {
		data, err := announcementAdminWeb.ReadFile("web/" + name)
		if err != nil {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "public, max-age=3600")
		c.Header("X-Content-Type-Options", "nosniff")
		c.Data(http.StatusOK, contentType, data)
	}
}

func writeAnnouncementAdminPageHeaders(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	c.Header("Content-Security-Policy", strings.Join([]string{
		"default-src 'none'",
		"style-src 'self'",
		"script-src 'self'",
		"connect-src 'self'",
		"img-src 'self' data:",
		"form-action 'self'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
	}, "; "))
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("X-Frame-Options", "DENY")
	c.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=()")
}
