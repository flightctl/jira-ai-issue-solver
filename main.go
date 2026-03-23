package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"jira-ai-issue-solver/container"
	"jira-ai-issue-solver/costtracker"
	"jira-ai-issue-solver/executor"
	"jira-ai-issue-solver/jobmanager"
	"jira-ai-issue-solver/models"
	"jira-ai-issue-solver/projectresolver"
	"jira-ai-issue-solver/recovery"
	"jira-ai-issue-solver/scanner"
	"jira-ai-issue-solver/services"
	"jira-ai-issue-solver/taskfile"
	"jira-ai-issue-solver/tracker/jira"
	"jira-ai-issue-solver/workspace"
)

func main() {
	configPath := flag.String("config", "", "Path to configuration file (optional, uses environment variables by default)")
	flag.Parse()

	config, err := models.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	logger := initLogger(config)
	defer func() { _ = logger.Sync() }()

	// --- Infrastructure ---

	jiraService := services.NewJiraService(config, logger)
	gitService := services.NewGitHubService(config, logger)

	issueTracker, err := jira.NewAdapter(jiraService, logger)
	if err != nil {
		logger.Fatal("Failed to create issue tracker", zap.Error(err))
	}

	resolver, err := projectresolver.NewConfigResolver(config)
	if err != nil {
		logger.Fatal("Failed to create project resolver", zap.Error(err))
	}

	wsMgr, err := workspace.NewFSManager(config.Workspaces.BaseDir, gitService, logger)
	if err != nil {
		logger.Fatal("Failed to create workspace manager", zap.Error(err))
	}

	// Container runtime detection and manager.
	detected, err := container.DetectRuntime(
		container.Runtime(config.Container.Runtime), nil)
	if err != nil {
		logger.Fatal("Failed to detect container runtime", zap.Error(err))
	}
	logger.Info("Container runtime detected",
		zap.String("runtime", string(detected.Runtime)),
		zap.String("path", detected.Path))

	containerRunner := container.NewCLIRunner(detected)

	containerResolver, err := container.NewResolver(
		container.ResolverDefaults{
			DisableSELinux: config.Container.DisableSELinux,
			UserNS:         config.Container.UserNS,
		},
		logger)
	if err != nil {
		logger.Fatal("Failed to create container resolver", zap.Error(err))
	}

	containerMgr, err := container.NewRuntimeManager(
		containerRunner,
		containerResolver,
		container.RuntimeManagerConfig{
			NamePrefix: "ai-bot",
		},
		logger)
	if err != nil {
		logger.Fatal("Failed to create container manager", zap.Error(err))
	}

	// --- Cost tracking ---

	if err := os.MkdirAll(config.Workspaces.BaseDir, 0o750); err != nil {
		logger.Fatal("Failed to create workspace base directory", zap.Error(err))
	}

	costFile := filepath.Join(config.Workspaces.BaseDir, "daily-cost.json")
	costs, err := costtracker.NewFileTracker(
		costFile,
		config.Guardrails.MaxDailyCostUSD,
		logger)
	if err != nil {
		logger.Fatal("Failed to create cost tracker", zap.Error(err))
	}

	// --- Executor pipeline ---

	aiAPIKeys := make(map[string]string)
	if config.Claude.APIKey != "" {
		aiAPIKeys["claude"] = config.Claude.APIKey
	}
	if config.Gemini.APIKey != "" {
		aiAPIKeys["gemini"] = config.Gemini.APIKey
	}

	pipeline, err := executor.NewPipeline(
		executor.Config{
			BotUsername:        config.GitHub.BotUsername,
			DefaultProvider:    config.AIProvider,
			AIAPIKeys:          aiAPIKeys,
			SessionTimeout:     time.Duration(config.Guardrails.MaxContainerRuntimeMinutes) * time.Minute,
			IgnoredUsernames:   config.GitHub.IgnoredUsernames,
			KnownBotUsernames:  config.GitHub.KnownBotUsernames,
			MaxThreadDepth:     config.GitHub.MaxThreadDepth,
			DefaultGeminiModel: config.Gemini.Model,
		},
		issueTracker,
		gitService,
		containerMgr,
		wsMgr,
		taskfile.NewMarkdownWriter(),
		resolver,
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to create executor pipeline", zap.Error(err))
	}

	// --- Job manager ---

	coordinator, err := jobmanager.NewCoordinator(
		jobmanager.Config{
			MaxConcurrent:           config.Guardrails.MaxConcurrentJobs,
			MaxRetries:              config.Guardrails.MaxRetries,
			CircuitBreakerThreshold: config.Guardrails.CircuitBreakerThreshold,
			CircuitBreakerWindow:    time.Duration(config.Guardrails.CircuitBreakerWindowMinutes) * time.Minute,
			CircuitBreakerCooldown:  time.Duration(config.Guardrails.CircuitBreakerCooldownMinutes) * time.Minute,
			CostRecorder:            costs,
		},
		pipeline.Execute,
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to create job coordinator", zap.Error(err))
	}

	// --- Crash recovery ---

	todoCriteria, inReviewCriteria, activeStatuses := buildScanCriteria(config)

	startupRunner, err := recovery.NewStartupRunner(
		recovery.Config{
			ContainerPrefix:    "ai-bot",
			WorkspaceTTL:       time.Duration(config.Workspaces.TTLDays) * 24 * time.Hour,
			BotUsername:        config.GitHub.BotUsername,
			InProgressCriteria: buildInProgressCriteria(config),
			ActiveStatuses:     activeStatuses,
		},
		issueTracker,
		gitService,
		wsMgr,
		containerMgr,
		coordinator,
		resolver,
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to create startup runner", zap.Error(err))
	}

	// Run crash recovery before starting scanners.
	if err := startupRunner.Run(context.Background()); err != nil {
		logger.Warn("Crash recovery returned error", zap.Error(err))
	}

	// --- Scanners ---

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ticketScanner, err := scanner.NewWorkItemScanner(
		issueTracker,
		coordinator,
		scanner.WorkItemScannerConfig{
			Criteria:     todoCriteria,
			PollInterval: time.Duration(config.Jira.IntervalSeconds) * time.Second,
		},
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to create work item scanner", zap.Error(err))
	}

	feedbackScanner, err := scanner.NewFeedbackScanner(
		issueTracker,
		coordinator,
		gitService,
		resolver,
		scanner.FeedbackScannerConfig{
			Criteria:          inReviewCriteria,
			PollInterval:      time.Duration(config.Jira.IntervalSeconds) * time.Second,
			BotUsername:       config.GitHub.BotUsername,
			IgnoredUsernames:  config.GitHub.IgnoredUsernames,
			KnownBotUsernames: config.GitHub.KnownBotUsernames,
			MaxThreadDepth:    config.GitHub.MaxThreadDepth,
		},
		logger,
	)
	if err != nil {
		logger.Fatal("Failed to create feedback scanner", zap.Error(err))
	}

	if err := ticketScanner.Start(ctx); err != nil {
		logger.Fatal("Failed to start work item scanner", zap.Error(err))
	}
	if err := feedbackScanner.Start(ctx); err != nil {
		logger.Fatal("Failed to start feedback scanner", zap.Error(err))
	}

	logger.Info("Scanners started")

	// --- HTTP server ---

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, "OK")
	})

	port := config.Server.Port
	if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil {
			port = p
		}
	}

	server := &http.Server{
		Addr:              fmt.Sprintf(":%d", port),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Info("Starting server", zap.Int("port", port))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error", zap.Error(err))
			stop <- syscall.SIGTERM
		}
	}()

	// --- Graceful shutdown ---

	<-stop

	logger.Info("Shutdown signal received")

	// Stop accepting new work.
	cancel()
	ticketScanner.Stop()
	feedbackScanner.Stop()

	// Drain running jobs.
	coordinator.Shutdown()

	// Shut down HTTP server.
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Error("Server shutdown error", zap.Error(err))
	}

	logger.Info("Shutdown complete")
}

// initLogger creates a structured logger from the application config.
func initLogger(config *models.Config) *zap.Logger {
	level := getLogLevel(config.Logging.Level)

	var encoderConfig zapcore.EncoderConfig
	if config.Logging.Format == models.LogFormatJSON {
		encoderConfig = zap.NewProductionEncoderConfig()
		encoderConfig.TimeKey = "timestamp"
		encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		encoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder
	} else {
		encoderConfig = zap.NewDevelopmentEncoderConfig()
		encoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
		encoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	var encoder zapcore.Encoder
	if config.Logging.Format == models.LogFormatJSON {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	}

	core := zapcore.NewCore(encoder, zapcore.AddSync(os.Stdout), level)
	return zap.New(core)
}

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

// buildScanCriteria constructs the search criteria for both scanners
// and the set of active statuses for workspace cleanup, derived from
// the multi-project configuration.
func buildScanCriteria(config *models.Config) (todo, inReview models.SearchCriteria, activeStatuses map[string]bool) {
	todoByType := make(map[string][]string)
	inReviewByType := make(map[string][]string)
	activeStatuses = make(map[string]bool)
	var projectKeys []string

	for _, project := range config.Jira.Projects {
		projectKeys = append(projectKeys, project.ProjectKeys...)

		for ticketType, transitions := range project.StatusTransitions {
			todoByType[ticketType] = appendUnique(todoByType[ticketType], transitions.Todo)
			inReviewByType[ticketType] = appendUnique(inReviewByType[ticketType], transitions.InReview)

			activeStatuses[transitions.Todo] = true
			activeStatuses[transitions.InProgress] = true
			activeStatuses[transitions.InReview] = true
		}
	}

	todo = models.SearchCriteria{
		ProjectKeys:              projectKeys,
		StatusByType:             todoByType,
		ContributorIsCurrentUser: true,
	}

	inReview = models.SearchCriteria{
		ProjectKeys:              projectKeys,
		StatusByType:             inReviewByType,
		ContributorIsCurrentUser: true,
	}

	return todo, inReview, activeStatuses
}

// buildInProgressCriteria constructs the search criteria for finding
// tickets stuck in "in progress" during crash recovery.
func buildInProgressCriteria(config *models.Config) models.SearchCriteria {
	inProgressByType := make(map[string][]string)
	var projectKeys []string

	for _, project := range config.Jira.Projects {
		projectKeys = append(projectKeys, project.ProjectKeys...)

		for ticketType, transitions := range project.StatusTransitions {
			inProgressByType[ticketType] = appendUnique(
				inProgressByType[ticketType], transitions.InProgress)
		}
	}

	return models.SearchCriteria{
		ProjectKeys:              projectKeys,
		StatusByType:             inProgressByType,
		ContributorIsCurrentUser: true,
	}
}

// appendUnique appends value to slice only if not already present.
func appendUnique(slice []string, value string) []string {
	for _, v := range slice {
		if v == value {
			return slice
		}
	}
	return append(slice, value)
}
