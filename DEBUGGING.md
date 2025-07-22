# Debugging Guide for Jira AI Issue Solver

This guide covers various ways to debug the Go application.

## Prerequisites

1. **Go 1.24+** installed
2. **Delve debugger** installed (automatically installed by the debug script)
3. **VS Code** with Go extension (recommended for GUI debugging)

## Quick Start

### Option 1: Using the Debug Script (Easiest)
```bash
./debug.sh
```

### Option 2: Using Make
```bash
# Debug with main config
make debug

# Debug tests
make debug-tests
```

### Option 3: Direct Delve Commands
```bash
# Debug main application
dlv debug main.go -- -config config.yaml

# Debug tests
dlv test ./... -- -v
```

## VS Code Debugging

1. Open the project in VS Code
2. Install the Go extension if not already installed
3. Press `F5` or go to Run and Debug panel
4. Select one of the debug configurations:
   - **Debug Jira AI Issue Solver**: Uses main config
   - **Debug Tests**: Runs tests in debug mode

## Setting Breakpoints

### In VS Code
- Click in the left margin next to line numbers to set breakpoints
- Red dots indicate active breakpoints

### In Delve CLI
```bash
# Set breakpoint at main function
(dlv) break main.main

# Set breakpoint at specific line in a file
(dlv) break main.go:75

# Set breakpoint in a specific package
(dlv) break services/jira.go:42

# Set breakpoint at function name
(dlv) break services.NewJiraService
```

## Common Debugging Commands

### Delve Commands
```bash
# Execution control
continue (c)     # Continue execution
next (n)         # Step over (execute current line)
step (s)         # Step into (go into function calls)
stepout (so)     # Step out (exit current function)
restart (r)      # Restart program

# Breakpoints
break (b)        # Set breakpoint
clear            # Clear breakpoint
clearall         # Clear all breakpoints
list             # List breakpoints

# Variable inspection
print (p)        # Print variable value
vars             # Show variables in scope
locals           # Show local variables
args             # Show function arguments

# Stack and goroutines
bt               # Show backtrace
goroutines       # Show all goroutines
threads          # Show threads

# Other
help (h)         # Show help
quit (q)         # Exit debugger
```

### VS Code Debugging
- Use the Debug Console to evaluate expressions
- Hover over variables to see their values
- Use the Variables panel to inspect all variables
- Use the Call Stack to navigate through function calls

## Debugging Specific Components

### Jira Service
```bash
# Set breakpoint in Jira service initialization
(dlv) break services.NewJiraService

# Set breakpoint in Jira API calls
(dlv) break services.(*JiraService).GetIssue
```

### GitHub Service
```bash
# Set breakpoint in GitHub service
(dlv) break services.NewGitHubService

# Set breakpoint in PR operations
(dlv) break services.(*GitHubService).CreatePullRequest
```

### AI Service
```bash
# Set breakpoint in AI service calls
(dlv) break services.(*ClaudeService).ProcessIssue
(dlv) break services.(*GeminiService).ProcessIssue
```

### Scanner Services
```bash
# Set breakpoint in issue scanner
(dlv) break services.(*JiraIssueScannerService).Start

# Set breakpoint in PR feedback scanner
(dlv) break services.(*PRFeedbackScannerService).Start
```

## Debugging Configuration Issues

### Check Config Loading
```bash
# Set breakpoint in config loading
(dlv) break models.LoadConfig
```

### Debug Environment Variables
```bash
# Print config after loading
(dlv) print config
```

## Debugging HTTP Server

### Set breakpoint in HTTP handlers
```bash
# Set breakpoint in health check handler
(dlv) break main.go:140
```

## Debugging Goroutines

### Monitor goroutines
```bash
# Show all goroutines
(dlv) goroutines

# Switch to specific goroutine
(dlv) goroutine 1
```

## Common Issues and Solutions

### 1. Program exits immediately
- Check if config file exists and is valid
- Set breakpoint at `main()` function
- Check for fatal errors in config validation

### 2. Can't connect to services
- Set breakpoints in service initialization
- Check API tokens and URLs
- Monitor network requests

### 3. AI service not responding
- Set breakpoints in AI service methods
- Check API keys and endpoints
- Monitor request/response data

### 4. Infinite loops or hanging
- Use `goroutines` command to see all running goroutines
- Set breakpoints in scanner services
- Check for deadlocks in channel operations

## Performance Debugging

### CPU Profiling
```bash
# Run with CPU profiling
go run -cpuprofile=cpu.prof main.go -config config.yaml

# Analyze profile
go tool pprof cpu.prof
```

### Memory Profiling
```bash
# Run with memory profiling
go run -memprofile=mem.prof main.go -config config.yaml

# Analyze profile
go tool pprof mem.prof
```

## Logging for Debugging

The application uses structured logging with Zap. You can:

1. Set log level to `debug` in config
2. Use JSON format for better parsing
3. Monitor logs in real-time

```yaml
logging:
  level: debug
  format: json
```

## Tips and Best Practices

1. **Start with main function**: Set breakpoint at `main.main` to see program startup
2. **Use conditional breakpoints**: Set breakpoints that only trigger under specific conditions
3. **Monitor goroutines**: Use `goroutines` command to see concurrent operations
4. **Check error handling**: Set breakpoints in error handling code
5. **Use print statements**: Combine debugging with strategic `fmt.Printf` statements
6. **Profile performance**: Use Go's built-in profiling tools for performance issues

## Troubleshooting

### Delve not working
```bash
# Reinstall Delve
go install github.com/go-delve/delve/cmd/dlv@latest

# Check Go version
go version
```

### VS Code debugging issues
1. Ensure Go extension is installed and updated
2. Check `go.mod` file is in workspace root
3. Restart VS Code if needed
4. Check Go tools are installed: `Ctrl+Shift+P` → "Go: Install/Update Tools"

### Permission issues
```bash
# If you get permission errors with Delve
sudo sysctl -w kernel.yama.ptrace_scope=0
``` 