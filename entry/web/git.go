package main

import (
	"html/template"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/mcasperson/MockGitRepo/internal/application/handlers"
	"github.com/mcasperson/MockGitRepo/internal/application/templates"
	"github.com/mcasperson/MockGitRepo/internal/domain/cleanup"
	"github.com/mcasperson/MockGitRepo/internal/domain/configuration"
	"github.com/mcasperson/MockGitRepo/internal/domain/logging"
	"go.uber.org/zap"
)

func main() {
	if err := logging.ConfigureLogger(); err != nil {
		panic("Failed to configure logger: " + err.Error())
	}

	// Start cleanup
	cleanup.StartTempDirCleanup()

	// Create a new Gin router with default middleware
	router := gin.Default()

	// Set up template functions and load embedded templates
	funcMap := template.FuncMap{
		"split": strings.Split,
		"joinPath": func(base, part string) string {
			if base == "" {
				return part
			}
			return base + "/" + part
		},
	}
	tmpl := template.Must(template.New("").Funcs(funcMap).ParseFS(templates.FS, "*.html"))
	router.SetHTMLTemplate(tmpl)

	// Git HTTP backend routes
	// Match all Git HTTP protocol routes
	router.Any("/repo/*path", handlers.GitHTTPBackend)

	// This allows us to insert a random id into the URL, which bypasses
	// the constraint in Octopus where a git repo can only be used one.
	router.Any("/uniquerepo/:id/*path", handlers.GitHTTPBackend)

	configuration.GetServiceToken()
	router.PUT("/api/credentials", handlers.Credentials)

	// File browser route - /browse/:username/*filepath
	router.GET("/browse/:username/*filepath", handlers.GitBrowser)
	router.GET("/browse/:username", handlers.GitBrowser)

	// Health check endpoint
	router.GET("/health", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	// Get port from environment variable or default to 8080
	port := configuration.GetPort()

	logging.Logger.Info("Starting HTTP server", zap.String("port", port))
	// Start the server
	if err := router.Run(":" + port); err != nil {
		logging.Logger.Fatal("Failed to start server", zap.Error(err))
	}
}
