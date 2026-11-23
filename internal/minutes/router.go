package minutes

import (
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	"github.com/gin-gonic/gin"
)

// Handler builds the shared HTTP entry point for the desktop window and browser.
func (a *App) Handler(web fs.FS, allowLAN bool) http.Handler {
	router := gin.New()
	router.Use(gin.Recovery())
	_ = router.SetTrustedProxies(nil)
	router.HandleMethodNotAllowed = true
	router.RedirectTrailingSlash = false
	router.UseRawPath = true
	router.UnescapePathValues = false

	registerAPI(router.Group("/api"), a)
	router.Match([]string{http.MethodGet, http.MethodHead}, "/ws", gin.WrapF(a.events))
	if web != nil {
		files := http.FileServerFS(web)
		router.NoRoute(func(c *gin.Context) {
			r := c.Request
			if (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
				r.URL.Path != "/api" && !strings.HasPrefix(r.URL.Path, "/api/") {
				files.ServeHTTP(c.Writer, r)
			}
		})
	}
	return networkBoundary(http.NewCrossOriginProtection().Handler(router), allowLAN)
}

// registerAPI groups meeting routes and preserves GET and HEAD support for reads.
func registerAPI(api *gin.RouterGroup, a *App) {
	read := []string{http.MethodGet, http.MethodHead}
	api.Match(read, "/health", gin.WrapF(health))
	api.Match(read, "/meetings", gin.WrapF(a.meetings))
	api.POST("/meetings", gin.WrapF(a.create))
	api.POST("/import", gin.WrapF(a.upload))
	api.GET("/import/capabilities", gin.WrapF(importCapabilities))
	api.Match(read, "/settings", gin.WrapF(a.getSettings))
	api.PUT("/settings", gin.WrapF(a.putSettings))
	api.Match(read, "/asr/profiles", gin.WrapF(serveASRProfiles))
	api.Match(read, "/summary/profiles", gin.WrapF(serveSummaryProfiles))

	meeting := api.Group("/meetings/:id", setPathValues)
	meeting.Match(read, "", gin.WrapF(a.meeting))
	meeting.PATCH("", gin.WrapF(a.update))
	meeting.DELETE("", gin.WrapF(a.delete))
	meeting.POST("/record/:action", gin.WrapF(a.record))
	meeting.POST("/jobs/:kind", gin.WrapF(a.enqueue))
	meeting.PATCH("/segments/:index", gin.WrapF(a.editSegment))
	meeting.Match(read, "/audio", gin.WrapF(a.audio))
	meeting.Match(read, "/export", gin.WrapF(a.export))
}

// health confirms that the local HTTP server is serving requests.
func health(w http.ResponseWriter, _ *http.Request) {
	respond(w, http.StatusOK, map[string]bool{"ok": true})
}

// setPathValues makes Gin captures available to standard HTTP handlers.
func setPathValues(c *gin.Context) {
	for _, param := range c.Params {
		value := param.Value
		if c.Request.URL.RawPath != "" {
			value, _ = url.PathUnescape(value)
		}
		c.Request.SetPathValue(param.Key, value)
	}
	c.Next()
}

// IsLANAddress identifies private and link-local unicast addresses used on local networks.
func IsLANAddress(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsPrivate() || ip.IsLinkLocalUnicast()
}

func requestHost(r *http.Request) string {
	host := r.Host
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	return strings.Trim(host, "[]")
}

func canManageCredentials(r *http.Request) bool {
	peer, _ := netip.ParseAddrPort(r.RemoteAddr)
	host := requestHost(r)
	ip, _ := netip.ParseAddr(host)
	return peer.Addr().Unmap().IsLoopback() && (ip.Unmap().IsLoopback() || strings.EqualFold(host, "localhost"))
}

// networkBoundary restricts client and Host addresses before checking browser origins.
func networkBoundary(next http.Handler, allowLAN bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := requestHost(r)
		ip, _ := netip.ParseAddr(host)
		if !strings.EqualFold(host, "localhost") && !ip.Unmap().IsLoopback() && !(allowLAN && IsLANAddress(ip)) {
			fail(w, http.StatusForbidden, "请使用本机或局域网 IP 地址访问")
			return
		}
		if allowLAN {
			peer, _ := netip.ParseAddrPort(r.RemoteAddr)
			if !peer.Addr().Unmap().IsLoopback() && !IsLANAddress(peer.Addr()) {
				fail(w, http.StatusForbidden, "仅允许本机和局域网设备访问")
				return
			}
		}
		if strings.EqualFold(r.Header.Get("Sec-Fetch-Site"), "cross-site") && (strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws") {
			fail(w, http.StatusForbidden, "拒绝跨站请求")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; connect-src 'self' ws:; frame-ancestors 'none'; object-src 'none'; base-uri 'none'")
		next.ServeHTTP(w, r)
	})
}
