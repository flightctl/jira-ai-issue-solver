package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/services"
)

var Logger *zap.Logger

// InitLogger initializes the global logger with appropriate configuration
func InitLogger(config *models.Config) {
	// Get log level from config
	level := getLogLevel(config.Logging.Level)

	// Create encoder config based on format
	var encoderConfig zapcore.EncoderConfig
	if config.Logging.Format == models.LogFormatJSON {
		encoderConfig = zap.NewProductionEncoderConfig()
		encoderConfig.TimeKey = "timestamp"
		encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	} else {
		// Console format (default)
		encoderConfig = zap.NewDevelopmentEncoderConfig()
		encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	// Create core based on format
	var core zapcore.Core
	if config.Logging.Format == models.LogFormatJSON {
		core = zapcore.NewCore(
			zapcore.NewJSONEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			level,
		)
	} else {
		// Console format (default)
		core = zapcore.NewCore(
			zapcore.NewConsoleEncoder(encoderConfig),
			zapcore.AddSync(os.Stdout),
			level,
		)
	}

	// Create logger
	Logger = zap.New(core)
}

// getLogLevel returns the log level based on config
func getLogLevel(level models.LogLevel) zapcore.Level {
	switch level {
	case models.LogLevelDebug:
		return zapcore.DebugLevel
	case models.LogLevelInfo:
		return zapcore.InfoLevel
	case models.LogLevelWarn:
		return zapcore.WarnLevel
	case models.LogLevelError:
		return zapcore.ErrorLevel
	default:
		return zapcore.InfoLevel
	}
}

func main() {
	// Parse command line flags
	configPath := flag.String("config", "", "Path to configuration file (optional, uses environment variables by default)")
	flag.Parse()

	// Load configuration
	config, err := models.LoadConfig(*configPath)
	if err != nil {
		// Use fmt for this error since logger isn't initialized yet
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	// Initialize logger
	InitLogger(config)
	defer func() { _ = Logger.Sync() }()

	// Validate required configuration
	hasComponentToRepo := false
	for _, project := range config.Jira.Projects {
		if len(project.ComponentToRepo) > 0 {
			hasComponentToRepo = true
			break
		}
	}
	if !hasComponentToRepo {
		Logger.Fatal("At least one component_to_repo mapping is required in project configuration")
	}

	// Create services (only if configuration is provided)
	var jiraService services.JiraService
	var githubService services.GitHubService

	// Check if Jira configuration is provided
	if config.Jira.BaseURL != "" && config.Jira.Username != "" && config.Jira.APIToken != "" {
		jiraService = services.NewJiraService(config, Logger)
		Logger.Info("Jira service initialized")
	} else {
		Logger.Warn("Jira configuration not provided - Jira services will be disabled")
	}

	// Check if GitHub configuration is provided
	if config.GitHub.PersonalAccessToken != "" && config.GitHub.BotUsername != "" && config.GitHub.BotEmail != "" {
		githubService = services.NewGitHubService(config, Logger)
		Logger.Info("GitHub service initialized")
	} else {
		Logger.Warn("GitHub configuration not provided - GitHub services will be disabled")
	}

	// Create AI service based on provider selection
	var aiService services.AIService
	switch config.AIProvider {
	case "claude":
		aiService = services.NewClaudeService(config, Logger)
		Logger.Info("Using Claude AI service")
	case "gemini":
		aiService = services.NewGeminiService(config, Logger)
		Logger.Info("Using Gemini AI service")
	default:
		Logger.Fatal("Unsupported AI provider", zap.String("provider", config.AIProvider))
	}

	// Only create scanner services if both Jira and GitHub are configured
	var jiraIssueScannerService services.JiraIssueScannerService
	var prFeedbackScannerService services.PRFeedbackScannerService

	if jiraService != nil && githubService != nil {
		ticketProcessor := services.NewTicketProcessor(jiraService, githubService, aiService, config, Logger)
		jiraIssueScannerService = services.NewJiraIssueScannerService(jiraService, ticketProcessor, config, Logger)
		prFeedbackScannerService = services.NewPRFeedbackScannerService(jiraService, githubService, aiService, config, Logger)

		// Start the Jira issue scanner service for periodic ticket scanning
		Logger.Info("Starting Jira issue scanner service...")
		jiraIssueScannerService.Start()

		// Start the PR feedback scanner service for processing PR review feedback
		Logger.Info("Starting PR feedback scanner service...")
		prFeedbackScannerService.Start()
	} else {
		Logger.Info("Scanner services disabled - Jira or GitHub configuration not provided")
	}

	// Create HTTP server (simplified for health checks only)
	mux := http.NewServeMux()

	// Add a health check endpoint
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, err := fmt.Fprintf(w, "OK")
		if err != nil {
			return
		}
	})

	// Get port from environment variable (for Cloud Run compatibility) or config
	port := config.Server.Port
	if envPort := os.Getenv("PORT"); envPort != "" {
		if envPortInt, err := strconv.Atoi(envPort); err == nil {
			port = envPortInt
		}
	}

	// Create server
	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start the server in a goroutine
	go func() {
		Logger.Info("Starting server", zap.Int("port", port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			Logger.Fatal("Server error", zap.Error(err))
		}
	}()

	// Wait for interrupt signal
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	// Gracefully shutdown the scanner services
	Logger.Info("Shutting down scanner services...")
	if jiraIssueScannerService != nil {
		jiraIssueScannerService.Stop()
	}
	if prFeedbackScannerService != nil {
		prFeedbackScannerService.Stop()
	}

	// Gracefully shutdown the server
	Logger.Info("Shutting down server...")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		Logger.Fatal("Server shutdown failed", zap.Error(err))
	}

	Logger.Info("Server stopped")
}
