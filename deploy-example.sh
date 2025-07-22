#!/bin/bash

# Example deployment script for jira-ai-issue-solver
# This shows how to use the Make-based deployment system

echo "🚀 Jira AI Issue Solver - Example Deployment"
echo "============================================"
echo ""

# Set your configuration here
PROJECT_ID="your-gcp-project-id"
REGION="your-preferred-region"
SERVICE_NAME="your-service-name"

echo "Deploying with configuration:"
echo "  Project ID: $PROJECT_ID"
echo "  Region: $REGION"
echo "  Service Name: $SERVICE_NAME"
echo ""

# Run the deployment
make deploy PROJECT_ID="$PROJECT_ID" REGION="$REGION" SERVICE_NAME="$SERVICE_NAME"

echo ""
echo "✅ Deployment completed!"
echo ""
echo "To monitor your service:"
echo "  gcloud run services describe $SERVICE_NAME --region=$REGION --format='value(status.url)'"
echo ""
echo "To view logs:"
echo "  gcloud beta logging tail \"resource.type=cloud_run_revision AND resource.labels.service_name=$SERVICE_NAME\" --project=$PROJECT_ID" 