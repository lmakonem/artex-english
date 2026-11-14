# Changelog

Important changes to this project are recorded in this file, following the [Keep a Changelog](https://keepachangelog.com/en/1.1.0/) format.

## [Unreleased]

### Deployment and release maintenance (keeping program version 0.3.15)

- Migrated to the standalone `RuoJi6/ARTEX` repository, preserving the original commit history and branches.
- The in-UI online update source is `RuoJi6/ARTEX`; GitHub Actions builds the five-platform program packages and the SHA256 checksum file.
- Builds the Docker Hub images on native AMD64 and ARM64 runners and verifies program startup, database connectivity, and the embedded frontend.
- After both architectures pass verification, publishes `v0.3.15`, the `latest` image, and the corresponding program packages.
- Compose and the deployment docs use this repository's image, with added migration guidance for preserving data in an existing deployment.

## [0.3.15] - 2026-10-07

### Intercept

#### Added

- **New built-in intercept rule "delete-class endpoint paths"**: the previous built-in destructive HTTP rule recognized only the DELETE **method**, yet most applications' delete endpoints can be triggered with GET/POST, and a call like `curl 'http://t/api/user/delete?id=1'` really deletes target data. A `deny` rule has been added to cover `/delete /del /remove /unlink /erase /destroy` (the verb must be followed by a separator, so `/delivery`, `/details`, and `/delta` are not falsely intercepted). Existing instances also get it after upgrading, and it can be disabled or deleted under "System -> Command Intercept".
- **The token cost of the model fallback approval can be viewed separately**: the approval call previously had no separate metering attribution, so how much it spent could only be guessed. It is now metered separately, and a usage card appears under the "Model Fallback Approval" switch in "System -> Command Intercept" - cumulative statistics for five items (call count, input, output, cache read/write), plus a mini bar chart of daily spend over the last 30 days.

### Vulnerability push

#### Added

- **Findings can be pushed to IM**: a new "System -> Notification Push" page lets you push scan findings to six channels: **DingTalk, Feishu, WeCom, generic Webhook, Telegram, and email**. You can configure any number of instances of the same type, each with its own enable/disable, rate limiting, and filter rules.
- **Push timing has two modes, realtime and digest**: realtime mode sends each hit one by one as it occurs; digest mode merges them into one message on a global cycle (default 30 minutes), leading with the new count and severity distribution. To do "high-severity realtime, the rest digested", create two channels configured separately.
- **Filter rules support four dimensions**: minimum severity, limiting task / asset scope, vulnerability-type keyword inclusion and exclusion (exclusion wins), and whether to receive disposition-status changes (off by default). Messages carry a "View Details" button whose target address is set by the global "callback address"; if left empty, no button is attached.
- **Delivery history and manual resend**: lists each delivery's status, attempt count, failure reason, and owning channel, filterable by channel and status; failed items can be resent in one click (a resend zeroes the retry counter).
- **Masked redisplay of channel credentials**: the Webhook address, signing secret, Bot Token, SMTP password, etc. are redisplayed only as masked values with a trailing-digit hint; submitting them unchanged means "don't modify", and clearing the input deletes that field.

#### Fixed

> This section is the conclusion of a security audit done after the feature's first version was complete; each item was reproduced, then fixed, then had a regression test added.

- **Fixed a mask bypass via "change the target address, keep the credentials" (critical)**: config merging kept the database's original value for any "unmentioned key", so changing only the address while saying nothing about the credentials could make the server send the real credentials in the database to an arbitrary address, completely silently. Now, once the target address changes, every credential field must be stated explicitly - give a new value, or explicitly leave it empty to mean no longer needed.
- **Fixed zero escaping of untrusted content in markdown-style channels**: vulnerability titles and summaries come from model output and asset URLs come from scan results, so a crafted title renders as a clickable external link in DingTalk / WeCom / Feishu, and image syntax causes the client to fetch it on render and leak the reader's IP. Now everything is flattened to a single line + markdown metacharacters escaped, with escaping happening at each channel's render exit rather than in a shared title function.
- **Fixed the SSRF surface of the delivery address**: previously loopback, intranet, and cloud-metadata addresses could all be delivered to, and the first 200 bytes of a failure response body went into `last_error` and were echoed by the delivery history, forming a semi-blind read primitive. Now defense is applied at the **dial stage**, covering DNS rebinding and rejecting cross-host redirects; loopback and link-local addresses require explicitly setting `ARTEX_NOTIFY_ALLOW_LOCAL=1`, while RFC1918 private networks are still allowed (self-hosted intranet SMTP relays are common).
- **Fixed push credentials leaking via error messages**: DingTalk's `access_token`, WeCom's `key`, and Telegram's bot token are all in the URL, and on a delivery failure the full URL flowed into `last_error`, the delivery history, server logs, and frontend prompts - four places. Now they are uniformly redacted, keeping only `scheme://host` and the underlying reason.
- **Fixed silent loss when a digest message is truncated but the whole batch is marked delivered**: the truncated-away vulnerabilities were neither in the message nor in the failure list, yet the delivery history showed success. Now packing is done per **whole message**, marking only those that fit into this message, with the rest returned to the queue to continue in the next message without consuming a retry; the message header also states how many more will continue in the next one.
- **Fixed a typo in the minimum-severity threshold silently disabling the filter**: when `min_severity` is misspelled as something like `hgih`, the unknown severity ordinal is 0 and the check degenerates to always-true, so the user thinks they only receive high-severity but in fact all vulnerabilities are flooded into the group. Now the value is validated on the write path and the valid options are listed.
- **Fixed `rate_per_min = 0` (no rate limit) being unreachable**: the DB-write layer silently changed an explicit 0 to the default value, so the operator thought they had lifted rate limiting while it was still throttled. The default is now applied at the API layer when the field is absent.
- **Fixed digest channels completely bypassing the token bucket**: `rate_per_min` previously had no effect on digest mode. Now the number claimed is bounded by both this round's quota and the in-memory upper bound.
- **Fixed failure handling judging by the whole batch's max attempt count, letting old deliveries drag down new ones**: a delivery already retried twice would pull brand-new deliveries in the same batch into failed too, so a new vulnerability was permanently lost without a single retry used. Now this is decided per item.
- **Fixed deliveries with unparseable snapshots in a digest batch being silently marked successful**: such items are skipped at the render stage yet counted as successful along with the whole batch. Now they are explicitly judged failed with a reason.
- **Fixed a single channel's per-round delivery count possibly exceeding the lease duration**: in a multi-instance deployment, rows whose lease expired would be re-claimed, sent again, and double-increment the attempt count. Now the per-round cap is derived backward from "lease / single-attempt timeout", with an assertion pinning the relationship of these constants.
- **Fixed Telegram truncation possibly cutting an HTML entity**: truncation only avoided a half-cut tag, not an entity fragment like `&amp`, which could make the parser reject the **whole** message. Now both are avoided.
- **Fixed the email channel judging a transient SMTP failure as permanent**: a 4xx such as a greylist `450` is a temporary rejection that should be retried later, but it was previously judged dead across the board, so a greylisting server made every push fail after the first attempt. Now the response code's first digit distinguishes them: 4xx is retryable, 5xx is a permanent failure.
- **Fixed a nested structure actually writing the mask literal into the database on submit**: an object field like `webhook.headers` can only be masked or submitted as a whole, so stuffing the mask sentinel inside the object would store it as a real value and silently break auth. Such a submission is now explicitly rejected.

#### Design notes

- **The vulnerability-write transaction does only a single blind INSERT**: `notification_events` and the vulnerability are written in the same transaction, so a commit guarantees both are atomically consistent. This INSERT deliberately does not read the channel table or run the user's filter rules, and is wrapped by a `SAVEPOINT` - otherwise a single misconfigured filter condition could abort the transaction and prevent a high-severity vulnerability from being stored.
- **Delivery uses lease-based claiming rather than a long transaction**: after `FOR UPDATE SKIP LOCKED` claims a row, it is set to `sending` and `next_attempt_at` is pushed into the future as a lease, so no database lock is held during delivery; rows left by a crashed process are re-claimed after the lease expires, self-healing without infinite retries.
- **Rate limiting does not consume the retry budget**: this round's quota is computed by the token bucket first, then that many are claimed. Reversing the order would waste an attempt on a rate-limited delivery, and after three budget units are drained by pure waiting it would fall into failure.

### Test infrastructure

#### Fixed

- **Fixed permanent asset residue caused by the cleanup order in the company-ICP-attribution test**: `defer d.Close()` runs first on function return and `t.Cleanup` runs after it, so all cleanup statements landed on an already-closed connection and the errors were discarded, leaving test assets and companies permanently in the database - which also made other cases that assert on asset counts fail inexplicably. Now closing the connection also goes through `t.Cleanup` and is registered first, and cleanup failures are made visible.

### Traffic

#### Added

- **The traffic list supports content search and path / status code / length filters and column sorting**: the filter row supports filtering by **response content** (via full-text index), **path**, **status code** (an exact value or a bucket like `2xx`), and **response length range**, and the header can sort by time, status code, and response length, with the sort preference stored locally. Two indexes are added and take effect automatically after an existing instance upgrades; the Agent tool's query contract is unchanged.
- **The traffic list adds "Clear All"**: deletes all traffic records in one step (ignoring the current filter) and cleans up historical host directories the index no longer records; after clearing, it does a full compaction and reports how much space was actually freed. Traffic evidence already bound to vulnerabilities lives in a separate evidence store and is unaffected.

#### Fixed

- **Fixed disk space not being freed after deleting traffic**: the index database was created without `auto_vacuum`, so the file never shrinks; the full-text index is also `contentless_delete`, so `DELETE` only writes tombstones without reclaiming the original postings - deleting traffic actually made the index larger (measured: capturing 6MB of bodies -> a 16MB index, still 16MB after deleting everything). Now a newly created index database enables `auto_vacuum=incremental`, performing full-text index merges and incremental reclamation in chunks in the background after each deletion, gradually returning space to the system (the same scenario drops back to 104KB); reclamation releases the write lock between chunks and does not block traffic recording.

> Upgrade note: `auto_vacuum` can only be set at database creation, so **an existing instance's index database is still in the old mode**, where incremental reclamation is a no-op (a line is printed at startup to indicate this). After such a database upgrades, tombstone growth has already stopped, and the already-occupied size can be reclaimed once with "Clear All" - that step converts the database to incremental-reclamation mode, after which everyday deletions take effect on their own.

### LLM

#### Fixed

- **Fixed custom session headers not taking effect on the "one-off LLM call" path**: goal decomposition (round 0) and cold-node compaction are both one-off calls that attach no transcript store, so there is no session id on the context and the gateway side can't read the header - for an endpoint like opencode zen that returns 400 outright when the header is missing, this showed up as "round 0 fails while later planner rounds work fine". Now these two paths are explicitly given a session id stable per exploration, which incidentally lets token usage be correctly attributed to the corresponding exploration (previously it was never recorded).

### Tools

#### Fixed

- **Fixed MCP and custom tools' output truncation not taking effect, so large results flood the context**: MCP tool returns were previously not truncated at all, and HTTP-style custom tools, because an empty tool context was passed in, always used a hardcoded 6000 characters rather than the session-configured limit. Now both paths go through `actool.Capture`: truncated to the session's `MaxOutputChars` (default 30000), and when `ToolOutputDir` is configured, the full output spills to disk with only a leading fragment and a file pointer left in the message.
- **Upgraded norma to v0.4.3**: the harness layer uniformly runs `CaptureOnce`, so no tool (including future ones) overflows the context by missing truncation.

### Deployment

#### Fixed

- **Fixed the activity stream not connecting in a reverse-proxy deployment**: the SSE address was previously unconditionally built as `${hostname}:8787`, so in a deployment reverse-proxied to 443 the page loaded from `https://domain/` while SSE was pointed at port 8787, which was not exposed publicly, showing up as the activity stream reconnecting repeatedly. Now it is distinguished by `NODE_ENV`: production defaults to same-origin, and only `next dev` keeps `:8787` to bypass Next.js's buffering of SSE; the `NEXT_PUBLIC_SSE_BASE` override is retained. The README adds reverse-proxy deployment guidance.

### Findings

#### Fixed

- **Fixed the "by asset" view's asset list having no scrollbar when it overflows**: the outer card had only a `max-height` and no definite height, so the scroll viewport's percentage height couldn't be resolved, and with many assets the list either burst the card or was truncated and couldn't scroll. Now the height cap is applied directly to the asset tree's native scroll container, adapting to the window height.

### Database

#### Fixed

- **Set an upper bound on the connection pool**: `database/sql` by default does not limit how many connections the application itself can open, so when the pool has no idle connections it unconditionally creates new ones, all the way up to PostgreSQL's `max_connections` (default 100) before being rejected. Now capped at 32, with over-quota queries waiting for an idle connection, so under the same load it manifests as slowness rather than errors; if `max_connections` is lowered, lower this accordingly.
- **Improved connection reuse**: `MaxIdleConns` defaults to 2, so beyond 2 concurrent connections, each is closed right after use and the next request has to redo the TCP and PostgreSQL auth handshake. Now the idle cap is raised to match the total cap, with a 30-minute connection lifetime and 5-minute idle eviction added, letting the pool naturally replace long-lived bad connections.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.14] - 2026-09-24

### Task list

#### Added

- **The task list adds a "Vulnerabilities" column**: shows counts by severity tier - Critical / High / Medium / Low - with nonzero tiers colored by severity, so you can see each task's vulnerability volume and distribution at a glance.

#### Changed

- **Narrowed the "Description / Goal" columns**: overly long content is ellipsized, with the full text viewable on hover, reducing how much horizontal list space long text takes up.

### Exploration graph and planning situation overview

#### Changed

- **Streamlined the planning situation overview (graph_overview) and set count caps on each list**: recent facts, ended intents, pending intents, cold-zone digests, and confirmed vulnerability details are all changed to "latest window + a count fallback", with the undisplayed ones queryable on demand; this significantly reduces the per-round LLM context size and avoids long-task context bloat (the cold zone of the linked-task overview is throttled too).

#### Fixed

- **Eliminated the reverse duplicate edge between digest and finding in the exploration graph after collapsing cold nodes.**

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.13] - 2026-09-19

### Asset intercept

#### Added

- **New global asset-intercept rules (blacklist)**: supports entering exact and fuzzy-match domains / IPs / URLs and CIDR ranges, with create/read/update/delete and enable/disable for rules; the management page is at "System -> Asset Intercept"; by default it includes fuzzy intercepts for government (`.gov` / `.gov.cn`) and education (`.edu` / `.edu.cn`) sites.
- **Asset intercept is wired into the execution chain**: before dispatching an intent (`add_intent`) and inserting assets (`insert_assets`), the Agent first runs an intercept check on the target asset - an intent that hits an intercept is not dispatched, an asset that hits an intercept is not inserted, and the asset info and intercept reason are returned to the Agent.
- **New task-level intercept / allow (whitelist) rules**: independent of the global rules and effective only for this task, evaluated in the order "intercept first, then allow" - hitting an intercept forbids it; not hitting an intercept but with this task's allow rules configured and none matching means "not allowed to test"; when no allow rules are configured, the whitelist is not enabled. They can be entered at task creation, or added/edited/deleted and enabled/disabled in the task detail "Overview".
- **Task templates support preset categories and task-level intercept/allow rules**: a template can save a task category and a set of task-level rules, which are brought into the new-task form when the template is applied.

### Operation review

#### Fixed

- **Tightened the model-judge output protocol to reduce false allows caused by truncation**: lowered the decision comment cap from 500 to 120 Chinese characters and enforced "output JSON only, no preamble or code block", to avoid a decision being unparseable after `MaxTokens` truncation and then allowed per the model-failure policy (fail-open).

### Task archiving

#### Fixed

- **Archiving skips a symbolic link instead of failing the whole package**: the archive format end-to-end supports only regular files and directories, so previously any symbolic link in the working directory caused the entire task archive to fail; now the symbolic link is skipped and logged, while the rest of the files archive normally (not following the link, not escaping the directory tree).

### Account and compliance

#### Added

- **A "Terms of Use and Disclaimer" dialog is added before login**: you must check agreement before you can log in.

### License and dependencies

#### Changed

- **The project adopts the AGPL-3.0 open-source license**, and the README's license and disclaimer notes are completed.
- **Upgraded norma to v0.4.1.**

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.12] - 2026-09-17

### Exploration-chain broadcast board

#### Added

- **The task detail page adds an "exploration-chain broadcast board"** (#144): presents the task's exploration nodes (start/goal/intent/fact/finding/hint/compaction) as a timeline feed, supporting filter by type, keyword search, ascending/descending order, pagination, and auto-refresh; when at "the first page with newest first" it is the live spot and refreshes each round, and when away from that spot it only accumulates an unread count without disturbing the current reading, with a one-click "N new broadcasts - back to latest" return. Grouped by day, so even paging through a long task you can tell "which day this happened".
- **Each row of the board shows the node id** (#145), to make it easy to cross-reference the exploration-chain graph and locate a specific node.
- **A board node's detail expansion shows upstream/downstream and anchored assets** (#147): expanding a broadcast reveals that node's upstream (where it came from) / downstream (what it produced) relationships, and hovering a linked item pops up the corresponding node's card (type/status/source/time/summary/payload fragment); it also lists the assets that node anchors (type label + recognizable text). The relevant data is sent along with the broadcast page, so expanding makes no extra request.
- **Board search supports filtering by node id** (#150): the search box adds node-id matching in addition to "content / source", so entering a plain number or the UI-displayed "#41" form precisely locates the corresponding node.

### Intent management

#### Added

- **Intent deletion supports two modes, "soft delete / hard delete"** (#149): pending, running, and paused intents can all be deleted, and deletion requires a reason; the delete mode is chosen in the confirmation dialog.
  - **Soft delete (default)**: the intent is set to "deleted" and the reason is recorded in a separate field, keeping the intent node and all its outputs and lineage.
  - **Hard delete**: physically removes the intent, plus the exclusive descendant nodes "supported only by it" (cascading along the output / intent chain to the leaves), to avoid leaving orphaned data; the deleted intent's token metering is retained archived by its original date; shared nodes (still referenced by other intents), goals, and task-root facts are all retained, and the hard-delete dialog shows the estimated number of cascade-affected nodes.

  Both modes notify the planner "this intent was deleted by the user + reason" and re-plan accordingly.

### Operation review

#### Added

- **Approval records support filtering by status and decision source** (#139): approval records can be filtered by approval status and decision source, to quickly locate the target record.

#### Fixed

- **Pending approval requests load independently of the history pagination** (#133): pending requests are no longer affected by the history list's pagination, and remain fully visible while paging through history.

### Agent

#### Changed

- **The worker role description is changed to generic cybersecurity-platform wording** (#138).

#### Fixed

- **Fixed cold-node compaction never triggering**: wired the Compactor into the per-task planner; cold-node compaction (cold-digest) previously never ran because it was not wired in, and is now restored.

### Chat

#### Added

- **Chat supports @-referencing multiple record types and scroll pagination** (#135): you can @-reference multiple types of records in chat, and the reference candidates support scroll-paginated loading.

#### Fixed

- **Long message bubbles are constrained within the session panel** (#137): overly long message bubbles no longer overflow the session area.
- **Fixed uploading a file before a new conversation is created reporting "missing upload file"**: the Composer's file selection snapshots the `FileList` into an array before clearing the input and then invokes the callback; previously in the draft state a session had to be created asynchronously first, and by the time execution resumed the `FileList` live-bound to the input had already been cleared, so the upload lacked the file field and the backend returned 400.

### Traffic

#### Fixed

- **The traffic-recording proxy listens only on 127.0.0.1 by default** (#129, #130): avoids the default config exposing the open proxy on other network interfaces.

### Web

#### Fixed

- **Fixed the static-export task list page losing the global header.**
- **Completed the demo vulnerability-detail page's associated-traffic mock, fixing a white screen.**

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)
- [@dingpotian](https://github.com/dingpotian)

## [0.3.11] - 2026-09-15

### Operation review

#### Added

- **Approval records support source navigation** (#125): clicking an approval record's "source" jumps back to the one tool execution that triggered that approval - opening the original full session, auto-paginating to the target position, expanding the command and result in place, and centering and highlighting it in the scroll area, with surrounding messages kept readable; when the user scrolls manually, auto-correction stops. Across ordinary conversations, task Workers, the planner, and main-Agent segmented sessions, it locates precisely via the persisted `tool_use_id` and the task mapping (a new scoped call-ID index is added), and when it is missing, duplicated, or ambiguous it shows an explicit notice rather than jumping to another execution. When the session is deleted or the task is archived/the record is missing, it gives a clear notice.
- **Approval records support pagination** (#115).

#### Changed

- **Streamlined the model-review input** (#124, #125): the model review (the LLM judge after rules miss) input is converged to a versioned JSON of "this one complete tool call + an explicitly selected short context + the local working directory". The context takes only real user messages (the current user message of chat / the task main Agent), the Worker no longer carries the intent summary and clears the context inherited from an upper Agent, and the planner and auto-triggered sessions do not fabricate user messages. It no longer carries the task description, goal, operation constraints, global exploration situation, call history, or the Worker's full intent - these are still used for Agent execution and standalone session auditing per the original logic, just not fed into operation review. All decisions must output a `decision`/`comment` JSON, and the comment must include "the actual operation, the consequence on success, and the matched rule"; instructions in the parameters or context cannot change the review policy. The snapshot and fingerprint of the input actually sent to the model are saved, old snapshots are retained and labeled with their version, and old inputs are not fabricated from current data.

#### Fixed

- **A model decision wrapped in a code block is no longer silently allowed** (#126): if the model's decision JSON was wrapped in a ``` code block, parsing previously failed and it was silently allowed per the model-failure policy; now the code-block fence is stripped before parsing, and only if parsing still fails is the configured failure policy applied.

### Agent

#### Added

- **New experimental noa context compaction** (norma upgraded to v0.4.0): an experimental feature that can be enabled in System Settings, off by default. When on, noa (model-driven context compaction) takes over context compaction for the four Agent types - main Agent, planner, Worker, chat - replacing the built-in compaction; the compaction originals are persisted and archived centrally under `<workDir>/noa/<session ID>/` (not scattered in each task directory), in a globally unique subdirectory by session ID. On wire-in failure it automatically falls back to built-in compaction without interrupting real tasks; the switch is read once per run, so toggling only affects runs started afterward.

#### Fixed

- **Exploration-graph tools reject rather than nil-crash when there is no task context**: when the exploration-graph tools are called in a non-task context, they return an explicit error, no longer crashing on a nil-store dereference.

### MCP

#### Added

- **Support for legacy SSE MCP services** (#117): compatible with MCP servers that only provide the legacy SSE transport.

### Traffic

#### Fixed

- **Traffic search matches the record's host port-aware** (#114): `traffic_search`'s host matching includes the port, avoiding same-host records on different ports interfering with each other.
- **A `traffic_search` description migration error no longer aborts subsequent reporter migrations**: a single migration failure is isolated and does not affect the execution of subsequent migrations.

### Web

#### Changed

- **The task retest vulnerability options add a divider** (#122): a separator is added between retest vulnerability options for clearer visuals.

#### Fixed

- **Fixed a full-page crash on the demo task detail page**: in mock mode, the task detail "Sessions" tab fetches `GET /api/tasks/<id>/side-questions` (side-question history), but the mock handler had no such route and the read fallback, treating "a path ending in s as a collection", returned `[]`, so `data.items` was `undefined`, and the side-question hook's `merge()` iterating over it threw `TypeError: t is not iterable`; this exception happened inside `setItems`'s updater, got deferred by React to the render phase and rethrown, where the caller's `catch` couldn't catch it, and the whole page was taken over by the error boundary showing "This page couldn't load". Now the mock handler explicitly returns an empty side-question history, and `sideAPI.history` also normalizes the return value (coercing a non-array `items` to `[]`) as defense in depth.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.10] - 2026-09-13

### Network

#### Added

- **Web search adds the official DeepSeek source**: directly reuses the currently active LLM config. It differs in nature from the other three sources - DeepSeek has no directly callable search endpoint, so search exists only inside its Anthropic-compatible endpoint (the `web_search_20250305` server-side tool), executed by the DeepSeek server. It therefore **only supports the official DeepSeek endpoint + the anthropic protocol** (an OpenAI-protocol endpoint rejects server-side tools outright), and each search **consumes an extra model call**, the request **does not go through the search-egress proxy**, is **not recorded in the traffic trail**, and the returned result **has only titles and links** (no summary; fetch the body with WebFetch when needed). The settings page explains these limits but **does not validate or block**; whether they are met is for the user to confirm, and a "Test Search" button can run one real search to verify.

### Agent

#### Added

- **Workers gain cross-work look-back**: adds `search_all_worker_traces` (no need to know the intent_id first - globally keyword-search hit steps across all work's execution processes in this task) and `get_worker_trace` (after pinning a given work, list its step flow, keyword-search in place, and fetch full content by step_id), to reuse observations other work saw but didn't write into a fact, avoiding duplicate work.
- **`node_detail` is given to workers**: paired with the look-back tools above, once a worker has an intent_id / node id it can directly query that node's full detail.
- **A planning round triggered by `add_hint` is explicitly announced**: previously adding a hint only folded it into the situation overview and left the planner to discover it; now a single `add_hint` records a "a person added N strategic hints: ..." trigger for the planner (a batch counts as one, not flooding one by one), and the planner is explicitly told "this round was triggered by a new hint" and sees the hint content directly.

#### Changed

- **The global situation prompt is reworded to a "loosening" tone**: encourages divergent exploration and timely reporting of cross-intent leads, rather than converging too early.
- **Streamlined the worker boundary wording**: being blocked the first time does not mean fully explored; focus on "exhausting the bypass techniques within this intent before concluding".
- `insert_assets` removes the per-asset `related` parameter: its only effect was to decide whether to add this asset into this task's scope, and the value wasn't persisted (re-registering the same asset voided it, and the UI didn't show who was judged irrelevant), so in practice it just made the model do one more judgment that didn't stick.
- **The test scope (`task_scope`) is no longer affected by the asset-coverage switch**: `insert_assets`'s auto-scoping (`source='auto'`) runs whether or not coverage is on, and `add_task_scope` is always provided to the planner / in-task main Agent / goal-decomposition Agent. The scope is the task's authorization boundary and the filter basis for asset queries; the coverage switch only decides whether to use it as the denominator for a metric, and should not decide whether to accumulate the scope itself. Previously turning off coverage disabled both the `auto`/`agent` write paths, leaving `task_scope` with only rows added manually in the UI. `list_untested_assets` is still hidden when coverage is off (it is itself a pure coverage view).

#### Fixed

- **Fixed cross-task asset leakage** (#59): `list_assets` previously hardcoded the task id to 0 and queried the entire shared asset store raw, while the model had no way to write a scope filter, so it treated other tasks' assets (especially IPs) as this task's targets, skewing the test direction. Now it returns only assets within the test scope (`task_scope`) of [this task and directly linked tasks], matching by **attribution** rather than literal value: having some root domain in scope lets you query all its subdomains/services/endpoints, and having some network range lets you query the hosts and services in that range; directly fetching an out-of-scope asset by id likewise returns nothing. A non-task context (Auto/pentest) has no scope to rely on and still falls back to the whole store. It incidentally fixes the attribution blind spot where an IP-direct host (e.g. `http://1.2.3.4/api`) couldn't be matched by a network-range scope. The UI's "test assets" view (filtered by the task's producer) is unaffected.
- **Fixed the worker cross-work look-back tools being wrongly removed**: after `search_all_worker_traces` / `get_worker_trace` (and `node_detail`) were added to the worker's default tool set, an old migration that "converges the worker's tool surface" unbound them again at startup, so workers couldn't actually get these tools. They have been moved out of that unbind list, and workers in databases that already ran the old migration are re-bound once.
- **Fixed occasional DB-write failures for `/btw` side questions**: before the side_question checkpoint is persisted, JSONB-unsupported NUL (`\u0000`) escapes are stripped, to avoid a write error for content containing that character.

### Triggers

#### Fixed

- **A merged trigger session deduplicates task descriptions/goals per task**: when multiple tasks trigger a merge, the same task's description/goal is no longer concatenated into the message repeatedly, to avoid long goals stacking up and blowing out the context.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.9] - 2026-09-11

### Agent

#### Added

- The in-task main Agent supports **multiple sessions**: you can create new ones, switch, and reset context independently, and each session is interactive.
- Support for persistent `/btw` **side questions**: ask without interrupting the main thread, with the Q&A persisted and still there after a restart.
- `spawn_task` adds `source_task_ids`, so a new task can **read-only inherit** the source task's assets and findings.

#### Changed

- Turned off the worker's cross-engagement memory and converged its default tool surface: reading context and cross-work review belong to the planning role, and the worker only handles the execution and write-back of a single intent.
- Streamlined the worker's default prompt; `get_worker_output` removes the `terminated` and `worker_name` fields; streamlined the descriptions of `insert_assets` / `list_assets`.

#### Fixed

- Fixed work being interrupted early due to an **idle round**: the model sometimes spends a whole round outputting only thinking, giving neither body text nor a tool call, and at that point the harness sees a natural end (`end_turn` with no `tool_use`) and ends with `completed` + an empty summary - so an intent not yet done is cut off midway, showing up as "the task ended normally but had no text summary at all". None of the five layers of LLM retry can reach it: it is not an error, the same-provider safe-window retry's entry point is "stream failure", the circuit breaker judges success outright when `err == nil`, and an intent re-run only recognizes `model_error`; the SDK's empty-response retry can't reach it either, because that layer judges emptiness by "whether any event was yielded", and a thinking delta is itself an event. Now work's Stop hook recognizes this kind of round and injects a continue instruction, letting the model carry on to the next step with the thinking it already produced - deliberately not "resend as-is", because such idling is usually determined by the prompt and context shape, is stable behavior rather than random flakiness, and resending would just make the model think it over again.
- Reuses the **empty-response retry count** from the LLM page's "Retry and Backoff" (both address "the model finished but produced no substantive content", just with different criteria and means), default 2; set `-1` to turn it off and revert to the original "idle means end" behavior. It is a **total** cap for one intent rather than a consecutive count: the harness itself hard-limits consecutive idling to one push, and only a real tool round refreshes the quota, so this number guards against a loop like "tool -> idle -> push -> tool -> idle" draining the intent budget. On trigger the log records `[work <worker> · #<intent>] idle round...injected continue instruction (n/N)`. See the LLM retry design doc (docs/) §1.1 for the design.
- Fixed `/btw`'s context budget and input layout in long conversations; in a non-secure context (non-HTTPS access), degrade the generation of the side-question request ID.

### Traffic

#### Added

- Vulnerabilities support **associating multiple pieces of traffic evidence**, which can be ordered, annotated, and distinguished by role (request/response/supporting).
- The report Agent **auto-associates** relevant traffic evidence before writing the report.
- Added an Agent **traffic-binding switch**, and completed the handoff of evidence between Agents.

### Tasks

#### Added

- Support for **session-level vulnerability retest**: a retest runs in a standalone Agent session, with the list and detail showing the run status.

### Intercept

#### Added

- Approval records add **details and an execution audit**: you can view the tool request context, the model/rule initial judgment, the execution output, and the parameter fingerprint.

### Assets

#### Added

- Task test assets support **DSL search**.

### LLM

#### Fixed

- Fixed the test connection not re-sending the custom session header, causing opencode zen to return 400.

### Network

#### Added

- MCP's HTTP transport supports **skipping TLS certificate verification**, to make it easy to connect to services with self-signed certificates.

### UI

#### Added

- The session list is grouped by Agent, with expand/collapse and independent pinning; conversations can be filtered by Agent.
- The attack-chain graph renders compaction (digest) nodes and collapses their members.

#### Fixed

- Fixed a white screen caused by login credentials being out of sync.
- The intercept prompt text is changed to clearly indicate "platform governance", to avoid being misread as a target-side defense.

### Deployment and updates

#### Added

- Support for **one-click update from the UI**: the System Configuration page adds a "Version and Updates" card, and the top bar shows a prompt when a new version is available. The flow is download the release package -> verify `SHA256SUMS` -> smoke test -> stage -> exit and let the supervisor script relaunch to complete the swap, and the page refreshes automatically.
- Added the supervisor startup scripts `start.sh` / `start.bat` as the official launch entry point (included in both release packages and the Docker image, with `install.sh` unchanged), which decide whether to relaunch based on the exit code and forward SIGTERM to artex. The verification and swap logic are all in Go, keeping the scripts dead simple.
- Automatic failure fallback: if verification or the smoke test fails, discard it and keep running the current version; if the new version fails to start 3 times in a row, roll back to the previous version. The settings page also has a manual rollback (note that the database schema is not rolled back).
- Updates recognize only GitHub domains and force HTTPS, and the release source is not configurable; dev builds disable one-click update. GitHub query results are cached for 30 minutes, to avoid the top-bar prompt exhausting the API quota.

#### Known limitations

- Under Docker, only the program is swapped, not the image: the toolchain is not upgraded with it, and recreating the container reverts to the version bundled in the image, so use `docker compose pull artex` when needed.
- The `skills/` in the release package is not synced, so built-in skills newly added in a release do not take effect automatically.
- An update is a restart and interrupts running tasks.

### Dependencies

#### Changed

- Upgraded norma to v0.3.7.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@RuoJi6](https://github.com/RuoJi6)

## [0.3.8] - 2026-09-09

### LLM

#### Added

- The LLM page adds a "Retry and Backoff" tab: both the **count** and **interval** of the five retry layers can be configured. A model call's failure passes, from inside out, through connection-establishment retry (SDK, connection reset / timeout / 429 / 5xx before the stream starts), empty-response retry (SDK, a normal end with no content at all, openai format only), same-provider safe-window retry (stream-break replay before any output is delivered to the caller), round-robin circuit breaking (cool down and skip after consecutive failures reach a threshold), and intent re-run (the whole intent re-runs after the worker ends with model_error) - five layers, with the outer layer coming into play only after the inner is exhausted. Each layer has two knobs with uniform semantics: leave empty = use the original default count and exponential backoff; enter a count to use that count; enter an interval to replace exponential backoff with a fixed interval; enter -1 to turn off that retry layer. The first three layers follow the endpoint and can be overridden field by field in each model config over the global default (one that only pins the interval still inherits the global count); circuit breaking and intent re-run are process-level semantics with only one global copy. Changes take effect hot after saving, with no restart. All empty means the current behavior, and an upgraded old database is byte-for-byte unchanged (new columns default to 0, and an absent settings key means all defaults). The connection test deliberately omits these parameters - it has a hard 30s timeout, and layering user-configured retries on top would just test a usable endpoint into a timeout failure. See the LLM retry design doc (docs/) for the design.
- Each LLM config supports a custom **session header** (`session_header_key`): when non-empty, every LLM request carries that HTTP header whose value is the current run's session id (a chat session is `conv-<id>`, a worker is `exp<x>-worker-i<intent>`, etc.), for gateways that do prompt caching / sticky routing by a session-id header. The implementation reads the session id from the request context and injects it, with no change to norma, so even a shared provider can emit different header values per session. The old database carries a migration, and the frontend LLM config dialog adds an input box.

#### Fixed

- Fixed saving an LLM config reporting `23502` directly when the session-header field is empty: that column is `NOT NULL DEFAULT ''`, and it previously wrongly applied `NULLIF($n,'')`, writing an unfilled session header as NULL and triggering the not-null constraint; now it is passed directly with empty-string semantics.

### Agent

#### Added

- Wall-clock timeout is changed to **settle in place**: relying on norma v0.3.6, when `MaxDuration` is reached it interrupts the running tool and continues for the settlement rounds on the live ctx (writing back identified content + a summary, with a terminal state of timeout), no longer relying on the worker/planner's respective `maxDur+90s` external hard ctx to kill a stuck run outright into `aborted_tools`. Chat goes through the same harness and automatically gets the same behavior, and the mainagent, having no `MaxDuration`, is unaffected.
- Stall fallback: when the planner wakes on a heartbeat / no-change wakeup and the whole graph has no open or running intent, the opening is changed to a stall alert, explicitly telling it that no worker is running and no direction is queued, so this round must produce one or more mutually non-duplicate new intents (it must not produce 0 intents).

#### Changed

- Worker prompt refactor: the intent / startup instruction / anchored-asset raw JSON are moved into the system prompt, reassembled each round, never compacted away, and a continue-run no longer relies on the transcript's first message being retained; the startup user message is slimmed to only the global situation overview (which can be degraded and tolerates being stale). The cost is that the system prompt mixes in per-intent data and loses cross-intent cache reuse - a deliberate trade-off for "not losing the intent".
- Streamlined the planner / worker default bodies and fixed several real-world issues: the planner distinguishes `recent_done` by status (for blocked/exhausted, check the trace first before deciding, not treating it as a dead end nor blindly re-running), negative conclusions are changed to "an observation / in doubt, not final" and evidence is checked before being trusted, when the goal is unmet and there is no open/running intent it must produce (a hard floor), and depth takes priority over coverage; the worker writes only "an observation + a tentative reading" for negative conclusions, the decision belongs to the planner, and cross-intent leads are written into the fact summary for the planner rather than chased by itself. A reseed appends the new default as a new version and switches to it, while user customizations / old versions are kept in history for rollback.
- Converged the work agent's default tool set and asset-write-back prompt: the worker is only responsible for the execution and write-back of a single intent, and reading context / cross-work review belongs to the planning role, so `list_facts` / `node_detail` / `list_companies` and cross-work search (`search_all_worker_traces` / `list_worker_traces` / `get_worker_trace`) are removed from its default tools, leaving only `list_findings` (dedup check before reporting a vulnerability) + `add_finding` / `record_fact` + `insert_assets` / `list_assets`; the prompt drops stale field descriptions that conflict with the `insert_assets` schema (`type=tech`/`on_url`/`props`). The old database carries a one-time migration to strip the corresponding bindings, leaving the planner/main's bindings of the same name untouched.

### Exploration graph

#### Added

- **Exploration-graph cold-node compaction (cold-digest)**: folds old and long-inactive intents / facts into a digest node shown in `graph_overview`, while the original nodes are permanently retained and fully restorable by id (lossless storage, compacting only the presentation, reversible folding). Cold/hot judgment is by reverse reachability + any live branch being hot, with R=6-round debounce and connected-component grouping; background compaction (minor folds uncovered cold blocks, major re-sources and re-compacts to merge fragments) carries an activity re-check and a cooldown mutex, never on the hot path and never overwriting a revived node. The overview side provides `cold_digests` and an asset-indexed `cold_index`, with `expand_digest` / `expand_index` doing the restoration. A linked / inherited task's overview likewise reuses its own folded view, and `expand_digest` supports cross-task read-only restoration. `expand_digest` / `expand_index` are given only to the planner and main agent, not the worker. All carry old-database migrations.
- `graph_overview` outputs the full `finding_list`: findings are the task's highest-value product and usually few per task, so they are now carried in full in the overview (unlike facts, which get only a recent window), letting the planner/worker see all confirmed vulnerabilities at a glance each round without calling `list_findings`. Each is slimmed to `{id, summary, evidence?, from_intent?, assets?}`, where the affected assets are given as readable content directly (url / domain / ip:port) rather than bare ids.

#### Changed

- `graph_overview` no longer flattens `hosts`: the coverage block removes the host list (which in a large-scope task would carry up to 500 host strings per round, of limited value to planning decisions), keeping only `host_count`, with specific hosts queried on demand via `list_assets`. Adds a top-level `done_intents_total` (total count of ended intents), parallel to the `recent_done_intents` truncated to <=15, so the planner knows whether there was truncation when deduplicating.

### Tools

#### Changed

- `add_company_scope`'s default binding changes from worker to planner: defining an enterprise asset scope belongs to the planning / main-control / Auto role, and the worker only performs exploration. A new database seeds the default binding as mainagent/planner/auto, and the old database carries a one-time migration.
- The planner's default binds `list_assets`: it now has both `list_assets` (DSL whole-store search) and `list_untested_assets` (untested within scope), with the old database backfilled once without overwriting a user's unbinding.

### Tasks

#### Added

- The task list adds a "Running Workers" column: counts the intent nodes with `state='running'` under that task, with the exact same definition as the task detail page overview's "Running Workers", showing 0 when there are no running Workers.

### Network

#### Added

- Added a **global egress proxy** config: all target traffic can egress through a unified global proxy (http/https/socks5, with `user:pass` support). When traffic capture is on, it is the upstream of the MITM recording proxy (traffic is still recorded and then egresses through the proxy, with both the intercept and pass-through paths going through the upstream so the source IP isn't leaked); when capture is off, it is injected directly into the agent's bash environment and WebFetch (`proxyEnv` adds `ALL_PROXY` for socks5). The config is stored in the settings KV table (no migration needed), and the frontend system config page adds a "Global Proxy" card, independent of the web-search proxy and the LLM proxy.

### UI

#### Fixed

- Fixed the semantic errors of the intent status labels: `exhausted` "exhausted" -> "budget used up" (really meaning cut off mid-way by hitting the step / time budget, writing back only partial results, not that the direction is fully explored), `blocked` "intercepted" -> "execution error" (really meaning model / API / network failures exhausted retries and the intent essentially never truly ran, not a target / WAF intercept), and added the previously missing `stopped` "stopped" (work manually stopped by the user).

### Dependencies

#### Changed

- Upgraded norma to v0.3.4: MCP tool output adds truncation and disk spill (see `651b961`), and the later v0.3.6 underpins wall-clock settle-in-place.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.7] - 2026-08-31

### LLM

#### Added

- The model config adds an "output limit", with an optional choice of which request field name the limit uses: the limit caps how many tokens one reply generates at most and is sent with each request, where 0 (default) = don't send the field and let the server default decide; it is a different thing from the "context window" - the latter is the model's total capacity, used only locally to compute the compaction threshold and not present in the request. The field-name switch is meaningful only for the `openai` (Chat Completions) format: empty (default) sends `max_tokens`, which the vast majority of compatible gateways recognize; OpenAI's official reasoning models (the o series / GPT-5) conversely recognize only `max_completion_tokens` and return `unsupported_parameter` on receiving `max_tokens`, so such endpoints need a manual switch to the new field. Each format hardcodes its own field name (Anthropic is `max_tokens`, the Responses API is `max_output_tokens`), so for a non-openai format this switch is grayed out and cleared on save. It also wires up a path that was never connected before: planner / worker / chat / main Agent / goal decomposition all never set an output limit, so openai-style didn't send it at all and Anthropic fell back to the SDK's 8192; just adding the config option without wiring this up meant entering a number had no effect. Now the value, like "streaming output", is resolved per round from the config, so it takes effect the next round after a failover switch. An upgraded old database behaves exactly the same (the new column defaults to 0 with an empty field name).
- When clicking "Test" in the backend, the status code and the gateway's raw response body of each HTTP attempt are logged to the server log (the body trimmed to 4K), to help diagnose 401, quota wording, empty frames, an HTML page being returned, etc., instead of only seeing the UI's collapsed ok / err. This log does not depend on the "LLM recording" switch.

#### Changed

- The task LLM config chain can be edited in any state, no longer restricted to "running / paused / config chain exhausted": after a task ends (done / failed / timeout) the main Agent conversation still uses this chain, and when a model on the chain had a problem it previously couldn't be edited and thus couldn't continue to interact. The terminal-state blocks in both the backend HTTP and the DB transaction are removed, and the frontend dialog opens editing and saving for terminal-state tasks too. Saving for a terminal-state task no longer reopens quota-blocked intents (those intents would be moved to open with no worker to run them and no longer meeting the "re-run intent" condition); to keep running, still use re-run intent / add goal, which pull the task back into a running state.

### Agent

#### Changed

- "Send a message to a running Worker to steer direction" is changed to reuse the existing pause / resume + transcript resume mechanism, behaving the same as a main-Agent conversation: it previously went through a self-built intervention-persistence protocol that touched the scheduling barrier, the resume flow, and a dozen-plus activity-query filters. Now the message is injected via the next round's input, the intent runs directly in a dedicated goroutine not limited by the 3-slot worker pool and runs as soon as it is sent; the frontend keeps the message box and restores the "continue directly" button, with sending now carried over SSE. There is no DB schema change. Trade-off: the message is stored only in memory with no crash recovery, and when a message is sent the task may momentarily exceed concurrency by one worker (a low-frequency scenario, acceptable).

### Tasks

#### Fixed

- Fixed an out-of-memory (OOM) crash when archiving a large task: archiving is changed to stream snapshots to disk rather than reading the whole task into memory at once; it also closes three recovery gaps on the cold-archive path - modern installs store traffic only in SQLite, and a crash between the PostgreSQL commit and the SQLite commit would leave an archived task's traffic stuck in hot storage with no way to recover, so now a staging journal is written whether or not there is a legacy directory, and a missing `journal.json` is also treated as discardable.
- Fixed the system stuttering when there are many tasks: there was a clear delay loading conversations and the UI. The queries for the task list, task context, and exploration records are optimized together, and the frontend dashboard, conversation page, and task detail page reduce duplicate requests accordingly.

### Assets

#### Changed

- Removed the "at most 256 rules" limit on the enterprise asset scope: an enterprise that enters its scope one IP / domain at a time easily hits this cap, and once hit can only split into multiple enterprises, while the scope itself isn't slower just for having more entries. The cap is removed in all three places (backend, frontend, and demo mock), a single rule is still limited to 1024 characters, and the 2 MiB request-body cap is kept as a fallback (roughly 40,000-50,000 rules).

### Skills

#### Fixed

- Fixed uploading a skill archive reporting "upload failed: zip: unsupported compression": the Go standard library only bundles the Store / Deflate decompressors, so bzip2 and Zstandard archives written by compression software at non-default levels can't be unpacked at all. Now these two decompressors are added (pure Go, no new external dependency); methods that genuinely can't be decompressed, such as Deflate64 / LZMA / XZ / PPMd, and encrypted archives, are now reported with a clear prompt before decompression, naming exactly which file used which compression method and how to repackage, instead of throwing the underlying English error at the user.
- Fixed the in-skill filename check judging Chinese filenames as illegal: the path check was previously an ASCII whitelist of `[A-Za-z0-9-_./]`, so a single Chinese-named file in the archive (something like a reference/notes.md) failed the whole upload with "the archive contains an illegal path". It is changed to a Unicode blacklist: filenames and spaces in various languages are allowed, while control characters, invalid UTF-8, zero-width and bidirectional control characters (RLO filename spoofing), `\ % # ? * : " < > |`, and `..` / absolute paths / empty path segments are still rejected, with the directory-traversal defense unchanged. The skill name is likewise loosened - ASCII is still limited to lowercase letters, digits, and hyphens (the agentskills.io spec), non-ASCII letters such as Chinese can be used directly as a skill name, but spaces, dots, and path separators are not accepted.
- Fixed Chinese archives produced by Windows compression software having garbled filenames after extraction or the whole archive being rejected: such zips don't set the UTF-8 flag and write filenames in GBK, so now a GBK fallback decode is done before the path check. Also, quoted frontmatter like `name: "Chinese skill"` now has its skill name read correctly.

### UI

#### Added

- The findings page adds an "All Findings" flat view, set as the default, switchable from the original "grouped by task" view via a header tab: the grouped view requires expanding each task to see vulnerabilities, which is a detour when you just want to scan the list across tasks. The flat view is one big cross-task table (10/20/50/100 per page), behaving exactly like the grouped view - check to export, expand in-row to see evidence and the detailed report, in-row edit name/category/severity, change disposition status, drill in, and delete. The two views share the stat cards and filter bar, switching doesn't lose the filter, and the current view and filters are stored locally; polling hits only the current view, and in-row changes are written to both caches so switching won't show stale data.
- The findings page adds a "by asset" view: on the left an asset tree (enterprise -> root domain / IP -> subdomain -> service -> endpoint, with the enterprise level appearing only when an asset really has an attribution), and on the right the findings under the entire subtree of the selected node, sharing the same table and the same filters as the other two views. The tree collects only assets that have findings, backfilling the ancestor chain on demand (even when findings hang only on the deepest endpoint, the layers above can still be reconstructed), and a node's count is the subtree aggregate deduplicated by finding; findings whose asset is deleted or that never had an associated asset go into an "Unassociated Assets" bucket. Unlike the other two views, the asset view doesn't poll - it queries only on entering the view, changing filters, adding/deleting/editing a finding on the page, or clicking the tree's refresh button, since the left tree is a navigation structure with no need to recompute every 5 seconds. Each level of the tree shows only the delta relative to the previous level (a subdomain drops the root-domain suffix, a service shows `https :443`, an endpoint shows only the path), with the full value in the hover prompt and breadcrumb; when there are too many nodes, a whole level's endpoint/service hierarchy is dropped with a prompt, and the counts still roll up to the upper level.

#### Fixed

- Fixed session records in the mobile session detail being squeezed and unreadable: on a narrow screen the session list is collapsed, giving the width to the records themselves.

### Dependencies

#### Changed

- Upgraded norma v0.3.2 -> v0.3.3: adds `Config.MaxTokensField`, letting OpenAI Chat Completions' output limit be sent as `max_completion_tokens` instead (reasoning models recognize only this key). The two keys are mutually exclusive and only one is sent, still defaulting to `max_tokens`.
- Upgraded `golang.org/x/mod` v0.37.0 -> v0.40.0, fixing two dependency security advisories.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)
- [@begininvoke](https://github.com/begininvoke)

## [0.3.6] - 2026-08-27

### LLM

#### Fixed

- Fixed the "non-streaming" switch reverting to "streaming" after being saved and reopened (#69): the config list API's DTO was missing the `streaming` field, so the response never returned it, and the frontend reading `undefined` always fell back to the default streaming; the actual write and DB storage were fine, it just couldn't be read back. The DTO restores the field (without `omitempty`, so `false` must also appear in the response).
- The test connection is changed to verify the model actually replies and to test in the config's real send/receive mode (#65): it previously only judged whether the HTTP succeeded, so a request that went through but got zero reply from the model (thinking burning the budget / the body swallowed by a safety policy / the compat layer dropping `content`) also reported "connection succeeded", at odds with the "no reply" behavior in a real session; and the test always went streaming, so a non-streaming config was actually testing a different channel. Now an empty reply is judged a failure outright, the model's reply is shown in the prompt on success, and the current streaming switch is carried into the test, keeping "the test passes" consistent with "a session works".

### Agent

#### Changed

- `list_facts` is changed to pagination + keyword filtering, to avoid a single call blowing out the context when there are many facts (#74): it returns the latest 20 by default, supports `limit` (cap 100) / a `before` cursor / a `q` summary keyword, and returns `{facts, total, has_more, next_before}`; an overly long single summary is truncated by character count (the full text still via `node_detail`). The worker / planner prompts are updated to the pagination semantics. The old database flushes the new parameter schema into the tool catalog table via a one-time migration (`SeedTool` is insert-only, otherwise the tool management page shows "no parameters").

### Tools

#### Added

- The "Tool Execution" page adds tool-call count statistics (#72): the toolbar's "Stats" button opens a dialog showing, per tool, the call count, proportion, and failure count (descending by count); it follows the list's task / keyword filters, counts the whole result set rather than the current page, and fetches only when the dialog opens.

### Dependencies

#### Changed

- Upgraded norma v0.3.1 -> v0.3.2: fixes reasoning/refusal silently dropped in OpenAI-style responses (Responses adds `reasoning_text`), dedup of the reasoning field's three names, bounded retry on an empty response (zero content blocks), and a gateway 400 caused by a compaction boundary cutting a tool pairing (also fixing the existing orphans already baked into the transcript).

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)

## [0.3.5] - 2026-08-25

### LLM

#### Added

- Each LLM config supports a streaming/non-streaming toggle (streaming by default): on goes streaming SSE; off goes true non-streaming (`stream:false`, returning the complete JSON at once), which can bypass some gateways' bad SSE implementations (empty frames, dropped thinking-field frames), at the cost of losing real-time progress and real-time token counting during the run. The worker / planner / mainagent / chat / goals all take the value dynamically from the currently active config; all three Provider wrappers (`llmpool` / `llmrec` / task runtime) are compatible with non-streaming; `llm_profiles` adds a `streaming` column and `ALTER`s the old database (defaulting to `true`, so old configs notice nothing).
- Support for the OpenAI Responses API format in LLM configs: each config adds a third format `openai-responses` (hitting `POST /v1/responses`), alongside Chat Completions / Anthropic; `BaseURL` is normalized, with the default model `gpt-5`; the `format` constraint on `llm_profiles` adds `openai-responses` and idempotently migrates the old database; the frontend format dropdown adds "OpenAI (Responses API)". Depends on upgrading norma to v0.3.1 (including the `reasoning_content` round-trip fix).
- Recording the raw HTTP text of LLM requests/responses: captures the real wire body at the HTTP transport layer, preserving the tool schema, `tool_use` blocks, and raw SSE frames not visible in the normalized view; the multiple attempts of norma's internal retries are preserved one by one; `llm_records` adds the two columns `raw_request` / `raw_response` and `ALTER`s the old database; the recording page detail panel adds a "raw text" view toggle and request/response copy buttons (with an `execCommand` fallback for non-secure contexts).

### Conversations

#### Changed

- The session list's multi-select is changed to a "multi-select" mode toggle: by default the list no longer always shows a per-row checkbox (cleaner look), and the header is changed to a "N total + Multi-select" button; clicking "Multi-select" enters selection mode (checkboxes, select all, batch delete), "Done" exits and clears the selection, and when a batch delete all succeeds it auto-exits (if any fail it stays in selection mode for retry); single rename / pin / delete still go through each row's ⋯ menu.

### UI

#### Changed

- Enhanced the task-management, session-operation, and traffic-viewing experience (#57): the task list's column-sort preference can be persisted, in-row rename is triggered directly by an icon (no longer via a menu), and the sheet interaction and traffic-viewing details are refined.

#### Fixed

- Worker asset labels show only domains and IPs, and correctly handle the empty-label case.

### Agent

#### Added

- The planner adds a "quantitative acceptance check" when judging a goal: when the goal contains a quantifiable acceptance condition (asset test coverage reaching X%, obtaining N flags, obtaining a certain privilege), before `prove_goal` it must check the measured values in `graph_overview` (`coverage.pct`, finding counts, etc.), and if the measured value is below target, `prove_goal` is forbidden and it instead dispatches intents to close the gap, not marking it met early on the grounds of "the main part is done / basically achieved". This fixes a goal requiring 100% coverage being judged complete at a measured 40%.

#### Changed

- Rewrote the basis for "0 intents this round" in the planner prompt: the original text described 0 intents as "the most common, most important principle", which made the planner give up too early when the goal was unmet and there was still untested surface in scope. It is now changed to produce 0 intents only in two specific cases - (1) all the directions it can think of are already covered by open/running/recent_done intents; (2) the next step depends on the output of a currently running work and that output hasn't appeared yet (it should wait for that to finish and plan on the next wakeup after the graph updates). And it adds a reverse constraint: when there is genuinely a new direction uncovered and not dependent on running work, or the goal is unmet with untested surface remaining, don't give up because "0 intents is common".
- Strengthened the "evidence threshold for a negative conclusion" in the worker prompt: for a negative conclusion such as "not injectable / port closed / no login entry" that could make the planner give up a whole direction, it requires exhausting the reasonable techniques within that intent (changing encoding/parameter/path/method) before concluding; if the techniques aren't exhausted or the evidence is weak, it is marked `confidence=inferred`, to avoid welding a whole route shut with a hasty `observed` negative (an erroneous negative early in a task especially skews the direction and is hard to self-heal).
- The above planner / worker default-prompt changes are appended as new versions via a one-time migration (`reseedPlannerPrompt` / `reseedWorkerPrompt`, each guarded by a settings flag) and switched to the current version; old versions are kept in the version history, and users who customized a prompt can restore it from the version record.

### Operations

#### Added

- Added `reset-password.sh` to reset the admin (username fixed as `ARTEX`) password: supports both local / docker deployments, with connection info either specified explicitly or auto-read from `--dsn`/`$ARTEX_PG_DSN`/`config.json`; it generates, inside the database, a bcrypt hash compatible with backend login via `pgcrypto` and writes it back to `settings.auth.password_hash`, with no service restart needed after reset. The password is passed in via an environment variable, never entering the process argv, and is escaped to prevent injection.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

## [0.3.4] - 2026-08-24

### UI

#### Added

- After checking multiple tasks in the task list, you can batch-change their category, with the target category supporting "Uncategorized" to move tasks out of the current category; the whole batch write completes in one transaction, and the only failed items would be tasks already deleted after being checked.

#### Changed

- The "Task Category" of a new task is changed from a dropdown to a searchable input box: typing filters the existing categories, a name not in the database is created and selected instantly on Enter (or by clicking "Create" in the dropdown), and the selected item is shown as a removable tag; still limited to a single category.

### Conversations

#### Fixed

- Fixed sending a message being blocked by "LLM not configured, cannot converse" when the session had a selected LLM config but there was no globally active config. The pre-send check originally looked only at the global active config, while execution actually prefers the session-selected config, so the two resolution logics were inconsistent; they are now unified into one (session/Agent binding first, global as fallback).
- The "cannot converse" prompt is broken down by state: no config at all prompts "Add a config", a config present but not activated prompts "Activate one or assign one to this session", no longer always showing "not configured", to make it easy to locate.

### Agent

#### Added

- For a task whose goals are all achieved, an intent dispatched by the main Agent via `add_intent` can be claimed and executed directly by a Worker and runs to completion: at this point the Planner no longer runs (with no open goal the task doesn't enter planning), avoiding it re-judging the goals as achieved and cancelling the just-dispatched intent; once the frontier is drained the task returns to completed. Before dispatching an intent the main Agent asks the user, based on the intent content, whether to register it as a formal goal, and registering it restores the task to normal autonomous planning.

#### Changed

- When `get_worker_trace` / `get_task_worker_trace`'s `step_ids` exceeds the per-call cap (5), it no longer errors outright; instead it returns the full content of the first 5 steps and, via `returned_step_ids` / `omitted_step_ids` / `notice` in the result, tells which were fetched this time and which remain, and prompts "no need to fetch more if these are enough". Duplicate and illegal ids passed in are deduplicated and removed before counting.

#### Fixed

- The task list is changed to descending by task id (newest created first), replacing the original sort by creation time. Multiple tasks created at the same moment have identical timestamps and sort unstably, which combined with the 10-second polling and the in-memory map's random traversal order made them frequently swap positions in the list.

### Assets

#### Fixed

- Fixed enterprise-attribution recomputation being broken by a single row of dirty data: when `assets.ip` stored a hostname (written by an Agent or the asset API), `a.ip::inet` would throw `22P02`, making adding/editing an enterprise asset scope and deleting an enterprise all fail and roll back. Changed to the safe conversion `try_inet()`, which skips an illegal value rather than aborting the whole statement.
- An unparseable `ip` is no longer silently skipped: after saving an enterprise scope, it clearly prompts how many assets have an `ip` that isn't a valid IP (including the specific id and value), noting that these assets won't be attributed by IP/CIDR rules; the same info is also written to the server log, covering paths with no frontend response (deleting an enterprise, scopesentry sync, Agent writes).
- The task test scope's IP/CIDR matching likewise switches to `try_inet()`, replacing the original regex guard that only checked the character set (a hostname of all-hex letters like `abc.def` previously still slipped through and triggered the same error).

#### Changed

- The `ip` field of `ip`, `service`, and `endpoint` assets no longer accepts a hostname: `insert_assets` and the asset API return a per-item error with an `index` and give the fix directly (use `type=subdomain` and fill `domain`, or resolve the A/AAAA record first), so the Agent can self-correct and re-insert. Other assets in the same batch are unaffected and stored as usual.

### Contributors

- [@neouks](https://github.com/neouks)

## [0.3.3] - 2026-08-23

### Worker

#### Added

- A running Worker supports per-item pause, resume, and cancel; a pause retains the intent, facts, and vulnerabilities, and a cancel transactionally cleans up the current intent and its direct outputs after execution exits.
- Added a `paused` intent state, an execution barrier, and named termination reasons, to prevent late blackboard writes after a pause, cancel, or task deletion.
- Added an optional task concurrency cap; new tasks, resumes, vulnerability drill-ins, and queue refills all go through a unified persistent FIFO admission path.

#### Changed

- The Worker's per-run wall-clock default duration is changed from 600 seconds to 1200 seconds; a timed-out task, once revived, re-times from its next real execution.
- The real execution state of the Worker, Planner, and main Agent uniformly drives the task status badge, fixing the Worker showing "idle" while running.
- A Worker session retains per-item controls and the current session's token total, and the model name is moved to the right of the current session title.

#### Removed

- Removed the Worker multi-select, select-all, and batch pause/resume UI, and removed the Worker batch-control API and Mock contract.
- Removed the token badge and tooltip in the Worker list rows; the underlying full token ledger and task-aggregation API are retained.

### LLM

#### Added

- Tasks support an ordered LLM config chain; when a provider's quota is clearly recognized as insufficient, it automatically switches to the next config, and the current config, exhaustion state, and error summary are persisted.
- Running and paused tasks support editing the full config chain, reordering, and manually switching the current config, taking effect from the next LLM call.
- Auto-switch, manual switch, and whole-chain exhaustion write structured system activity and pop a dedupable reminder via the task activity stream.
- Added a task role-model resolution API that uniformly resolves the config and model the Main Agent, Planner, and Worker will use on their next call.
- Added an optional global LLM Pool, supporting a configured call order, per-model failure fallback, health status, cooldown recovery, and manual reset.
- Added an always-on LLM usage ledger that aggregates input, output, and cache tokens by task, session, model, and config.

#### Changed

- Goal decomposition, the Planner, the Worker, and the main Agent share a task-level LLM runtime (resolution priority below).
- Failover responds only to an explicit quota, balance, or billing error; ordinary rate-limit, auth, network, server, and context errors don't wrongly switch.
- The LLM config page is changed to a list of config cards with a right-side drawer for editing, and provides the Pool round-robin order, priority, exclusions, health status, and recovery actions.
- Config-chain exhaustion info supports wrapping on a narrow screen; the current model changes from an icon in the session list to a text label and full tooltip next to the current session title.
- The order in which each in-task role resolves its LLM is adjusted to "Agent binding -> task config chain -> global": a role with an explicitly bound model always runs on that model, only an unbound role falls to the task chain, and only when the task chain is empty does it fall to the global.
- Leaving the outbound proxy empty means a direct connection, no longer falling back to the `HTTP_PROXY`/`HTTPS_PROXY` environment variables (an explicit `ARTEX_LLM_PROXY` is unaffected); the proxy input supports `socks5://user:pass@host:port` with credentials.

#### Fixed

- A transient streaming failure before submission (before any output is produced) is retried with safe backoff on the same provider, significantly reducing "ran only one or two tools and then stopped, with no summary and a terminal state of `model_error`"; quota exhaustion, over-long context, and 4xx deterministic errors are not retried and are still handled by failover and compaction recovery respectively.

#### Removed

- Removed the LLM icon marker in the session list, to avoid confusion with the Worker status icon.

### UI

#### Added

- Tasks support global category create, rename, delete, and filter; a new task can select a category directly.
- Added global task-template CRUD and a right-side management drawer; a new task can load a preset description and goals, or save the current content as a template.
- A new task supports linking multiple source tasks and multiple enterprise asset scopes; an enterprise scope auto-recognizes domains, URLs, IPs, CIDRs, ICPs, and enterprise keywords from a single multi-line text box.
- Task test assets support real-time add and delete, recording the source as manual, enterprise, inherited, or Agent-discovered; the current test assets and a source summary are shown next to the Worker session title.
- The findings page is grouped by task with independent in-group pagination, and supports writing a description for a vulnerability and creating a high-priority drill-in intent.
- Conversations support rename, pin, unpin, and delete; the task list supports batch pause and resume for the current page.
- Traffic detail and tool-execution detail are changed to a right-side drawer; HTTP messages fill in the `Host` and highlight the request line, status code, headers, JSON, and markup bodies.
- The task list's action column adds "Detail" and "Pause/Resume" buttons; the session send key can be configured in System Settings (`Enter` / `Cmd+Enter`, etc.), and the web-search area adds a proxy input box.
- The badge to the right of the session title is changed to show the LLM config name, with the model ID moved to the hover tooltip; a task can be given an optional name, and the task list adds a name column and search (falling back to the description when empty).
- The skills page adds usage count, last-used time, and missing-dependency statistics, to make it easy to locate a skill that didn't take effect or isn't installed.

#### Changed

- The task title is changed to a focusable detail link; task assets, enterprise assets, finding task groups, and in-group vulnerabilities use real server-side pagination, stable ordering, and an exact total count.
- Adding an enterprise is changed to a right-side drawer, with scope entry unified into multi-line text and providing real-time recognition, validation, and type preview.
- The main Agent input box supports auto-grow, `Enter` to send, and `Shift+Enter` for a newline, and avoids a false send during a Chinese IME composition.
- Agent preview, task reports, and related details uniformly use a shared Markdown renderer; delete confirmations, long errors, and mobile drawer widths are fixed together.
- The task-template selector displays and searches by template name and submits by template ID; the system's original font size is restored, and the app version is uniformly shown as `0.3.3`.
- The task total and current-session tokens highlight only the input, cache-read, and output values; the send button uses a cleaner up-arrow icon.
- The task overview's test scope shows the enterprise name instead of the ID.

#### Fixed

- Ordinary text containing a dot is no longer misjudged as an ICP filing number.
- Fixed a name clash in the variable catalog with a global runtime variable (such as `{{.Now}}`) causing the Agent editor's variable list to render a duplicate key.

#### Removed

- Removed the task card's standalone "Enter" button, unifying entry to the detail by clicking the task title.
- Removed the dashboard's "New Task" button, the Logo URL input when adding an enterprise, and the vulnerability-count badge at the top of the findings page and in the task groups.
- Reverted the global 10% font enlargement, and removed the Worker selector, model icon, and row-level token stats in the session list.

### Agent

#### Added

- A new task can directly link multiple existing tasks, read-only inheriting in real time the goals, facts, vulnerabilities, completed intents, asset scope, and blackboard context of its direct source tasks.
- Blackboard-read tools support on-demand querying of source-task nodes, facts, vulnerabilities, and execution traces; inherited nodes carry a source marker and all write tools refuse to modify them.
- Enterprise-scope tools support domains, URLs, IPs, CIDRs, ICPs, and enterprise keywords; keywords are only an Agent scope hint and don't participate in automatic asset attribution.
- A vulnerability drill-in creates, in the original task, a high-priority manual Worker intent with an asset anchor and a `derived_from` edge.
- The overview adds a "Goal Management" card, supporting manual view, add, edit, and delete of goals; adding or editing notifies the Planner and revives the task, while deleting hard-deletes the goal node (cascading edges and anchors) but does not revive.
- The main Agent gets the `steer_work` tool by default, to inject a real-time correction instruction into a running Worker without interrupting it or losing progress (still verifying the intent belongs to this task).

#### Changed

- Source-task intents do not enter the new task's frontier, and the new task continues to use an independent exploration, execution queue, working directory, and conversation history.
- Deleting a running intent no longer destroys data: it is instead stopped to `stopped`, requires a deletion reason, hangs the reason as a fact on that intent and writes it into the payload, and the planner perceives it via a `cancelled` trigger (keeping the intent content and reason).
- Main Agent conversations are changed to be driven by the server-side activity stream, with the input recoverable on a send failure; opening a session auto-sticks to the bottom and keeps the last reply visible after the detail lazy-loads.
- Pausing a task terminates the current Main Agent, Planner, and Worker calls, but doesn't prevent the user from actively starting a new main-Agent orchestration session during the pause.
- Cancel, close, and stream-break uniformly preserve the real termination reason, generated content, run rounds, elapsed time, tokens, and unreturned tool calls.

#### Removed

- Removed the main Agent client's optimistic message echo, to avoid duplicate messages and cross-session stream bleed under pause, failure, or concurrent activity.

### Constraints

#### Added

- The goal-decomposition stage extracts operation constraints (allow/deny) before decomposing goals, and the main Agent can add to them at runtime; constraints are injected into the Planner and Worker system prompts at the highest priority, and after de-biasing, diversity and surface-expansion exploration explicitly obey the constraints.
- The overview adds a "Constraint Management" card and CRUD APIs; the constraint injection scope can be toggled separately for Planner / Worker (both on by default, read each round, effective immediately). The new table `task_constraints` uses `CREATE TABLE IF NOT EXISTS`, and the old database is auto-created.

### Intercept

#### Added

- Command intercept adds a model fallback approval beyond regex/string rules: when no rule matches, the model makes an `ALLOW`/`ASK`/`DENY` semantic judgment, with the failure-fallback action and the approval-timeout action configurable; the intercept page splits into two tabs, "Intercept Rules / Model Config", and a model-decision result is labeled with a `[model]` prefix and reason.

#### Fixed

- Hardened the judge output parsing to avoid a correct `DENY` decision being mistaken for an allow.

### Traffic

#### Changed

- Traffic recording is changed to store a whole exchange into SQLite (large bodies spill to a hash-deduplicated blob bucket), with a trigram full-text index built on text bodies supporting arbitrary-substring and Chinese search; deletion degenerates to a single SQL transaction, dropping from hours to milliseconds without stalling recording in the meantime. Added large-body streaming download and a `traffic_search` full-text parameter; old file-tree data needs no migration and is still readable, searchable, and deletable.

### Tasks and assets

#### Added

- Task creation adds an "asset coverage feature" switch (on by default): when off, coverage is not computed or displayed, the situation graph shows only assets, `task_scope` is not accumulated automatically, and the related tools are removed from the Planner / main Agent; enterprise linkage is unaffected by this switch.
- `insert_assets` adds a `related` marker per asset (default `true`): effective only when coverage is on, with `false` only storing to the shared asset store and not counting toward this task's coverage (such as an incidentally discovered neighboring site or an irrelevant asset).
- The enterprise scope and task test assets uniformly support text recognition of domains, URLs, IPs, CIDRs, ICPs, and keywords; a domain/IP can create or reuse a global asset, while CIDR/ICP/keyword are retained as task-scope context.

### Build

- `build.sh --release` supports building Linux amd64/arm64, macOS amd64/arm64, and Windows amd64 at once, and generates zip release packages with `skills/`, a config example, and the README.
- Uses the Go linker to strip debug info and generates zip release packages; UPX is changed to explicitly optional, to avoid its self-extracting ELF causing a segfault at startup in some Linux environments.
- The Release Workflow runs a startup smoke test on the Linux amd64 binary and ships `SHA256SUMS` with the release packages.

### Contributors

- [@neouks](https://github.com/neouks)

## [0.3.2] - 2026-08-20

### Added

- A new task supports linking multiple existing tasks, read-only inheriting in real time the facts, vulnerabilities, completed intents, asset scope, and blackboard context of its direct source tasks; the new task still uses an independent execution queue, working directory, and session history.
- Tasks support an ordered LLM config chain. When a provider's quota is clearly recognized as insufficient, it automatically switches to the next config, and the current config, exhaustion state, and structured audit activity are persisted.
- Running and paused tasks support editing the LLM config chain, reordering, and manually switching the current config; auto-switch, manual switch, and whole-chain exhaustion are shown on the task detail page.
- A running Worker supports pause, resume, and cancel. A pause retains blackboard data, and a cancel transactionally cleans up that intent and its directly produced facts, vulnerabilities, and execution records after the Worker stops writing.
- Deleting a task can optionally also clean up the associated assets, traffic, vulnerabilities, and task-landed files, and adds a deletion barrier, concurrency protection, and auditable deletion statistics.
- Added an optional task concurrency cap; new tasks that hit the cap queue FIFO and auto-start when a run slot frees up.
- Added task-asset pagination, company-asset pagination, task-category statistics, and a paginated-intent Mock contract.
- Added the `build.sh` single-binary build script, supporting static frontend export, asset embedding, cross-platform targets, and build-version injection.

### Changed

- A task-level explicit LLM config chain coexists with the existing global LLM Pool; the explicit chain continues to use strict quota-failover semantics, and with no explicit chain it follows the Agent binding and global config rules.
- The main Agent input box supports auto-growing multi-line input, `Enter` to send, `Shift+Enter` for a newline, and avoids a false send during a Chinese IME composition.
- The Planner, Worker, and main Agent share a task-level LLM runtime; the task chain uses the smallest context window among the candidate models as the safe compaction threshold.
- The task detail page shows running, idle, and paused states by real LLM-call status; a source task's facts, vulnerabilities, intents, asset references, and graph nodes are uniformly marked with their source and kept read-only.
- Agent preview, task reports, and related detail views uniformly use a shared Markdown rendering component.
- The LLM model config adopts a card and drawer interaction, with the model list supporting independent scrolling inside the drawer.
- Pausing a task no longer blocks main-Agent conversations: a main-Agent orchestration session is independent of a task pause, and new messages can still be sent during the pause (the pause only terminates the current in-progress round).
- The Worker's per-run wall-clock default duration is changed from 600 seconds to 1200 seconds.

### Fixed

- Removed the main Agent console's optimistic echo, changing to pure server-side data rendering, fixing message disorder and bleeding into other sessions' content under pause or send-failure scenarios.
- Opening a main Agent session defaults to scrolling to the bottom and fully showing the last reply: the last reply's full content auto-sticks to the bottom after lazy-loading and is no longer pushed off screen.
- Fixed the main Agent session still running after pausing a task, and the orchestration Agent not stopping the main Agent in sync when it paused the task.
- Fixed the task status badge and action-button state being out of sync when a task completed or the Planner, Worker, or main Agent was actually running.
- Fixed possible late blackboard writes or leftover files between a Worker cancel, a task delete, and a concurrent write.
- Fixed the Markdown preview failing, delete-confirmation long-text overflow, mobile width, and some task-detail buttons being misaligned.
- Added a named termination reason to all Agent cancel paths, so the activity detail can show the canceller, terminal state, run rounds, elapsed time, token usage, and unreturned tool calls.
- Fixed a race where the parent context could preempt and overwrite the named `shutdown` reason on backend shutdown, and Chinese activity summaries being garbled by byte-based truncation.
- Fixed a cancel event with partial streaming output losing the real termination reason, while preserving the content generated before the cancel.

### Contributors

- [@Autumn-27](https://github.com/Autumn-27)
- [@neouks](https://github.com/neouks)

[Unreleased]: https://github.com/Autumn-27/ARTEX/compare/v0.3.10...HEAD
[0.3.10]: https://github.com/Autumn-27/ARTEX/compare/v0.3.9...v0.3.10
[0.3.9]: https://github.com/Autumn-27/ARTEX/compare/v0.3.8...v0.3.9
[0.3.8]: https://github.com/Autumn-27/ARTEX/compare/v0.3.7...v0.3.8
[0.3.7]: https://github.com/Autumn-27/ARTEX/compare/v0.3.6...v0.3.7
[0.3.6]: https://github.com/Autumn-27/ARTEX/compare/v0.3.5...v0.3.6
[0.3.5]: https://github.com/Autumn-27/ARTEX/compare/v0.3.4...v0.3.5
[0.3.4]: https://github.com/Autumn-27/ARTEX/compare/v0.3.3...v0.3.4
[0.3.3]: https://github.com/Autumn-27/ARTEX/compare/v0.3.2...v0.3.3
[0.3.2]: https://github.com/Autumn-27/ARTEX/compare/v0.3.1...v0.3.2
