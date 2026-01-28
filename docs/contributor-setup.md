# Contributor Setup Guide

Quick start guide for contributors who want to work on Jira tickets with the
AI bot.

## What is this?

When you're assigned a Jira ticket, the bot can automatically:

1. Create a branch in **your fork**
2. Generate code changes using AI
3. Create a pull request from **your fork** to the main repository
4. Let you collaborate by pushing additional changes to the same branch

## Prerequisites

- You have a fork of the repository (e.g., `yourname/repository`)
- You're assigned Jira tickets in the configured project
- The bot's Jira username is added as a **Contributor** on tickets you want
  processed
  - Example: If the bot's username is `wg-jira-ai-issue-solver`, add it as a
    contributor in Jira
  - This tells the bot which tickets to process
- Tickets are in the configured "todo" status (check with your admin for the
  exact status name)

## Setup (One-Time, ~2 Minutes)

### Step 1: Install the GitHub App on Your Fork

The bot needs permission to push code to your fork.

1. **Get the GitHub App URL** from your admin or team lead
   - Format: `https://github.com/apps/APP-NAME`
   - Example: `https://github.com/apps/bugs-buddy-jira-ai-issue-solver`

2. **Visit the URL** and click **"Install"** or **"Configure"**

3. **Select repositories**:
   - Choose **"Only select repositories"**
   - Select **your fork** (e.g., `yourname/repository`)
   - Click **"Install"**

4. **Verify installation**:
   - Go to your fork's settings: `https://github.com/yourname/repository/settings/installations`
   - You should see the app listed

### Step 2: Notify Your Admin

Tell your admin or bot operator:

- Your **Jira email address** (e.g., `alice@yourcompany.com`)
- Your **GitHub username** (e.g., `alice`)

They need to add this mapping to the bot's configuration so it knows which fork
to use when you're assigned a ticket.

### Step 3: Done! 🎉

You're all set. When you're assigned a Jira ticket:

1. The bot will detect it
2. Create a branch in **your fork**
3. Generate code changes
4. Create a PR from your fork to the main repo
5. You can push additional changes to collaborate

## Working with Bot-Created PRs

When the bot creates a PR from your fork:

```bash
# Clone your fork if you haven't already
git clone https://github.com/yourname/repository.git
cd repository

# Checkout the bot's branch
git checkout jira/PROJ-123

# Make your changes
# ... edit files ...

# Commit and push (goes to YOUR fork, updates the PR)
git add .
git commit -m "Additional changes"
git push origin jira/PROJ-123
```

Both your changes and the bot's changes will appear in the same PR!

## Troubleshooting

### "I was assigned a ticket but the bot didn't process it"

**Possible causes:**

1. The bot's Jira username is not added as a **Contributor** on the ticket →
   Add it in Jira (see Prerequisites above)
2. The ticket is not in the configured "todo" status → Check with your admin
   for the expected status
3. You haven't installed the GitHub App on your fork → Do Step 1 above
4. Your admin hasn't added your email-to-GitHub mapping → Do Step 2 above
5. Your fork doesn't exist → Fork the repository first

**Check with admin**: Ask if they see any errors in the bot logs for your ticket.

### "The bot created a PR but I can't push to the branch"

**Solution:** Make sure you're pushing to **your fork**, not the main repository.

```bash
# Check your remote
git remote -v

# Should show:
# origin  https://github.com/yourname/repository.git

# If it shows the main repo instead, update it:
git remote set-url origin https://github.com/yourname/repository.git
```

### "I don't have a fork yet"

1. Go to the main repository (e.g., `https://github.com/orgname/repository`)
2. Click the **"Fork"** button in the top right
3. Select your personal account as the destination
4. Then do Step 1 above (install the app on your new fork)

## FAQ

**Q: Why do I need to install the app on my fork?**

A: The bot needs permission to push code to your fork. Installing the app grants
it "write" access to your fork only.

**Q: Can I uninstall the app later?**

A: Yes, but then the bot won't be able to create branches in your fork for
future tickets. You can reinstall anytime.

**Q: What permissions does the app have?**

A: Only what you see when installing: typically "Contents: Write" (to push code)
and "Pull Requests: Write" (to create PRs). It can't access other repositories
or perform admin actions.

**Q: Can the bot modify my other branches?**

A: No. The bot only creates new branches (like `jira/PROJ-123`). Your existing
branches are untouched.

**Q: Do I need to do this for every repository?**

A: Yes, if you work with multiple repositories, install the app on each fork
where you want the bot to push code.

## Need Help?

- **Can't find the GitHub App URL?** Ask your admin or team lead
- **Installation issues?** Check your GitHub notifications for any error messages
- **Bot not working?** Ask your admin to check the bot logs

---

**For admins setting up the bot:** See [admin-setup.md](admin-setup.md)

**For understanding the workflow in depth:** See [architecture.md](architecture.md)
