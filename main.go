package main

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"openstack-reporter/internal/handlers"
	"openstack-reporter/internal/version"
)

func main() {
	// Print version information
	log.Println(version.GetFullVersionString())

	// Load environment variables
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found, using system environment variables")
	}

	// Initialize web server
	r := gin.Default()

	// Configure trusted proxies for security
	// Set to localhost and private networks only
	trustedProxies := []string{
		"127.0.0.1",
		"::1",
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
	}
	err := r.SetTrustedProxies(trustedProxies)
	if err != nil {
		log.Printf("Warning: Failed to set trusted proxies: %v", err)
	}

	// Setup routes
	setupRoutes(r)

	// Start server
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	log.Printf("Starting server on port %s (version: %s)", port, version.GetVersionString())
	log.Fatal(r.Run(":" + port))
}

// isInternalRequest проверяет, является ли запрос внутренним
func isInternalRequest(clientIP string) bool {
	if clientIP == "" {
		return false
	}

	// Проверяем localhost
	if clientIP == "127.0.0.1" || clientIP == "::1" || clientIP == "localhost" {
		return true
	}

	// Парсим IP адрес
	ip := net.ParseIP(clientIP)
	if ip == nil {
		return false
	}

	// Проверяем приватные сети
	privateNetworks := []*net.IPNet{
		{IP: net.IPv4(10, 0, 0, 0), Mask: net.CIDRMask(8, 32)},     // 10.0.0.0/8
		{IP: net.IPv4(172, 16, 0, 0), Mask: net.CIDRMask(12, 32)},  // 172.16.0.0/12
		{IP: net.IPv4(192, 168, 0, 0), Mask: net.CIDRMask(16, 32)}, // 192.168.0.0/16
	}

	for _, network := range privateNetworks {
		if network.Contains(ip) {
			return true
		}
	}

	return false
}

// authMiddleware проверяет токен авторизации
func authMiddleware() gin.HandlerFunc {
	apiToken := os.Getenv("API_TOKEN")
	if apiToken == "" {
		log.Println("Warning: API_TOKEN not set, API authentication is disabled")
		return func(c *gin.Context) {
			c.Next()
		}
	}

	return func(c *gin.Context) {
		// Получаем IP адрес клиента (Gin учитывает X-Forwarded-For и X-Real-IP благодаря SetTrustedProxies)
		clientIP := c.ClientIP()

		// Проверяем, является ли запрос внутренним по IP адресу
		if isInternalRequest(clientIP) {
			log.Printf("DEBUG: Internal request from %s, skipping auth check", clientIP)
			c.Next()
			return
		}

		// Проверяем заголовки для случаев, когда Traefik проксирует запрос
		// Если запрос идет от браузера через Traefik, реальный IP будет в X-Forwarded-For
		forwardedFor := c.GetHeader("X-Forwarded-For")
		if forwardedFor != "" {
			// X-Forwarded-For может содержать несколько IP через запятую, берем первый
			ips := strings.Split(forwardedFor, ",")
			if len(ips) > 0 {
				realIP := strings.TrimSpace(ips[0])
				if isInternalRequest(realIP) {
					log.Printf("DEBUG: Internal request from %s (via X-Forwarded-For: %s), skipping auth check", clientIP, realIP)
					c.Next()
					return
				}
			}
		}

		// Проверяем, является ли запрос от веб-интерфейса (по Referer или Origin)
		// Если запрос идет с того же домена, это внутренний запрос от веб-интерфейса
		referer := c.GetHeader("Referer")
		origin := c.GetHeader("Origin")
		host := c.GetHeader("Host")

		if host != "" {
			// Если Referer или Origin содержат тот же Host, это запрос от веб-интерфейса
			if referer != "" && strings.Contains(referer, host) {
				log.Printf("DEBUG: Request from web interface (Referer: %s, Host: %s), skipping auth check", referer, host)
				c.Next()
				return
			}
			if origin != "" && strings.Contains(origin, host) {
				log.Printf("DEBUG: Request from web interface (Origin: %s, Host: %s), skipping auth check", origin, host)
				c.Next()
				return
			}
		}

		// Для внешних запросов проверяем токен
		token := ""
		if authHeader := c.GetHeader("Authorization"); authHeader != "" {
			// Поддерживаем формат "Bearer <token>"
			if len(authHeader) > 7 && authHeader[:7] == "Bearer " {
				token = authHeader[7:]
			} else {
				token = authHeader
			}
		} else if apiTokenHeader := c.GetHeader("X-API-Token"); apiTokenHeader != "" {
			token = apiTokenHeader
		} else if queryToken := c.Query("token"); queryToken != "" {
			// Support token in query parameter for EventSource (less secure but necessary)
			token = queryToken
		}

		if token == "" || token != apiToken {
			log.Printf("DEBUG: External request from %s without valid token", clientIP)
			c.JSON(http.StatusUnauthorized, gin.H{
				"error": "Unauthorized",
				"message": "Valid API token required. Use Authorization: Bearer <token>, X-API-Token header, or token query parameter",
			})
			c.Abort()
			return
		}

		log.Printf("DEBUG: External request from %s with valid token", clientIP)
		c.Next()
	}
}

func setupRoutes(r *gin.Engine) {
	// Initialize handlers
	handler := handlers.NewHandler()

	// Add request logging middleware
	r.Use(gin.LoggerWithFormatter(func(param gin.LogFormatterParams) string {
		return fmt.Sprintf("%s - [%s] \"%s %s %s %d %s \"%s\" %s\"\n",
			param.ClientIP,
			param.TimeStamp.Format(time.RFC1123),
			param.Method,
			param.Path,
			param.Request.Proto,
			param.StatusCode,
			param.Latency,
			param.Request.UserAgent(),
			param.ErrorMessage,
		)
	}))

	// Static files
	r.Static("/static", "./web/static")
	r.LoadHTMLGlob("web/templates/*")

	// API routes
	api := r.Group("/api")
	{
		// Public routes (no authentication required)
		api.GET("/status", handler.GetReportStatus)
		api.GET("/version", getVersion)
		api.GET("/docs", getAPIDocs)

		// Protected routes (authentication required)
		protected := api.Group("")
		protected.Use(authMiddleware())
		{
			protected.GET("/resources", handler.GetResources)
			protected.GET("/projects", handler.GetProjects)
			protected.POST("/refresh", handler.RefreshResources)
			protected.POST("/refresh/progress", handler.RefreshWithProgress)
			protected.GET("/progress", handler.GetProgress)
			protected.GET("/export/pdf", handler.ExportToPDF)
		}
	}

	log.Println("Routes registered:")
	log.Println("  Public routes:")
	log.Println("    GET  /api/status")
	log.Println("    GET  /api/version")
	log.Println("    GET  /api/docs")
	log.Println("  Protected routes (require API_TOKEN):")
	log.Println("    GET  /api/resources")
	log.Println("    GET  /api/projects")
	log.Println("    POST /api/refresh")
	log.Println("    POST /api/refresh/progress")
	log.Println("    GET  /api/progress")
	log.Println("    GET  /api/export/pdf")

	// Web routes
	r.GET("/", indexHandler)
	r.GET("/docs", docsHandler)
}

func indexHandler(c *gin.Context) {
	c.HTML(200, "index.html", gin.H{
		"title":   "OpenStack Resources Report",
		"version": version.GetVersionString(),
		"apiToken": os.Getenv("API_TOKEN"), // Pass token to frontend if set
	})
}

func getVersion(c *gin.Context) {
	c.JSON(200, version.Get())
}

// getAPIDocs returns API documentation
func getAPIDocs(c *gin.Context) {
	// Determine scheme (http/https)
	scheme := "http"
	if proto := c.GetHeader("X-Forwarded-Proto"); proto != "" {
		scheme = proto
	} else if c.Request.TLS != nil {
		scheme = "https"
	}

	// Get host from request
	host := c.Request.Host
	if host == "" {
		host = "localhost:8080"
	}

	// Build base URL
	baseURL := fmt.Sprintf("%s://%s/api", scheme, host)

	docs := map[string]interface{}{
		"title":       "OpenStack Reporter API",
		"version":     version.GetVersionString(),
		"description": "REST API for OpenStack resources reporting and management",
		"base_url":    baseURL,
		"endpoints": []map[string]interface{}{
			{
				"method":      "GET",
				"path":        "/api/resources",
				"description": "Get all OpenStack resources from cache or fetch from API",
				"auth_required": true,
				"parameters": []map[string]string{
					{"name": "force", "type": "query", "description": "Force refresh from OpenStack API (optional)"},
					{"name": "project", "type": "query", "description": "Filter by project name(s), comma-separated (e.g., 'project1,project2')"},
					{"name": "project_id", "type": "query", "description": "Filter by project ID(s), comma-separated (e.g., 'id1,id2')"},
					{"name": "type", "type": "query", "description": "Filter by resource type(s), comma-separated (e.g., 'server,volume,network')"},
					{"name": "status", "type": "query", "description": "Filter by status, comma-separated (e.g., 'active,available')"},
				},
				"response": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"projects":        map[string]string{"type": "array", "description": "List of projects"},
						"resources":        map[string]string{"type": "array", "description": "List of all resources (servers, volumes, networks, etc.)"},
						"summary":          map[string]string{"type": "object", "description": "Resource counts summary"},
						"generated_at":     map[string]string{"type": "string", "description": "Report generation timestamp"},
					},
					"note": "Resources array contains all resource types. Use 'type' filter to get specific resource types. Summary is automatically recalculated for filtered results.",
				},
			},
			{
				"method":      "POST",
				"path":        "/api/refresh",
				"description": "Force refresh all resources from OpenStack API",
				"auth_required": true,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"message": map[string]string{"type": "string", "description": "Success message"},
						"data":    map[string]string{"type": "object", "description": "Updated resource data"},
					},
				},
			},
			{
				"method":      "POST",
				"path":        "/api/refresh/progress",
				"description": "Force refresh all resources from OpenStack API with progress updates via SSE",
				"auth_required": true,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type":        "text/event-stream",
					"description": "Server-Sent Events stream with progress updates",
				},
			},
			{
				"method":      "GET",
				"path":        "/api/progress",
				"description": "Get current refresh progress status",
				"auth_required": true,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"status":    map[string]string{"type": "string", "description": "Current status (in_progress, completed, error)"},
						"progress":  map[string]string{"type": "number", "description": "Progress percentage (0-100)"},
						"message":   map[string]string{"type": "string", "description": "Current progress message"},
					},
				},
			},
			{
				"method":      "GET",
				"path":        "/api/export/pdf",
				"description": "Export current report to PDF format",
				"auth_required": true,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type":        "file",
					"description": "PDF file download",
					"headers": map[string]string{
						"Content-Type":        "application/pdf",
						"Content-Disposition": "attachment; filename=openstack-report.pdf",
					},
				},
			},
			{
				"method":      "GET",
				"path":        "/api/status",
				"description": "Get current report status and metadata",
				"auth_required": false,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"exists":      map[string]string{"type": "boolean", "description": "Whether report data exists"},
						"last_update": map[string]string{"type": "string", "description": "Last update timestamp"},
						"age_minutes": map[string]string{"type": "number", "description": "Report age in minutes"},
						"file_size":   map[string]string{"type": "number", "description": "Report file size in bytes"},
					},
				},
			},
			{
				"method":      "GET",
				"path":        "/api/version",
				"description": "Get application version information",
				"auth_required": false,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"version":    map[string]string{"type": "string", "description": "Application version"},
						"git_commit": map[string]string{"type": "string", "description": "Git commit hash"},
						"build_time": map[string]string{"type": "string", "description": "Build timestamp"},
						"go_version": map[string]string{"type": "string", "description": "Go compiler version"},
					},
				},
			},
			{
				"method":      "GET",
				"path":        "/api/docs",
				"description": "Get this API documentation",
				"auth_required": false,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type":        "object",
					"description": "API documentation in JSON format",
				},
			},
			{
				"method":      "GET",
				"path":        "/api/projects",
				"description": "Get list of all OpenStack projects",
				"auth_required": true,
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"projects":     map[string]string{"type": "array", "description": "List of projects"},
						"total":        map[string]string{"type": "number", "description": "Total number of projects"},
						"generated_at": map[string]string{"type": "string", "description": "Report generation timestamp"},
					},
				},
			},
		},
		"authentication": map[string]interface{}{
			"api_auth": map[string]interface{}{
				"type":        "token",
				"description": "API token authentication for protected endpoints",
				"required":    true,
				"header":      "Authorization: Bearer <token> or X-API-Token: <token>",
				"env_var":     "API_TOKEN",
				"note":        "If API_TOKEN is not set, authentication is disabled",
			},
			"openstack_auth": map[string]interface{}{
				"type":        "environment",
				"description": "OpenStack credentials via environment variables",
				"variables": []string{
					"OS_PROJECT_DOMAIN_NAME",
					"OS_USER_DOMAIN_NAME",
					"OS_USERNAME",
					"OS_PASSWORD",
					"OS_AUTH_URL",
					"OS_IDENTITY_API_VERSION",
					"OS_AUTH_TYPE",
					"OS_INSECURE",
				},
			},
		},
		"supported_resources": []map[string]string{
			{"name": "Projects", "description": "OpenStack projects/tenants"},
			{"name": "Servers", "description": "Virtual machines with Flavor and network info (Nova)"},
			{"name": "Volumes", "description": "Block storage volumes with attachment details (Cinder)"},
			{"name": "Networks", "description": "Network resources with subnet information (Neutron)"},
			{"name": "Load Balancers", "description": "Load balancing services with IP addresses (Octavia)"},
			{"name": "Floating IPs", "description": "Public IP addresses with attachment info (Neutron)"},
			{"name": "Routers", "description": "Network routers (Neutron)"},
			{"name": "VPN Connections", "description": "IPSec site-to-site connections with peer info (Neutron VPNaaS)"},
			{"name": "Kubernetes Clusters", "description": "Kubernetes clusters managed by Magnum"},
		},
		"filtering": map[string]interface{}{
			"description": "The /api/resources endpoint supports filtering via query parameters",
			"filters": []map[string]string{
				{"name": "project", "description": "Filter by project name(s), comma-separated (e.g., 'project1,project2')"},
				{"name": "project_id", "description": "Filter by project ID(s), comma-separated (e.g., 'id1,id2')"},
				{"name": "type", "description": "Filter by resource type(s), comma-separated. Available types: server, volume, network, load_balancer, floating_ip, router, vpn_service, cluster"},
				{"name": "status", "description": "Filter by status, comma-separated (e.g., 'active,available')"},
			},
			"examples": []string{
				"/api/resources?project=infra&type=server,volume",
				"/api/resources?type=network&status=active",
				"/api/resources?project_id=123,456&type=server",
			},
		},
	}

	c.Header("Content-Type", "application/json")
	c.JSON(http.StatusOK, docs)
}

// docsHandler renders the API documentation page
func docsHandler(c *gin.Context) {
	c.HTML(http.StatusOK, "docs.html", gin.H{
		"version": version.GetVersionString(),
	})
}
