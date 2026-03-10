# TODO

## Approach

This project evolves incrementally within the existing repository (strangler
fig pattern). New abstractions and implementations are introduced alongside
existing code. The existing pipeline continues to work throughout the
transition. Deprecated code is removed at cutover.

Design document: `docs/architecture-redesign.md`

---

## Epic 1: AI Code Agent System Redesign

Rearchitect the system so the AI runs autonomously inside ephemeral dev
containers with full toolchain access. The bot manages jobs and plumbing
(cloning, committing, PR creation, status transitions). The AI manages
thinking (code generation, validation, fixing failures). Workspaces persist
across jobs for the same ticket, enabling AI-generated artifacts to survive
between sessions.

### Task 1: Core domain types and IssueTracker interface ✅

Introduce the generic domain model that decouples the system from Jira.
Implement a Jira adapter that wraps the existing `JiraService` behind the
new interface, proving the abstraction works without breaking the existing
pipeline.

**Scope**

- Define `WorkItem` type (key, summary, description, type, components,
  assignee, security level) -- the generic representation of a work item
  independent of any issue tracker
- Define shared domain types in `models/`:
  - `PRDetails` (number, title, branch, base branch, URL)
  - `PRComment` (ID, author, body, file path, line, timestamp, in-reply-to)
  - `PRParams`, `PRUpdateParams`, `PR` (for PR creation/update)
  - `Author` (name, email, username -- for co-author attribution)
- Define `SearchCriteria` type abstracting JQL and future query languages
- Define `IssueTracker` interface: `SearchWorkItems`, `GetWorkItem`,
  `TransitionStatus`, `AddComment`, `GetFieldValue`, `SetFieldValue`
- Implement `JiraIssueTrackerAdapter` wrapping the existing `JiraService`
  - Maps `SearchCriteria` to JQL
  - Maps Jira API responses to `WorkItem`
  - Delegates all operations to the existing `JiraService`
- Existing code and tests remain untouched; this is purely additive

**Testing**

- Adapter correctly maps Jira ticket responses to `WorkItem` fields
- `SearchCriteria` produces correct JQL for various configurations
- Adapter delegates status transitions, comments, field operations correctly
- Error propagation from underlying `JiraService`
- Edge cases: missing fields, security-level redaction

**Documentation**

- Document `IssueTracker` interface contract (godoc)
- Document what is implemented (Jira) vs. planned (GitHub Issues, GitLab)
- Update `docs/architecture-redesign.md` if any design adjustments are needed

**Dependencies**: None (first task)

**Implementation notes**

- Package structure: `IssueTracker` interface in `tracker/`, Jira adapter
  in `tracker/jira/`. This establishes the pattern for new abstractions:
  each gets its own package with implementation sub-packages, rather than
  being added to `services/`.
- Domain types in separate files under `models/`: `work_item.go`,
  `author.go`, `pr.go`, `search_criteria.go`.
- `SearchCriteria.StatusByType map[string][]string` handles type-specific
  status filtering (e.g., Bug→"Open", Story→"To Do"). `Statuses []string`
  handles uniform status queries (e.g., crash recovery). No raw query
  escape hatch — new query patterns require evolving the struct.
- `WorkItem` guarantees: `Components` and `Labels` are always non-nil
  empty slices; security level "None" (case-insensitive) is normalized
  to empty string.
- Mock: `mocks/issue_tracker.go` using the existing func-field pattern.

---

### Task 2: Workspace Manager and GitService SyncWithRemote ✅

Introduce workspace lifecycle management and the `SyncWithRemote` git
operation that underpins it. Workspaces are scoped to tickets (not jobs),
enabling AI-generated artifacts to persist across container invocations.
`SyncWithRemote` reconciles local git state after API-created commits.
These are combined because `SyncWithRemote` has no caller without the
workspace manager, and the workspace manager needs it to function.

**Scope**

- Add `SyncWithRemote(dir, branch string) error` to the `GitHubService`
  interface
  - Implementation: `git fetch origin && git reset --hard origin/<branch>`
  - Preserves untracked files (the key property for artifact persistence)
- Update the mock in `mocks/github.go` to include the new method
- `WorkspaceManager` service with interface:
  - `Create(ticketKey, repoURL string) (string, error)` -- clone repo,
    return workspace path
  - `Find(ticketKey string) (string, bool)` -- find existing workspace
  - `FindOrCreate(ticketKey, repoURL string) (string, bool, error)` --
    return path and whether it was reused
  - `Cleanup(ticketKey string) error` -- remove a specific workspace
  - `CleanupStale(maxAge time.Duration) (int, error)` -- TTL-based cleanup,
    returns count removed
  - `CleanupByFilter(shouldRemove func(ticketKey string) bool) (int, error)`
    -- remove workspaces where the filter returns true
  - `List() ([]WorkspaceInfo, error)` -- list all workspaces (for startup
    scan)
- Directory naming convention: `<base_dir>/<ticket-key>/`
- Configuration additions: `workspaces.base_dir`, `workspaces.ttl_days`
- Config validation: `base_dir` must be set, `ttl_days` must be positive

**Testing**

- `SyncWithRemote` executes the correct git commands
- Untracked files are preserved after sync (integration test with real git
  repo)
- Error handling: remote unreachable, branch doesn't exist, dirty index
- Create workspace: directory created, repo cloned
- Find workspace: returns correct path, returns false when not found
- FindOrCreate: creates on first call, reuses on second
- Cleanup: removes directory
- CleanupStale: removes only workspaces older than TTL, preserves recent
- CleanupByFilter: removes only workspaces where filter returns true
- List: discovers workspaces on disk by naming convention
- Error cases: invalid base dir, permission errors, cleanup of non-existent
  workspace
- Existing `GitHubService` tests continue to pass

**Documentation**

- Document the post-commit sync pattern and why it's needed (API commits
  create remote state that local git doesn't know about)
- Document the artifact persistence guarantee (untracked files survive)
- Document workspace lifecycle (creation, reuse, cleanup triggers)
- Document artifact persistence convention (untracked files in `.ai-bot/cache/`
  or gitignored directories)
- Document directory structure and naming convention
- Document configuration options

**Dependencies**: None (independent of Task 1)

---

### Task 3: ContainerManager -- config resolution and runtime detection ✅

Introduce the container management abstraction. This task covers config
resolution (how the bot discovers what container to use) and runtime
detection (Podman vs Docker). Lifecycle operations (start/stop/exec) are
in Task 4.

The existing `ContainerRunner` from the validation feature branch can be
referenced for runtime detection patterns, but this is a new, broader
abstraction.

**Scope**

- `ContainerManager` interface: `ResolveConfig`, `Start`, `Exec`, `Stop`,
  `CleanupOrphans`
- `ContainerRuntime` interface abstracting Podman vs Docker
- Runtime auto-detection (`exec.LookPath` for podman, then docker)
- `ContainerConfig` type: image, build config, env vars, resource limits,
  mount points
- Config resolution chain:
  1. `.ai-bot/container.json` -- bot-specific config
  2. `.devcontainer/devcontainer.json` -- standard devcontainer (practical
     subset: `image`, `postCreateCommand`, `containerEnv`; `build.*`
     fields are deferred -- only pre-built images supported initially)
  3. Bot's `container.default_image` config -- admin fallback
  4. Built-in minimal fallback
- Configuration additions: `container.runtime`, `container.default_image`,
  `container.resource_limits.memory`, `container.resource_limits.cpus`
- Parsing logic for devcontainer.json subset (unsupported fields logged
  and ignored)

**Testing**

- Runtime detection: finds podman, falls back to docker, errors when
  neither available
- Config resolution: each priority level, fallback chain
- devcontainer.json parsing: supported fields extracted, unsupported
  fields ignored with log warning
- `.ai-bot/container.json` parsing
- Default image used when no repo config exists
- Config validation

**Documentation**

- Document dev container strategy: how teams configure their environment
- Document config resolution priority and what's read from each format
- Document the "no config" path (minimal fallback, AI generates but can't
  validate)
- Document configuration options

**Dependencies**: None

---

### Task 4: ContainerManager -- lifecycle operations ✅

Implement container start, exec, stop, and orphan cleanup. This is where
containers actually run. Builds on the interface and config resolution
from Task 3.

**Scope**

- `Start`: launch container with resolved config
  - Volume mount: workspace at `/workspace` (`:Z` for SELinux)
  - Environment injection: `AI_PROVIDER`, API keys, `PROJECT_DIR`
  - Resource limits (memory, CPU) via runtime flags
  - Container name prefix for orphan identification
- `Exec`: run a command inside a running container
  - Capture combined stdout/stderr
  - Return exit code
  - Timeout via `context.Context`
- `Stop`: stop and remove a container
- `CleanupOrphans`: find and remove containers matching the bot's name
  prefix
- Output truncation (configurable limit for large outputs)

**Testing**

- Unit tests with mocked `ContainerRuntime`:
  - Start passes correct flags (volume, env, limits, name)
  - Exec captures output and exit code
  - Stop removes container
  - CleanupOrphans filters by prefix
  - Timeout cancellation
- Integration tests with real Podman:
  - Start a container, exec a command, verify output, stop
  - Resource limits applied
  - Volume mount works (file created in container visible on host)
  - Orphan cleanup finds and removes stale containers

**Documentation**

- Document container security model (what's mounted, what's not, no
  GitHub/Jira credentials)
- Document resource limits and timeout behavior
- Document orphan cleanup (naming convention, when it runs)

**Dependencies**: Task 3

**Implementation notes**

- Task 3 intentionally deferred the `ContainerRuntime` execution
  interface (YAGNI). Task 3 implements runtime *detection*
  (`DetectRuntime` finds podman/docker on PATH) but does not define
  an interface for running containers. This task should define the
  `ContainerRuntime` interface with the methods needed for lifecycle
  operations (run, exec, stop, list) and implement it for podman/docker.
  The `Resolver` from Task 3 is a standalone component; compose it
  into the concrete `Manager` implementation built here.

---

### Task 5: Task file generation ✅

Implement the mechanism by which the bot communicates goals to the AI.
Instead of prompt templates, the bot writes a structured markdown task
file that the AI reads like a developer reading a task description.

**Scope**

- `TaskFileWriter` service:
  - `WriteNewTicketTask(workItem WorkItem, dir string) error`
  - `WriteFeedbackTask(prDetails PRDetails, newComments, addressedComments []PRComment, dir string) error`
- New ticket task file format:
  - Summary, description, acceptance criteria from `WorkItem`
  - Standard instructions ("validate using project tools, don't push to
    git")
- PR feedback task file format:
  - PR context (number, title, branch)
  - Review comments grouped by file, with reviewer attribution
  - Distinction between new comments and already-addressed ones
  - Standard instructions
- `.ai-bot/config.yaml` parsing:
  - `validation_commands` (hints for the AI)
  - `pr` settings (draft, title_prefix, labels -- used by bot)
  - `ai` provider preferences (allowed_tools, model)
- Task files written to `/workspace/.ai-bot/task.md`
- Config file read from `/workspace/.ai-bot/config.yaml`

**Testing**

- New ticket task file: various WorkItem configurations (with/without
  acceptance criteria, security-redacted, different types)
- Feedback task file: single comment, multiple comments, comments on
  different files, general comments, mix of new and addressed
- `.ai-bot/config.yaml` parsing: full config, partial config, missing
  file (defaults), malformed file
- Task file directory creation (`.ai-bot/` may not exist)
- Output format is valid, readable markdown

**Documentation**

- Document task file format with examples (both types)
- Document `.ai-bot/config.yaml` schema with all supported fields
- Document the philosophy: goal-oriented communication, not step-by-step
  instructions
- This replaces the existing prompt templates -- document what's changing
  and why

**Dependencies**: Task 1 (uses `WorkItem`, `PRDetails`, `PRComment` types
from the shared domain model).

**Implementation notes**

- Package structure: `taskfile/` for the Writer interface and
  MarkdownWriter implementation. `repoconfig/` for `.ai-bot/config.yaml`
  parsing. Each gets its own package because the repo config is consumed
  by later tasks (Task 7+ for PR settings and AI preferences), not just
  the task file writer.
- Test doubles follow the established pattern:
  `taskfile/taskfiletest/stubs.go`, `repoconfig/repoconfigtest/stubs.go`
  (if needed; `repoconfig.Load` is a pure function so a stub may not be
  necessary).
- No `AcceptanceCriteria` field on `WorkItem`. In practice (confirmed
  against real Jira instances), acceptance criteria is embedded in the
  description text (e.g., `h2. Acceptance Criteria` in wiki markup),
  not a separate custom field. The task file includes the full
  description verbatim; the AI sees acceptance criteria naturally.
- Validation commands are NOT included in the task file. The design
  principle is "bot gives a goal, not instructions." The task file's
  Instructions section says "validate using whatever build tools this
  project provides." The AI discovers validation tools autonomously
  from the repo (CLAUDE.md, Makefile, `.ai-bot/config.yaml`, CI config).
  This aligns with "AI acts autonomously" and avoids duplicating
  information already present in the repo.
- `WriteFeedbackTask` accepts two separate comment slices:
  `newComments` (action required) and `addressedComments` (context
  only). The caller (JobExecutor/FeedbackScanner) is responsible for
  determining which comments have been addressed. The writer just
  formats them into clearly labeled sections so the AI knows what
  needs attention vs. what's already handled.
- Security-level tickets: the task file includes the full description
  (the AI needs it to do its work), but adds a note in the Instructions
  section reminding the AI not to include vulnerability details in
  commit messages or code comments that may appear in the public PR.
  Security redaction of PR titles/bodies is the caller's responsibility
  (JobExecutor), not the task file writer's.

---

### Task 6: JobManager ✅

Introduce the central coordination layer. The JobManager receives events,
creates jobs, enforces constraints, and tracks state. This is the
orchestration brain of the new architecture.

**Scope**

- `Job` type: ID, ticket key, job type (new ticket vs feedback), status
  (pending, running, completed, failed), workspace path, retry count,
  timestamps
- `JobManager` service:
  - `Submit(event Event) (*Job, error)` -- create a job from an event
  - `Complete(jobID string, result JobResult) error`
  - `Fail(jobID string, err error) error`
  - `GetJob(jobID string) (*Job, error)`
  - `ActiveJobs() []*Job`
- Deduplication: reject job if one is already running for the same ticket
- Concurrency semaphore: configurable `max_concurrent_jobs`, blocks
  `Submit` when limit reached (or queues and processes when slot opens)
- Retry policy: configurable `max_retries`, exponential backoff
- Circuit breaker: pause job creation if N consecutive failures occur
  within a time window (protects against sustained AI API outages)
- Workspace integration: delegates to `WorkspaceManager` for
  create/find/cleanup
- Event types: `NewTicketEvent`, `NewFeedbackEvent`
- Configuration: `guardrails.max_concurrent_jobs`, `guardrails.max_retries`,
  `guardrails.circuit_breaker_threshold`, `guardrails.circuit_breaker_window`,
  `guardrails.circuit_breaker_cooldown`

**Testing**

- Submit creates a job with correct initial state
- Deduplication: second submit for same ticket rejected while first is
  running
- Deduplication: submit succeeds after prior job completes
- Concurrency: respects semaphore limit
- Retry: failed job can be resubmitted within retry limit
- Retry: resubmit rejected after max retries exhausted
- Complete/Fail update job state correctly
- ActiveJobs returns only running jobs
- Workspace path assigned from WorkspaceManager
- Circuit breaker: trips after N consecutive failures within window
- Circuit breaker: resets after cooldown period
- Circuit breaker: does not trip on isolated failures spread over time

**Documentation**

- Document job lifecycle states and transitions
- Document concurrency model and semaphore behavior
- Document retry policy (count, backoff strategy)
- Document deduplication semantics

**Dependencies**: None (workspace integration moved to Task 7)

**Implementation notes**

- Package structure: `jobmanager/` for the Manager interface and
  Coordinator implementation. `jobmanager/jobmanagertest/stubs.go`
  for test doubles. Follows the established `*test` package convention.
- **Retry semantics**: The JobManager does NOT retry jobs internally
  and does NOT implement exponential backoff. Scanners drive retries:
  when a job fails, the ticket status reverts to "todo", the scanner
  re-discovers it on the next poll cycle, and calls Submit again. The
  JobManager tracks per-ticket failure counts across submissions and
  rejects once `max_retries` is exhausted. Success resets the counter.
  The "backoff" is implicit — it happens at the scanner's poll
  interval. This avoids duplicating the scanner's work and keeps the
  JobManager stateless with respect to scheduling.
- **No workspace integration**: The original spec had the JobManager
  delegating to WorkspaceManager for create/find/cleanup. In practice,
  workspace operations are the JobExecutor's concern (Task 7), not
  the JobManager's. The JobManager coordinates job lifecycle; it
  doesn't need to know where repos are on disk. This removes the
  Task 2 dependency.
- **Event is minimal**: `Event` carries only `TicketKey` and `Type`
  (new ticket vs feedback). The executor fetches work item details,
  PR details, and repo URLs from the tracker/git service when it
  runs. Scanners emit lightweight events; they don't pre-fetch and
  bundle payloads. This keeps the JobManager decoupled from domain
  models and avoids stale data (the executor always gets fresh state).
- **ExecuteFunc injection**: The Coordinator accepts an `ExecuteFunc`
  at construction — this is how the JobExecutor (Task 7) plugs in.
  The Coordinator calls it in a new goroutine when a slot is
  available and transitions the job to Completed/Failed based on
  the return value. Task 11 (wiring) composes them.
- **Concurrency model**: `Submit` enqueues a job and calls an
  internal `tryDispatch` function synchronously (while holding the
  lock). `tryDispatch` checks for pending jobs and available slots,
  transitions jobs to Running, and launches goroutines. The same
  `tryDispatch` is called from `Complete`/`Fail` to drain the queue
  as slots free up. No background dispatch goroutine is needed.
- **Circuit breaker**: Simple open/closed (no half-open). Tracks
  consecutive failure timestamps within a configurable window. Any
  success resets the failure history. Auto-resets after the cooldown
  period. Threshold of 0 disables the breaker entirely.
- **Thread safety**: All mutable state is guarded by a single mutex.
  Public methods return deep-copy snapshots of Jobs to prevent data
  races on reads outside the lock. The ExecuteFunc receives a
  snapshot, not a reference to internal state. Race detector passes
  cleanly.
- **Job.AttemptNum**: Set to `failureCount + 1` at submission time.
  Gives the executor visibility into how many prior attempts have
  failed for this ticket (e.g., for logging "attempt 3 of 3").

---

### Task 7: JobExecutor -- new ticket pipeline ✅

Implement the end-to-end pipeline for processing a new ticket. This is
where all the components come together. The executor handles plumbing;
the AI handles thinking.

**Scope**

- `JobExecutor` service:
  - `ExecuteNewTicket(job *Job) (*JobResult, error)`
- Pipeline steps:
  1. Prepare workspace (WorkspaceManager.FindOrCreate)
  2. Create branch (`{bot-username}/{ticket-key}`)
  3. Write task file (TaskFileWriter.WriteNewTicketTask)
  4. Write wrapper script (`/workspace/.ai-bot/run.sh`) -- provider-specific
     script that invokes the AI CLI, captures exit code, and writes
     `session-output.json`. Must handle Claude and Gemini output formats.
  5. Read `.ai-bot/config.yaml` for container/AI hints
  6. Resolve and start container (ContainerManager)
  7. Launch AI agent inside container, wait for completion or timeout
  8. Collect results: `git diff`, `git status` in workspace
  9. If no changes: return failure (JobManager handles retry)
  10. Commit via GitHub API (verified commit, co-author attribution)
  11. Post-commit sync (GitService.SyncWithRemote)
  12. Generate PR description
  13. Create PR (draft if AI indicated validation failures)
  14. Update ticket: set PR URL, transition to "In Review"
  15. Stop container (workspace retained)
- Error handling: revert ticket status on failure, post error comment
  (unless disabled)
- Draft PR: if AI exits with failure indicators or validation issues,
  create draft PR instead of normal PR; leave ticket in "In Progress"

**Testing**

All dependencies mocked (IssueTracker, GitService, ContainerManager,
WorkspaceManager, TaskFileWriter):

- Happy path: full pipeline succeeds, PR created, ticket transitioned
- AI produces no changes: failure result returned
- AI times out: container killed, failure result, ticket reverted
- Container fails to start: fallback to minimal image, retry
- Commit fails: ticket reverted, error comment posted
- PR creation fails: ticket reverted, error comment posted
- Draft PR path: validation failure indicators trigger draft PR
- Security-level tickets: redacted PR title/body
- Co-author attribution when ticket has assignee
- Error comments disabled: errors logged but not posted to Jira

**Documentation**

- Document the executor pipeline with a step-by-step description
- Document error handling and recovery behavior for each step
- Document draft PR conditions
- Document what the AI CLI command looks like and how the AI discovers
  its task

**Dependencies**: Tasks 1-6 (all foundational components and JobManager)

---

### Task 8: JobExecutor -- PR feedback pipeline ✅

Implement the pipeline for processing PR review feedback. This is a
variation of the new ticket pipeline with key differences: workspace
reuse, feedback-specific task files, pushing to an existing PR branch,
and replying to review comments.

**Scope**

- `JobExecutor` addition:
  - `ExecuteFeedback(job *Job) (*JobResult, error)`
- Pipeline steps:
  1. Find or recreate workspace (WorkspaceManager.FindOrCreate -- self-healing
     if workspace was cleaned up by TTL or disk failure)
  2. Sync workspace with remote (GitService.SyncWithRemote -- picks up
     human commits and bot's prior API commits; preserves untracked
     artifacts)
  3. Write feedback task file (TaskFileWriter.WriteFeedbackTask)
  4. Read `.ai-bot/config.yaml` for container/AI hints
  5. Resolve and start container (ContainerManager)
  6. Launch AI agent, wait for completion or timeout
  7. Collect results
  8. If no changes: return failure
  9. Commit via GitHub API (new commit on existing branch)
  10. Post-commit sync
  11. Reply to PR comments that were addressed
  12. Stop container (workspace retained)
- Workspace reuse: verify artifacts from prior sessions are present when
  workspace was reused; accept their absence when workspace was recreated
- Comment replies: post responses indicating which comments were addressed

**Testing**

All dependencies mocked:

- Happy path: workspace reused, artifacts present, changes committed to
  existing branch, comments replied to
- Workspace not found: self-heals by re-cloning and checking out existing
  branch; AI proceeds without prior artifacts
- Sync picks up human commits: workspace updated before AI runs
- Artifacts survive: untracked files from prior session present after sync
- No changes from AI: failure result
- AI timeout: container killed, failure
- Multiple feedback rounds: workspace reused across several feedback
  cycles
- Comment grouping: comments organized by file in task file

**Documentation**

- Document the feedback pipeline and how it differs from new ticket
- Document workspace reuse and artifact persistence in practice
- Document comment reply behavior
- Document the full lifecycle: ticket created -> bug fix -> PR created ->
  review comments -> feedback processed -> more comments -> more feedback

**Dependencies**: Task 7 (shares `JobExecutor`, extends it)

---

### Task 9: Event-based scanners ✅

Implement the event loop that discovers work. The scanners poll external
systems and emit events to the JobManager. This replaces the existing
scanner implementations with the event-driven model from the redesign.

**Scope**

- `WorkItemScanner`:
  - Polls Jira (via IssueTracker) for tickets in "todo" status
  - Emits `NewTicketEvent` to JobManager
  - Configurable poll interval
- `FeedbackScanner`:
  - Polls Jira for tickets in "in review" status
  - Checks GitHub for new PR comments since last processed timestamp
  - Applies bot-loop-prevention filters:
    - Ignored usernames (completely skipped)
    - Known bot usernames (processed but loop prevention applies)
    - Thread depth limiting
  - Emits `NewFeedbackEvent` to JobManager
- Both scanners are stateless (query each cycle, JobManager handles
  dedup)
- Carry forward existing bot-loop-prevention logic from current codebase
- Event types carry enough context for JobManager to create jobs

**Testing**

- WorkItemScanner: emits events for matching tickets, skips non-matching
- FeedbackScanner: emits events for tickets with new comments
- Bot-loop prevention: ignored users skipped, known bots filtered,
  thread depth respected
- Poll interval respected
- Scanner stop/start lifecycle
- No events emitted when no work found
- Multiple tickets in single scan cycle

**Documentation**

- Document scanner design and event model
- Document bot-loop prevention configuration and behavior
- Document the relationship between scanners and JobManager (scanners
  emit, JobManager deduplicates and schedules)
- Update existing bot-loop-prevention documentation to reflect new
  architecture

**Dependencies**: Tasks 1 (IssueTracker), 6 (JobManager). Task 2
(GitService/WorkspaceManager) for FeedbackScanner's PR comment retrieval.

---

### Task 10: Crash recovery and startup orchestration

Implement the startup sequence that recovers from crashes and cleans up
orphaned resources. The system uses Jira and GitHub as the durable state
store -- no separate database needed.

**Scope**

- Startup sequence:
  1. Clean orphaned containers (ContainerManager.CleanupOrphans)
  2. Scan workspace base directory (WorkspaceManager.List)
  3. Query Jira for tickets in "In Progress" assigned to the bot
  4. For each stuck ticket:
     - Check GitHub for existing PR
     - If PR exists but ticket still "In Progress": status transition
       was interrupted, complete it
     - If no PR but branch has commits beyond base: AI work completed
       but crashed before PR creation, skip AI and create PR directly
     - If no PR and no branch commits: job was interrupted mid-execution,
       re-queue via JobManager
  5. Clean up workspaces for tickets in terminal states
     (WorkspaceManager.CleanupByFilter with a callback that checks
     ticket status via IssueTracker)
  6. Clean up stale workspaces past TTL
     (WorkspaceManager.CleanupStale)
- Integrate into application startup in `main.go` (runs before scanners
  start)

**Testing**

- No orphans: startup completes cleanly
- Orphaned containers: detected and removed
- Stuck ticket with no PR and no branch: re-queued
- Stuck ticket with no PR but branch has commits: PR created directly
- Stuck ticket with PR: status transition completed
- Stale workspaces: cleaned up
- Terminal ticket workspaces: cleaned up
- Mixed scenario: combination of the above
- Startup errors are logged but non-fatal (best-effort recovery)

**Documentation**

- Document crash recovery behavior and assumptions
- Document startup sequence order and rationale
- Document what "stuck" means and how it's detected
- Document the durable state model (Jira + GitHub + filesystem)

**Dependencies**: Tasks 2 (WorkspaceManager), 4 (ContainerManager),
6 (JobManager), 1 (IssueTracker)

---

### Task 11: Guardrails, wiring, and cutover

Add cost tracking and budget enforcement. Wire all new components together
in `main.go`. Remove deprecated code paths. Validate the complete system
end-to-end.

**Scope**

- Cost tracking:
  - Track AI session costs (per-job and daily aggregate)
  - Daily budget enforcement: pause job creation when
    `max_daily_cost_usd` exceeded
  - Budget reset (configurable: daily, or manual)
  - Configuration: `guardrails.max_daily_cost_usd`,
    `guardrails.max_container_runtime_minutes`
- Wiring in `main.go`:
  - Initialize all new components with dependency injection
  - Startup sequence (crash recovery → scanners → HTTP server)
  - Graceful shutdown (stop scanners → wait for active jobs → cleanup)
- Cutover:
  - Replace existing scanner/processor pipeline with new
    JobManager/JobExecutor pipeline
  - Remove deprecated code: old `TicketProcessor`, old scanner
    implementations, prompt templates (or mark clearly as deprecated
    with removal timeline if phased cutover is preferred)
  - Remove unused mocks and test helpers
- End-to-end validation:
  - Process a test ticket through the full pipeline
  - Process PR feedback through the full pipeline
  - Verify crash recovery works

**Testing**

- Cost tracking: accumulation, budget check, daily reset
- Budget exceeded: job creation paused, existing jobs finish
- End-to-end (with mocked external services): full ticket lifecycle from
  scan → job → container → AI → commit → PR → feedback → done
- Graceful shutdown: active jobs complete, no orphaned containers

**Documentation**

- Final review of all documentation for accuracy
- Update `CLAUDE.md` project overview to reflect new architecture
- Update `docs/architecture.md` (current system doc) or replace with
  `docs/architecture-redesign.md`
- Update `config.example.yaml` with all new configuration options
- Document migration notes: what changed, what was removed, any
  behavioral differences from the old system
- Remove or archive `plan.md` (pre-PR validation design, superseded)
