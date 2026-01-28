# GitHub App Setup Instructions for Repository Administrators

This document provides step-by-step instructions for creating and configuring a
GitHub App for the jira-ai-issue-solver bot.

## Overview

The jira-ai-issue-solver uses a GitHub App to automatically create pull requests
and respond to PR review comments. This requires a GitHub App to be created in
your organization and installed on your repository.

## Prerequisites

- Administrator access to the GitHub organization/repository
- Ability to create GitHub Apps in your organization

## Step 1: Create the GitHub App

1. Navigate to your **organization's settings** (not personal settings):
   - Go to `https://github.com/organizations/YOUR_ORG/settings/apps`
   - Or: Click your org → Settings → Developer settings → GitHub Apps

2. Click **"New GitHub App"**

3. Fill in the basic information:
   - **GitHub App name**: `jira-ai-issue-solver` (or similar, must be unique
     across GitHub)
   - **Description**: `Automated Jira ticket processor that creates PRs and
     responds to review feedback`
   - **Homepage URL**: `https://github.com/YOUR_ORG/YOUR_REPO` (your repository
     URL)

4. **Webhook section**:
   - **Uncheck** "Active" (this app doesn't use webhooks)

5. **Permissions - Repository permissions**:

   Set the following permissions:

   <!-- markdownlint-disable MD013 -->
   | Permission        | Access Level   | Why Needed                                 |
   |-------------------|----------------|--------------------------------------------|
   | **Contents**      | Read and write | Clone repos, create branches, push commits |
   | **Pull requests** | Read and write | Create PRs, read reviews, post comments    |
   | **Metadata**      | Read-only      | Required (automatically selected)          |
   <!-- markdownlint-enable MD013 -->

6. **Where can this GitHub App be installed?**
   - Select **"Only on this account"** (restricts to your organization)

7. Click **"Create GitHub App"**

## Step 2: Generate a Private Key

1. After creating the app, you'll be on the app's settings page
   - Or navigate to:
   `https://github.com/organizations/YOUR_ORG/settings/apps/YOUR_APP_NAME`

2. Scroll down to the **"Private keys"** section

3. Click **"Generate a private key"**

4. A `.pem` file will automatically download to your computer
   - **IMPORTANT**: This file is downloaded only once and cannot be retrieved again
   - Keep this file secure - it's essentially a password for the app

## Step 3: Note the App ID

1. On the app settings page, at the very top, you'll see:

   ```text
   App ID: 2591456
   ```

2. **Save this number** - you'll need to provide it to the bot operator

## Step 4: Install the App on Your Repository

1. From the app settings page, click **"Install App"** in the left sidebar

2. Click the **"Install"** button next to your organization name

3. Choose repository access:
   - Select **"Only select repositories"**
   - Choose the repository where PRs will be created (e.g., `flightctl/flightctl`)

4. Click **"Install"**

## Step 5: Provide Information to the Bot Operator

Send the following to the person operating the jira-ai-issue-solver bot:

### Required Information

1. **App ID**: (the number from Step 3, e.g., `2591456`)

2. **Private Key File**: The `.pem` file downloaded in Step 2
   - Send this securely (encrypted email, secure file share, etc.)
   - **DO NOT** commit this to git or share in plain text channels

3. **App Name**: The app name (WITHOUT `[bot]` suffix - added automatically)
   - Format: `YOUR_APP_NAME`
   - Example: `bugs-buddy-jira-ai-issue-solver`

4. **Installation Confirmation**: Confirm which repository/repositories the
   app is installed on

### Example Information to Provide

```text
GitHub App Configuration:
- App ID: 2591456
- App Name: bugs-buddy-jira-ai-issue-solver
- Private Key: [attached: bugs-buddy.2025-01-02.private-key.pem]
- Installed on: flightctl/flightctl
```

## Security Notes

- The private key file (`.pem`) is sensitive - treat it like a password
- Only share it through secure channels
- The bot operator will store it securely in their deployment environment
- You can revoke the key at any time from the app settings page
- You can see when/where the app is being used in the app's "Advanced" →
  "Recent Deliveries" section (even though webhooks are disabled, API usage
  is logged)

## Troubleshooting

### If the bot operator reports authentication errors

1. Verify the app is installed on the correct repository:
   - Go to the app settings → "Install App"
   - Confirm the repository is listed

2. Check permissions are correctly set:
   - Go to app settings → "Permissions & events"
   - Verify Contents and Pull requests have Read & write access

3. Verify the private key hasn't been revoked:
   - Go to app settings → scroll to "Private keys"
   - Ensure at least one key is active

### To rotate the private key (for security)

1. Generate a new private key (Step 2)
2. Provide the new key to the bot operator
3. After they've updated their configuration, revoke the old key

## Questions?

Contact the bot operator if you have questions about:

- What the bot does
- Why specific permissions are needed
- How the bot will use the app
