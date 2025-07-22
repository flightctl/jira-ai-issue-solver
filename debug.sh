#!/bin/bash

# Debug script for jira-ai-issue-solver

set -e

echo "=== Jira AI Issue Solver Debug Script ==="
echo ""

# Check if config file exists
if [ ! -f "config.yaml" ]; then
    echo "❌ config.yaml not found!"
    echo "Please copy config.example.yaml to config.yaml and configure it."
    exit 1
fi

# Check if Go is installed
if ! command -v go &> /dev/null; then
    echo "❌ Go is not installed!"
    exit 1
fi

# Check if Delve is installed
if ! command -v dlv &> /dev/null; then
    echo "Installing Delve debugger..."
    go install github.com/go-delve/delve/cmd/dlv@latest
fi

echo "✅ Environment ready for debugging"
echo ""
echo "Available debugging options:"
echo "1. VS Code: Press F5 or use the debug panel"
echo "2. Command line: make debug"
echo "3. Direct Delve: dlv debug main.go -- -config config.yaml"
echo "4. Debug tests: make debug-tests"
echo ""
echo "Common Delve commands:"
echo "  break main.main    - Set breakpoint at main function"
echo "  break services/    - Set breakpoint in services package"
echo "  continue (c)       - Continue execution"
echo "  next (n)           - Step over"
echo "  step (s)           - Step into"
echo "  print variable     - Print variable value"
echo "  vars               - Show variables"
echo "  goroutines         - Show goroutines"
echo "  quit (q)           - Exit debugger"
echo ""

# Ask user what they want to do
read -p "Start debugging now? (y/n): " -n 1 -r
echo
if [[ $REPLY =~ ^[Yy]$ ]]; then
    echo "Starting debug session..."
    $HOME/go/bin/dlv debug main.go -- -config config.yaml
fi 