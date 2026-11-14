# Side-question long conversations and context budget

Fixed 2026-09-11. The original implementation treated the character count of the request JSON directly as tokens and inherited the main task's 32K output reservation, so it would reject valid side questions prematurely when there was a lot of HTML, JS, or tool-result content.

## Open-source implementation review

- [Grok CLI side-question context](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/agent.ts#L739): extracts fragments from the most recent user and assistant text, with a character budget of about 2000 and at most 400 characters taken per item. It does not maintain a continuous side-question history on this path.
- [Grok CLI standalone request](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/utils/side-question.ts): independent cancel signal; output capped at 2048 tokens when the model supports it; no tools provided.
- [Grok CLI main-session compaction](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/compaction.ts): estimates tokens, retains recent content, folds new content into the old summary, and handles cross-turn truncation.
- [OpenCode session compaction](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/compaction.ts): full-request estimation, output/buffer reservation, recent content plus a rolling summary, and tool-free summary requests; this implementation defaults to a recent budget of 8000 and a summary output cap of 4096 tokens.
- [OpenCode overflow recovery](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/runner/llm.ts): overflow recovery is attempted only before assistant output has started, and the post-recovery call does not enter the same overflow-recovery path again.

ARTEX borrows the independent output budget, the recent-content-plus-rolling-summary approach, and bounded recovery. It keeps norma v0.3.6's structured messages and tool pairing, does not copy Grok's text excerpting, and does not write OpenCode's main-session compaction events into the ARTEX main transcript.

## Request budget and execution

- Messages follow norma's per-content-block UTF-8 byte estimate with a 4/3 margin; system prompts, tool schemas, and message framing overhead are also counted. The estimate is not an exact model token count.
- Side-question output defaults to at most 8192 tokens and does not exceed the output cap already set by the main configuration. The service environment variable `ARTEX_BTW_MAX_OUTPUT_TOKENS` can set a cap of 256–32768; it does not modify the product default model or the main task parameters.
- The input budget is the context window minus the output cap and a safety margin; an unknown window uses the platform default of 200K. The safety margin is 5% of the window, with a minimum of 128 and a maximum of 8192 tokens.
- Successful question-answer pairs are loaded in batches of at most 20 by increasing sequence number. At most 20 pairs are kept verbatim, with a token budget of at most 1/4 of the input budget and no more than 16K.
- Excess pairs are folded into the rolling summary. The summary carries the history source and context time; a historical assistant answer is not equivalent to new tool evidence, and on conflict the latest main snapshot takes priority.
- When the main context is still too long, only the older messages in the copy are summarized, keeping at most 8K tokens of recent content; the cut point does not split a tool call from its result. An oversized single group is summarized as a whole.
- Summary input is chunked UTF-8-safely according to the actual remaining window, with an output cap of 2048 tokens; an empty summary, truncation, a tool call, or exceeding the summary budget all skip caching. A single side question makes at most 12 summary calls and is subject to the same 120-second timeout; reaching the limit fails explicitly and does not loop forever.
- If the model first returns a context overflow and has not yet emitted text or a tool call, it retries at most once after further reduction; if the estimated size does not decrease, recovery stops immediately. Other model errors and partial streaming output do not trigger this recovery.
- All usage obtained, including summaries, failed attempts, and usage at cancellation, is accumulated into the same side-question request. When the Provider returns no usage, only zero can be recorded; an estimate must not be passed off as actual usage.

## Persistence and interface

`side_question_sessions.memory` stores the summary of old question-answer pairs, the covered ordinal, and the main-context summary cached by snapshot identity. `side_question_requests.context_info` stores the preparation stage, the actual number of replayed pairs, summary usage, and the budget estimate.

A summary is saved only while the original request is still running and the cleanup version matches; clearing also clears the cache, and a late write does not restore already-cleaned data. A new snapshot does not reuse an old snapshot's summary. The summary fields are saved with v3 task archives; when restoring an old v3 missing those fields, empty objects are filled in, and v1/v2 remain compatible.

POST accepts and returns the request first, with preparation and compaction running in the background, holding no admission lock or database transaction. SSE/history show the preparation, question-answer organization, copy-compaction, and answer stages; a compaction failure is saved as the terminal failure state of that side-question request. The frontend preserves the error and restores the draft of the failed question, does not cover the input box with a floating notification, and history polling no longer clears a submission error.

## Validation record

- 19/20/21/50-pair replay, retention of old conclusions across 20 pairs, and summary-cache reuse across restart: automated checks pass.
- Very long Chinese answers and code context, chunked request budget, tool pairing, snapshot immutability, and new-snapshot cache invalidation: automated checks pass.
- Summary failure/cancel/truncation/oversize/tool return, clear race, call cap, and only-once overflow recovery with no retry on partial stream: automated checks pass.
- Standalone PostgreSQL pagination, restart, v1/v2/v3 archive, summary-cache and budget-metadata archive restore, old v3 missing new fields, and 20 parent sessions sharing four concurrency slots: pass.
- Go candidate service build, frontend TypeScript check, Biome check of modified components, and a Next.js production build in a standalone directory: pass.
- The built-in browser used a standalone UI fixture and verified, at 1280×720 and 390×844, the organization stage, the summary-scope hint, draft recovery after failure, the absence of floating error notifications, the absence of horizontal overflow, and an error-free console. The temporary fixture has been removed.
- A read-only replay of this machine's existing Worker snapshots passed the new budget check; for example, Worker #3's 293085-character snapshot is no longer wrongly rejected by the local character count. This item makes no external model calls.
- A real-conversation test that would send a private Worker snapshot to Grok was rejected by the automated approval review, was not executed, and does not count as a passing item.
- At 2026-09-11 00:37, per the user's request, the local backend was restarted, reusing the original database, data directory, and login configuration. The running file and the candidate binary have matching SHA-256 values; `/api/health` returns normal for both the backend and the frontend proxy.

Validation commands (using the standalone test database only):

```sh
go test -race ./sidequestion ./db ./server -run 'TestSide|TestCheckpoint|TestSnapshot|TestBuildRequest|TestService|TestMainSide|TestTaskArchive' -count=1
go build ./cmd/artex
npx tsc --noEmit
npm run build -- --webpack
```

The frontend production build uses a standalone copy to avoid overwriting the currently previewed `.next`. The candidate service is at `/private/tmp/artex-btw-budget-candidate`, has been copied to `/private/tmp/artex-btw-preview/artex` and started; the original binary is backed up as `artex.before-context-budget` in the same directory.
