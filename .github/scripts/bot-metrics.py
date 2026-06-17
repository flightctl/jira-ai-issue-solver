#!/usr/bin/env python3
"""Collect adoption metrics for the AI issue solver bot.

Runs in CI (GitHub Actions) or locally with `gh` CLI authenticated.
Outputs a markdown report to the GitHub Actions job summary, or stdout.

Configuration via environment variables:
  TARGET_REPOS     - Comma-separated GitHub repos to scan (e.g., "flightctl/flightctl")
  BOT_AUTHOR       - GitHub login of the bot (default: app/bugs-buddy-jira-ai-issue-solver)
  JIRA_PROJECTS    - Comma-separated Jira project prefixes to include (e.g., "EDM,PROJ").
                     Empty = all projects (default).
  EXCLUDED_TICKETS - Comma-separated ticket keys to exclude (default: "")
  START_DATE       - YYYY-MM-DD start date for data collection; PRs before this are excluded
                     (default: 2026-03-23)
"""

import json
import os
import subprocess
import sys
from collections import Counter, defaultdict
from datetime import datetime, timezone, date

BOT_AUTHOR = os.environ.get(
    "BOT_AUTHOR", "app/bugs-buddy-jira-ai-issue-solver"
)
TARGET_REPOS = [
    r.strip()
    for r in os.environ.get("TARGET_REPOS", "").split(",")
    if r.strip()
]
JIRA_PROJECTS = [
    p.strip().upper()
    for p in os.environ.get("JIRA_PROJECTS", "").split(",")
    if p.strip()
]
EXCLUDED_TICKETS = [
    t.strip()
    for t in os.environ.get("EXCLUDED_TICKETS", "").split(",")
    if t.strip()
]
START_DATE = date.fromisoformat(
    os.environ.get("START_DATE", "2026-03-23")
)


def run_gh(*args):
    """Run a gh CLI command and return parsed JSON."""
    result = subprocess.run(
        ["gh", *args], capture_output=True, text=True, check=True
    )
    return json.loads(result.stdout) if result.stdout.strip() else []


def parse_time(iso_str):
    """Parse an ISO-8601 timestamp from the GitHub API."""
    if not iso_str:
        return None
    return datetime.fromisoformat(iso_str.replace("Z", "+00:00"))


def extract_ticket(title):
    """Extract ticket key from PR title (e.g., 'EDM-1234: ...' -> 'EDM-1234')."""
    colon_idx = title.find(":")
    if colon_idx == -1:
        return None
    candidate = title[:colon_idx].strip()
    if "-" in candidate and any(c.isdigit() for c in candidate):
        return candidate
    return None


def extract_project(ticket_key):
    """Extract the Jira project prefix from a ticket key (e.g., 'EDM-1234' -> 'EDM')."""
    if not ticket_key:
        return None
    dash_idx = ticket_key.find("-")
    if dash_idx == -1:
        return None
    return ticket_key[:dash_idx].upper()


def classify_size(additions, deletions):
    """Classify a PR by total lines changed."""
    total = additions + deletions
    if total <= 10:
        return "XS (1-10)"
    if total <= 50:
        return "S (11-50)"
    if total <= 200:
        return "M (51-200)"
    if total <= 500:
        return "L (201-500)"
    return "XL (500+)"


def ci_passed(status_checks):
    """Determine CI pass/fail from statusCheckRollup."""
    if not status_checks:
        return None
    for check in status_checks:
        conclusion = (check.get("conclusion") or "").upper()
        if conclusion == "FAILURE":
            return False
    return True


def fetch_prs_for_repo(repo):
    """Fetch all bot PRs from a single repo (metadata only)."""
    fields = ",".join([
        "number", "title", "state", "createdAt", "mergedAt", "closedAt",
        "additions", "deletions", "reviews",
    ])
    prs = run_gh(
        "pr", "list",
        "--repo", repo,
        "--state", "all",
        "--author", BOT_AUTHOR,
        "--limit", "500",
        "--json", fields,
    )
    # Tag each PR with its source repo
    for pr in prs:
        pr["_repo"] = repo
    return prs


def fetch_all_prs():
    """Fetch bot PRs across all target repos."""
    all_prs = []
    for repo in TARGET_REPOS:
        print(f"  Fetching from {repo}...", file=sys.stderr)
        all_prs.extend(fetch_prs_for_repo(repo))
    return all_prs


def fetch_ci_status(prs):
    """Fetch CI check status for specific PRs individually.

    statusCheckRollup is too large to fetch in bulk (causes GitHub API
    timeouts), so we query one PR at a time.
    """
    results = {}
    for pr in prs:
        repo = pr["_repo"]
        num = pr["number"]
        key = (repo, num)
        try:
            data = run_gh(
                "pr", "view", str(num),
                "--repo", repo,
                "--json", "number,statusCheckRollup",
            )
            results[key] = data.get("statusCheckRollup", [])
        except subprocess.CalledProcessError:
            results[key] = []
    return results


COST_MARKER = "<!-- AI-BOT-COST -->"


def fetch_pr_cost(repo, pr_number):
    """Fetch the AI session cost from a PR's issue comments.

    Looks for a comment containing the AI-BOT-COST marker and parses
    the total from the markdown table. Returns 0.0 if no cost comment
    is found or parsing fails.
    """
    try:
        result = subprocess.run(
            ["gh", "api", f"repos/{repo}/issues/{pr_number}/comments",
             "--paginate", "--jq", "[.[].body]"],
            capture_output=True, text=True, check=True,
        )
        bodies = []
        for line in result.stdout.strip().split("\n"):
            if line:
                bodies.extend(json.loads(line))
    except (subprocess.CalledProcessError, json.JSONDecodeError):
        return 0.0

    for body in bodies:
        if COST_MARKER not in body:
            continue
        for line in body.split("\n"):
            if "**Total**" in line:
                parts = line.split("|")
                if len(parts) >= 3:
                    cost_str = parts[2].strip().strip("*").strip("$").strip("*")
                    try:
                        return float(cost_str)
                    except ValueError:
                        pass
    return 0.0


def filter_prs(prs):
    """Exclude PRs before start date, test tickets, and non-matching projects."""
    excluded_count = 0
    filtered_count = 0
    result = []

    for pr in prs:
        ticket = extract_ticket(pr["title"])

        # Exclude specific tickets
        if ticket and ticket in EXCLUDED_TICKETS:
            excluded_count += 1
            continue

        # Apply Jira project filter
        if JIRA_PROJECTS:
            project = extract_project(ticket)
            if project not in JIRA_PROJECTS:
                filtered_count += 1
                continue

        # Exclude PRs before start date
        created = parse_time(pr["createdAt"])
        if created and created.date() < START_DATE:
            continue

        result.append(pr)

    return result, excluded_count, filtered_count


def compute_metrics(prs, ci_status, pr_costs=None):
    """Compute adoption metrics from a list of PRs. Returns None if empty."""
    if not prs:
        return None
    if pr_costs is None:
        pr_costs = {}

    total = len(prs)
    merged = [p for p in prs if p["state"] == "MERGED"]
    closed = [p for p in prs if p["state"] == "CLOSED"]
    open_prs = [p for p in prs if p["state"] == "OPEN"]

    # -- Ticket-level aggregation --
    tickets = defaultdict(list)
    for pr in prs:
        ticket = extract_ticket(pr["title"]) or f"(no ticket) PR#{pr['number']}"
        tickets[ticket].append(pr)

    resolved = {
        t for t, t_prs in tickets.items()
        if any(p["state"] == "MERGED" for p in t_prs)
    }

    # -- CI pass rate --
    ci_results = [
        ci_passed(ci_status.get((pr["_repo"], pr["number"])))
        for pr in prs
    ]
    ci_known = [r for r in ci_results if r is not None]
    ci_pass_count = sum(1 for r in ci_known if r)

    # -- Merge time --
    merge_hours = []
    for pr in merged:
        created = parse_time(pr["createdAt"])
        merged_at = parse_time(pr["mergedAt"])
        if created and merged_at:
            merge_hours.append((merged_at - created).total_seconds() / 3600)

    # -- Review counts on merged PRs --
    review_counts = [len(pr.get("reviews") or []) for pr in merged]

    # -- Size distribution --
    size_dist = Counter(
        classify_size(pr.get("additions", 0), pr.get("deletions", 0))
        for pr in prs
    )

    # -- Monthly trend --
    monthly = defaultdict(lambda: {"total": 0, "merged": 0, "closed": 0, "open": 0})
    for pr in prs:
        month = pr["createdAt"][:7]
        monthly[month]["total"] += 1
        monthly[month][pr["state"].lower()] += 1

    # -- Per-ticket detail --
    ticket_rows = []
    total_cost = 0.0
    for ticket, t_prs in sorted(tickets.items()):
        m = sum(1 for p in t_prs if p["state"] == "MERGED")
        c = sum(1 for p in t_prs if p["state"] == "CLOSED")
        o = sum(1 for p in t_prs if p["state"] == "OPEN")
        ci_ok = sum(
            1 for p in t_prs
            if ci_passed(ci_status.get((p["_repo"], p["number"]))) is True
        )
        ci_total = sum(
            1 for p in t_prs
            if ci_passed(ci_status.get((p["_repo"], p["number"]))) is not None
        )
        ticket_cost = sum(
            pr_costs.get((p["_repo"], p["number"]), 0.0) for p in t_prs
        )
        total_cost += ticket_cost
        # Show repo if multiple target repos
        repo_suffix = ""
        if len(TARGET_REPOS) > 1:
            repos = sorted({p["_repo"] for p in t_prs})
            repo_suffix = f" ({', '.join(r.split('/')[-1] for r in repos)})"
        ticket_rows.append({
            "ticket": ticket + repo_suffix,
            "total": len(t_prs),
            "merged": m,
            "closed": c,
            "open": o,
            "resolved": ticket in resolved,
            "ci_pass": f"{ci_ok}/{ci_total}" if ci_total else "n/a",
            "cost": ticket_cost,
        })

    prs_per_ticket_vals = [len(t_prs) for t_prs in tickets.values()]

    # -- Per-repo breakdown (only if multiple repos) --
    repo_breakdown = {}
    if len(TARGET_REPOS) > 1:
        for repo in TARGET_REPOS:
            repo_prs = [p for p in prs if p["_repo"] == repo]
            if repo_prs:
                repo_merged = sum(1 for p in repo_prs if p["state"] == "MERGED")
                repo_breakdown[repo] = {
                    "total": len(repo_prs),
                    "merged": repo_merged,
                    "merge_rate": repo_merged / len(repo_prs) * 100,
                }

    tickets_with_cost = [t for t in ticket_rows if t["cost"] > 0]
    return {
        "total_prs": total,
        "merged": len(merged),
        "closed": len(closed),
        "open": len(open_prs),
        "merge_rate": len(merged) / total * 100,
        "unique_tickets": len(tickets),
        "resolved_tickets": len(resolved),
        "resolution_rate": len(resolved) / len(tickets) * 100 if tickets else 0,
        "avg_prs_per_ticket": (
            sum(prs_per_ticket_vals) / len(prs_per_ticket_vals)
            if prs_per_ticket_vals else 0
        ),
        "merged_additions": sum(p.get("additions", 0) for p in merged),
        "merged_deletions": sum(p.get("deletions", 0) for p in merged),
        "avg_reviews_merged": (
            sum(review_counts) / len(review_counts) if review_counts else 0
        ),
        "avg_merge_hours": (
            sum(merge_hours) / len(merge_hours) if merge_hours else None
        ),
        "ci_pass_rate": (
            ci_pass_count / len(ci_known) * 100 if ci_known else None
        ),
        "ci_total_checked": len(ci_known),
        "size_distribution": dict(size_dist),
        "monthly_trend": dict(monthly),
        "ticket_details": ticket_rows,
        "repo_breakdown": repo_breakdown,
        "total_cost": total_cost,
        "avg_cost_per_ticket": (
            total_cost / len(tickets_with_cost) if tickets_with_cost else 0
        ),
    }


def format_merge_time(hours):
    """Format hours as a human-readable duration."""
    if hours is None:
        return "n/a"
    if hours > 48:
        return f"{hours / 24:.1f} days"
    return f"{hours:.1f} hours"


def format_summary_table(m):
    """Render a single-column summary of metrics."""
    lines = []

    def val(key, fmt=None):
        v = m[key]
        if v is None:
            return "—"
        if fmt:
            return fmt(v)
        return str(v)

    def pct(key):
        return val(key, lambda v: f"{v:.1f}%")

    ci_str = "—"
    if m["ci_pass_rate"] is not None:
        ci_str = f"{m['ci_pass_rate']:.1f}% ({m['ci_total_checked']} checked)"

    lines.append("## Summary")
    lines.append("")
    lines.append("| Metric | Value |")
    lines.append("|--------|-------|")
    lines.append(f"| Total PRs | {val('total_prs')} |")
    lines.append(f"| Merged | {val('merged')} |")
    lines.append(f"| Closed (rejected) | {val('closed')} |")
    lines.append(f"| Open | {val('open')} |")
    lines.append(f"| **Merge rate** | **{pct('merge_rate')}** |")
    lines.append(f"| CI pass rate | {ci_str} |")
    lines.append(f"| Unique tickets | {val('unique_tickets')} |")
    lines.append(f"| Tickets resolved | {val('resolved_tickets')} |")
    lines.append(f"| **Resolution rate** | **{pct('resolution_rate')}** |")
    lines.append(f"| Avg PRs per ticket | {val('avg_prs_per_ticket', lambda v: f'{v:.1f}')} |")
    lines.append(f"| Avg reviews (merged) | {val('avg_reviews_merged', lambda v: f'{v:.1f}')} |")
    lines.append(f"| Avg time to merge | {format_merge_time(m['avg_merge_hours'])} |")
    lines.append(f"| Lines added (merged) | {val('merged_additions', lambda v: f'+{v}')} |")
    lines.append(f"| Lines removed (merged) | {val('merged_deletions', lambda v: f'-{v}')} |")
    lines.append(f"| **Total AI cost** | **{val('total_cost', lambda v: f'${v:.2f}')}** |")
    lines.append(f"| Avg cost per ticket | {val('avg_cost_per_ticket', lambda v: f'${v:.2f}')} |")
    lines.append("")

    return lines


def format_detail_sections(m):
    """Render per-ticket breakdown, monthly trend, and repo breakdown."""
    lines = []

    # -- Per-repo breakdown (multi-repo only) --
    if m["repo_breakdown"]:
        lines.append("### Per-Repo Breakdown")
        lines.append("")
        lines.append("| Repository | PRs | Merged | Merge Rate |")
        lines.append("|------------|-----|--------|------------|")
        for repo in sorted(m["repo_breakdown"]):
            d = m["repo_breakdown"][repo]
            lines.append(
                f"| {repo} | {d['total']} | {d['merged']} | {d['merge_rate']:.1f}% |"
            )
        lines.append("")

    # -- PR size distribution --
    lines.append("### PR Size Distribution")
    lines.append("")
    lines.append("| Size | Count |")
    lines.append("|------|-------|")
    for size in ["XS (1-10)", "S (11-50)", "M (51-200)", "L (201-500)", "XL (500+)"]:
        count = m["size_distribution"].get(size, 0)
        if count > 0:
            lines.append(f"| {size} | {count} |")
    lines.append("")

    # -- Monthly trend --
    if m["monthly_trend"]:
        lines.append("### Monthly Trend")
        lines.append("")
        lines.append("| Month | Total | Merged | Closed | Open | Merge Rate |")
        lines.append("|-------|-------|--------|--------|------|------------|")
        for month in sorted(m["monthly_trend"]):
            d = m["monthly_trend"][month]
            rate = d["merged"] / d["total"] * 100 if d["total"] else 0
            lines.append(
                f"| {month} | {d['total']} | {d['merged']} "
                f"| {d['closed']} | {d['open']} | {rate:.0f}% |"
            )
        lines.append("")

    # -- Per-ticket breakdown --
    lines.append("### Per-Ticket Breakdown")
    lines.append("")
    lines.append("| Ticket | PRs | Merged | Closed | Open | CI Pass | Cost | Resolved |")
    lines.append("|--------|-----|--------|--------|------|---------|------|----------|")
    for t in sorted(m["ticket_details"], key=lambda x: x["total"], reverse=True):
        resolved = "Yes" if t["resolved"] else "No"
        cost_str = f"${t['cost']:.2f}" if t.get("cost", 0) > 0 else "—"
        lines.append(
            f"| {t['ticket']} | {t['total']} | {t['merged']} "
            f"| {t['closed']} | {t['open']} | {t['ci_pass']} | {cost_str} | {resolved} |"
        )
    lines.append("")

    return lines


def format_report(metrics, excluded_count, filtered_count):
    """Render the metrics report."""
    lines = []
    now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")

    lines.append("# AI Bot Adoption Metrics")
    lines.append("")
    meta = [
        f"*Generated: {now}*",
        f"*Repos: {', '.join(TARGET_REPOS)}*",
        f"*Data collected since: {START_DATE}*",
    ]
    if JIRA_PROJECTS:
        meta.append(f"*Jira projects: {', '.join(JIRA_PROJECTS)}*")
    if excluded_count:
        meta.append(
            f"*Excluded {excluded_count} PRs from test tickets: "
            f"{', '.join(EXCLUDED_TICKETS)}*"
        )
    if filtered_count:
        meta.append(
            f"*Filtered {filtered_count} PRs not matching project filter*"
        )
    lines.append("  \n".join(meta))
    lines.append("")

    lines.extend(format_summary_table(metrics))
    lines.extend(format_detail_sections(metrics))

    return "\n".join(lines)


def write_output(key, value):
    """Write a key=value pair to GITHUB_OUTPUT if available."""
    path = os.environ.get("GITHUB_OUTPUT")
    if path:
        with open(path, "a") as f:
            f.write(f"{key}={value}\n")


def main():
    if not TARGET_REPOS:
        print("ERROR: TARGET_REPOS is required (comma-separated list of owner/repo).")
        sys.exit(1)

    print("Fetching bot PRs...", file=sys.stderr)
    prs = fetch_all_prs()
    if not prs:
        print("No bot PRs found.")
        sys.exit(0)
    print(f"Found {len(prs)} total bot PRs", file=sys.stderr)

    filtered_prs, excluded_count, filtered_count = filter_prs(prs)
    print(
        f"Included: {len(filtered_prs)}, "
        f"Excluded: {excluded_count}, Filtered: {filtered_count}",
        file=sys.stderr,
    )

    if not filtered_prs:
        print(
            f"No PRs remaining after filtering "
            f"({excluded_count} excluded, {filtered_count} filtered)."
        )
        sys.exit(0)

    print(
        f"Fetching CI status for {len(filtered_prs)} PRs...",
        file=sys.stderr,
    )
    ci_status = fetch_ci_status(filtered_prs)

    print(
        f"Fetching AI cost data for {len(filtered_prs)} PRs...",
        file=sys.stderr,
    )
    pr_costs = {}
    for pr in filtered_prs:
        repo = pr["_repo"]
        num = pr["number"]
        cost = fetch_pr_cost(repo, num)
        if cost > 0:
            pr_costs[(repo, num)] = cost
    if pr_costs:
        print(
            f"Found cost data for {len(pr_costs)} PRs "
            f"(total: ${sum(pr_costs.values()):.2f})",
            file=sys.stderr,
        )

    metrics = compute_metrics(filtered_prs, ci_status, pr_costs)

    report = format_report(metrics, excluded_count, filtered_count)

    summary_path = os.environ.get("GITHUB_STEP_SUMMARY")
    if summary_path:
        with open(summary_path, "a") as f:
            f.write(report)
        print(f"Report written to job summary ({len(filtered_prs)} PRs analyzed)")
    else:
        print(report)

    if metrics:
        write_output("total_prs", metrics["total_prs"])
        write_output("merge_rate", f"{metrics['merge_rate']:.1f}")
        write_output("resolution_rate", f"{metrics['resolution_rate']:.1f}")
        write_output("unique_tickets", metrics["unique_tickets"])
        write_output("resolved_tickets", metrics["resolved_tickets"])
        write_output("total_cost", f"{metrics['total_cost']:.2f}")
    else:
        write_output("total_prs", 0)
        write_output("merge_rate", "0.0")
        write_output("resolution_rate", "0.0")
        write_output("unique_tickets", 0)
        write_output("resolved_tickets", 0)
        write_output("total_cost", "0.00")


if __name__ == "__main__":
    main()
