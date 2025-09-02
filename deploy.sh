#!/bin/bash

# Comprehensive deployment script for jira-ai-issue-solver to Cloud Run
# Automatically creates secrets from config.yaml and deploys with secure configuration

set -e

# Configuration - these are passed from the Makefile
PROJECT_ID="${PROJECT_ID}"
REGION="${REGION}"
SERVICE_NAME="${SERVICE_NAME}"

# Calculate image name from other parameters
IMAGE_NAME="${REGION}-docker.pkg.dev/${PROJECT_ID}/jira-ai-issue-solver/jira-ai-issue-solver:v1"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Parse command line arguments first
SKIP_SECRETS=false
SKIP_DEPLOY=false
SKIP_TEST=false

while [[ $# -gt 0 ]]; do
    case $1 in
        --skip-secrets)
            SKIP_SECRETS=true
            shift
            ;;
        --skip-deploy)
            SKIP_DEPLOY=true
            shift
            ;;
        --skip-test)
            SKIP_TEST=true
            shift
            ;;
        --help|-h)
            echo "Usage: $0 [OPTIONS]"
            echo ""
            echo "This script is typically called from Make with required environment variables:"
            echo "  PROJECT_ID, REGION, SERVICE_NAME"
            echo "  (IMAGE_NAME is calculated automatically)"
            echo ""
            echo "Options:"
            echo "  --skip-secrets    Skip creating/updating secrets"
            echo "  --skip-deploy     Skip deploying to Cloud Run"
            echo "  --skip-test       Skip testing the deployment"
            echo "  --help, -h        Show this help message"
            echo ""
            echo "Default behavior: Creates secrets from config.yaml, deploys, and tests"
            echo ""
            echo "Example Make usage:"
            echo "  make deploy PROJECT_ID=your-project-id REGION=your-region SERVICE_NAME=your-service-name"
            exit 0
            ;;
        *)
            print_error "Unknown option: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
done

# Validate required parameters
if [ -z "$PROJECT_ID" ]; then
    print_error "PROJECT_ID is required"
    exit 1
fi

if [ -z "$REGION" ]; then
    print_error "REGION is required"
    exit 1
fi

if [ -z "$SERVICE_NAME" ]; then
    print_error "SERVICE_NAME is required"
    exit 1
fi



# Function to create secrets from config.yaml
create_secrets_from_config() {
    print_status "Creating secrets from config.yaml..."
    
    # Enable Secret Manager API if not already enabled
    print_status "Enabling Secret Manager API..."
    gcloud services enable secretmanager.googleapis.com --project=${PROJECT_ID} >/dev/null 2>&1 || true
    
    # Extract values from config.yaml
    print_status "Reading configuration from config.yaml..."
    
    # Extract Jira API Token
    JIRA_TOKEN=$(yq eval '.jira.api_token' config.yaml)
    if [ -z "$JIRA_TOKEN" ]; then
        print_error "Jira API token not found in config.yaml"
        return 1
    fi
    
    # Extract GitHub Personal Access Token
    GITHUB_TOKEN=$(yq eval '.github.personal_access_token' config.yaml)
    if [ -z "$GITHUB_TOKEN" ]; then
        print_error "GitHub Personal Access Token not found in config.yaml"
        return 1
    fi
    
    # Extract Gemini API Key
    GEMINI_KEY=$(yq eval '.gemini.api_key' config.yaml)
    if [ -z "$GEMINI_KEY" ]; then
        print_error "Gemini API Key not found in config.yaml"
        return 1
    fi
    
    print_status "Found secrets in config.yaml (showing first 10 chars):"
    echo "  Jira API Token: ${JIRA_TOKEN:0:10}..."
    echo "  GitHub Token: ${GITHUB_TOKEN:0:10}..."
    echo "  Gemini API Key: ${GEMINI_KEY:0:10}..."
    
    # Create secrets (remove any newlines to prevent issues)
    print_status "Creating secrets in Secret Manager..."
    
    # Jira API Token
    echo "$JIRA_TOKEN" | tr -d '\n' | gcloud secrets create jira-api-token --data-file=- --project=${PROJECT_ID} 2>/dev/null || \
    echo "$JIRA_TOKEN" | tr -d '\n' | gcloud secrets versions add jira-api-token --data-file=- --project=${PROJECT_ID} >/dev/null 2>&1
    print_success "Jira API token secret created/updated"
    
    # GitHub Personal Access Token
    echo "$GITHUB_TOKEN" | tr -d '\n' | gcloud secrets create github-token --data-file=- --project=${PROJECT_ID} 2>/dev/null || \
    echo "$GITHUB_TOKEN" | tr -d '\n' | gcloud secrets versions add github-token --data-file=- --project=${PROJECT_ID} >/dev/null 2>&1
    print_success "GitHub token secret created/updated"
    
    # Gemini API Key
    echo "$GEMINI_KEY" | tr -d '\n' | gcloud secrets create gemini-api-key --data-file=- --project=${PROJECT_ID} 2>/dev/null || \
    echo "$GEMINI_KEY" | tr -d '\n' | gcloud secrets versions add gemini-api-key --data-file=- --project=${PROJECT_ID} >/dev/null 2>&1
    print_success "Gemini API key secret created/updated"
    
    print_success "All secrets created successfully!"
}

# Function to parse environment variables from config.yaml
parse_env_vars_from_config() {
    print_status "Parsing environment variables from config.yaml..."
    
    # Check if yq is available
    if ! command -v yq &> /dev/null; then
        print_error "yq is required for YAML parsing. Please install it first."
        exit 1
    fi
    
    # Extract values from config.yaml using yq for proper YAML parsing
    # Store in global variables for use in deployment
    export SERVER_PORT=$(yq eval '.server.port' config.yaml)
    export LOGGING_LEVEL=$(yq eval '.logging.level' config.yaml)
    export LOGGING_FORMAT=$(yq eval '.logging.format' config.yaml)
    export AI_PROVIDER=$(yq eval '.ai_provider' config.yaml)
    export JIRA_BASE_URL=$(yq eval '.jira.base_url' config.yaml)
    export JIRA_USERNAME=$(yq eval '.jira.username' config.yaml)
    export JIRA_INTERVAL_SECONDS=$(yq eval '.jira.interval_seconds' config.yaml)

    export GITHUB_BOT_USERNAME=$(yq eval '.github.bot_username' config.yaml)
    export GITHUB_BOT_EMAIL=$(yq eval '.github.bot_email' config.yaml)
    export GITHUB_TARGET_BRANCH=$(yq eval '.github.target_branch' config.yaml)
    export GITHUB_PR_LABEL=$(yq eval '.github.pr_label' config.yaml)
    export CLAUDE_CLI_PATH=$(yq eval '.claude.cli_path' config.yaml)
    export CLAUDE_TIMEOUT=$(yq eval '.claude.timeout' config.yaml)
    export CLAUDE_DANGEROUSLY_SKIP_PERMISSIONS=$(yq eval '.claude.dangerously_skip_permissions' config.yaml)
    export GEMINI_CLI_PATH=$(yq eval '.gemini.cli_path' config.yaml)
    export GEMINI_TIMEOUT=$(yq eval '.gemini.timeout' config.yaml)
    export GEMINI_MODEL=$(yq eval '.gemini.model' config.yaml)
    export GEMINI_ALL_FILES=$(yq eval '.gemini.all_files' config.yaml)
    export GEMINI_SANDBOX=$(yq eval '.gemini.sandbox' config.yaml)
    export TEMP_DIR=$(yq eval '.temp_dir' config.yaml)

    
    print_success "Environment variables parsed from config.yaml"
}

# Function to deploy to Cloud Run
deploy_to_cloud_run() {
    print_status "Deploying to Cloud Run..."
    
    # Grant Secret Manager access to Cloud Run service account
    print_status "Setting up IAM permissions..."
    gcloud projects add-iam-policy-binding ${PROJECT_ID} \
        --member="serviceAccount:289883336294-compute@developer.gserviceaccount.com" \
        --role="roles/secretmanager.secretAccessor" >/dev/null 2>&1 || true
    
    # Deploy to Cloud Run
    print_status "Deploying service..."
    gcloud run deploy ${SERVICE_NAME} \
        --image=${IMAGE_NAME} \
        --region=${REGION} \
        --project=${PROJECT_ID} \
        --port=8080 \
        --memory=1Gi \
        --cpu=1 \
        --max-instances=10 \
        --min-instances=0 \
        --timeout=300 \
        --concurrency=80 \
        --set-env-vars="JIRA_AI_SERVER_PORT=${SERVER_PORT}" \
        --set-env-vars="JIRA_AI_LOGGING_LEVEL=${LOGGING_LEVEL}" \
        --set-env-vars="JIRA_AI_LOGGING_FORMAT=${LOGGING_FORMAT}" \
        --set-env-vars="JIRA_AI_AI_PROVIDER=${AI_PROVIDER}" \
        --set-env-vars="JIRA_AI_JIRA_BASE_URL=${JIRA_BASE_URL}" \
        --set-env-vars="JIRA_AI_JIRA_USERNAME=${JIRA_USERNAME}" \
        --set-env-vars="JIRA_AI_JIRA_INTERVAL_SECONDS=${JIRA_INTERVAL_SECONDS}" \

        --set-env-vars="JIRA_AI_GITHUB_BOT_USERNAME=${GITHUB_BOT_USERNAME}" \
        --set-env-vars="JIRA_AI_GITHUB_BOT_EMAIL=${GITHUB_BOT_EMAIL}" \
        --set-env-vars="JIRA_AI_GITHUB_TARGET_BRANCH=${GITHUB_TARGET_BRANCH}" \
        --set-env-vars="JIRA_AI_GITHUB_PR_LABEL=${GITHUB_PR_LABEL}" \
        --set-env-vars="JIRA_AI_CLAUDE_CLI_PATH=${CLAUDE_CLI_PATH}" \
        --set-env-vars="JIRA_AI_CLAUDE_TIMEOUT=${CLAUDE_TIMEOUT}" \
        --set-env-vars="JIRA_AI_CLAUDE_DANGEROUSLY_SKIP_PERMISSIONS=${CLAUDE_DANGEROUSLY_SKIP_PERMISSIONS}" \
        --set-env-vars="JIRA_AI_GEMINI_CLI_PATH=${GEMINI_CLI_PATH}" \
        --set-env-vars="JIRA_AI_GEMINI_TIMEOUT=${GEMINI_TIMEOUT}" \
        --set-env-vars="JIRA_AI_GEMINI_MODEL=${GEMINI_MODEL}" \
        --set-env-vars="JIRA_AI_GEMINI_ALL_FILES=${GEMINI_ALL_FILES}" \
        --set-env-vars="JIRA_AI_GEMINI_SANDBOX=${GEMINI_SANDBOX}" \

        --set-env-vars="JIRA_AI_TEMP_DIR=${TEMP_DIR}" \
        --set-secrets="JIRA_AI_JIRA_API_TOKEN=jira-api-token:latest" \
        --set-secrets="JIRA_AI_GITHUB_PERSONAL_ACCESS_TOKEN=github-token:latest" \
        --set-secrets="JIRA_AI_GEMINI_API_KEY=gemini-api-key:latest" \
        --no-allow-unauthenticated
    
    print_success "Deployment completed successfully!"
    
    # Get service URL
    SERVICE_URL=$(gcloud run services describe ${SERVICE_NAME} --region=${REGION} --format="value(status.url)")
    print_success "Service URL: ${SERVICE_URL}"
}

# Function to test the deployment
test_deployment() {
    print_status "Testing deployment..."
    
    SERVICE_URL=$(gcloud run services describe ${SERVICE_NAME} --region=${REGION} --format="value(status.url)")
    
    # Test health endpoint
    HEALTH_RESPONSE=$(curl -s -H "Authorization: Bearer $(gcloud auth print-identity-token)" "${SERVICE_URL}/health" || echo "FAILED")
    
    if [ "$HEALTH_RESPONSE" = "OK" ]; then
        print_success "Health check passed: ${HEALTH_RESPONSE}"
    else
        print_error "Health check failed: ${HEALTH_RESPONSE}"
        return 1
    fi
}

# Security check function
security_check() {
    print_status "Running security checks..."
    
    # Check if config.yaml is ignored by git
    if ! git check-ignore config.yaml >/dev/null 2>&1; then
        print_error "SECURITY WARNING: config.yaml is not ignored by git!"
        print_error "This could lead to secrets being committed to version control."
        exit 1
    fi
    
    
    print_success "Security checks passed"
}

# Main deployment function
main() {
    echo "🚀 Jira AI Issue Solver - Cloud Run Deployment"
    echo "=============================================="
    echo "Configuration:"
    echo "  Project ID: ${PROJECT_ID}"
    echo "  Region: ${REGION}"
    echo "  Service Name: ${SERVICE_NAME}"
    echo "  Image Name: ${IMAGE_NAME}"
    echo ""
    
    # Run security checks
    security_check
    
    # Check if config.yaml exists
    if [ ! -f "config.yaml" ]; then
        print_error "config.yaml not found in current directory"
        exit 1
    fi
    
    # Create secrets from config.yaml
    if [ "$SKIP_SECRETS" = false ]; then
        create_secrets_from_config
    else
        print_status "Skipping secrets creation (--skip-secrets)"
    fi
    
    # Parse environment variables from config.yaml
    parse_env_vars_from_config
    
    # Deploy to Cloud Run
    if [ "$SKIP_DEPLOY" = false ]; then
        deploy_to_cloud_run
    else
        print_status "Skipping deployment (--skip-deploy)"
    fi
    
    # Test the deployment
    if [ "$SKIP_TEST" = false ]; then
        test_deployment
    else
        print_status "Skipping deployment test (--skip-test)"
    fi
    
    echo ""
    print_success "🎉 Deployment completed successfully!"
    echo ""
    echo "📋 Summary:"
    echo "  • Secrets created/updated from config.yaml"
    echo "  • Environment variables parsed from config.yaml"
    echo "  • Service deployed to Cloud Run"
    echo "  • Health check passed"
    echo ""
    echo "🔗 Service URL: $(gcloud run services describe ${SERVICE_NAME} --region=${REGION} --format="value(status.url)")"
    echo "📊 Monitor logs: gcloud beta logging tail \"resource.type=cloud_run_revision AND resource.labels.service_name=${SERVICE_NAME}\" --project=${PROJECT_ID}"
}

# Run main function
main 