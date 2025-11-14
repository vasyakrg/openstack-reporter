package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
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
		api.GET("/resources", handler.GetResources)
		api.POST("/refresh", handler.RefreshResources)
		api.POST("/refresh/progress", handler.RefreshWithProgress)
		api.GET("/progress", handler.GetProgress)
		api.GET("/export/pdf", handler.ExportToPDF)
		api.GET("/status", handler.GetReportStatus)
		api.GET("/version", getVersion)
		api.GET("/docs", getAPIDocs)
	}

	log.Println("Routes registered:")
	log.Println("  GET  /api/resources")
	log.Println("  POST /api/refresh")
	log.Println("  POST /api/refresh/progress")
	log.Println("  GET  /api/progress")
	log.Println("  GET  /api/export/pdf")
	log.Println("  GET  /api/status")
	log.Println("  GET  /api/version")
	log.Println("  GET  /api/docs")

	// Web routes
	r.GET("/", indexHandler)
	r.GET("/docs", docsHandler)
}

func indexHandler(c *gin.Context) {
	c.HTML(200, "index.html", gin.H{
		"title":   "OpenStack Resources Report",
		"version": version.GetVersionString(),
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
				"parameters": []map[string]string{
					{"name": "force", "type": "query", "description": "Force refresh from OpenStack API (optional)"},
				},
				"response": map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"projects":        map[string]string{"type": "array", "description": "List of projects"},
						"servers":         map[string]string{"type": "array", "description": "List of virtual machines"},
						"volumes":         map[string]string{"type": "array", "description": "List of storage volumes"},
						"load_balancers":  map[string]string{"type": "array", "description": "List of load balancers"},
						"floating_ips":    map[string]string{"type": "array", "description": "List of floating IP addresses"},
						"routers":         map[string]string{"type": "array", "description": "List of network routers"},
						"vpn_services":    map[string]string{"type": "array", "description": "List of VPN IPSec site connections"},
						"summary":         map[string]string{"type": "object", "description": "Resource counts summary"},
						"generated_at":    map[string]string{"type": "string", "description": "Report generation timestamp"},
					},
				},
			},
			{
				"method":      "POST",
				"path":        "/api/refresh",
				"description": "Force refresh all resources from OpenStack API",
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
				"method":      "GET",
				"path":        "/api/export/pdf",
				"description": "Export current report to PDF format",
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
				"parameters":  []map[string]string{},
				"response": map[string]interface{}{
					"type":        "object",
					"description": "API documentation in JSON format",
				},
			},
		},
		"authentication": map[string]interface{}{
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
		"supported_resources": []map[string]string{
			{"name": "Projects", "description": "OpenStack projects/tenants"},
			{"name": "Servers", "description": "Virtual machines with Flavor and network info (Nova)"},
			{"name": "Volumes", "description": "Block storage volumes with attachment details (Cinder)"},
			{"name": "Load Balancers", "description": "Load balancing services with IP addresses (Octavia)"},
			{"name": "Floating IPs", "description": "Public IP addresses with attachment info (Neutron)"},
			{"name": "Routers", "description": "Network routers (Neutron)"},
			{"name": "VPN Connections", "description": "IPSec site-to-site connections with peer info (Neutron VPNaaS)"},
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
