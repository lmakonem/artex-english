# `/btw` validation record

Date: 2026-09-10. Branch: `codex/btw-side-question`. Baseline: `8dae851b9b622f2ff2631f332fde9719d0b16fba`.

A standalone PostgreSQL test database and data directory were used; real model credentials were injected only into the standalone test environment, were not written into code or this record, and the product default model was not modified. Go 1.26.3, norma v0.3.6, Next.js 16.2.9.

The real model conversations, returned objects, engineering assertions, and the raw Qwen review text are saved in [validation-2026-09-10.json](validation-2026-09-10.json), which contains no API credentials.

## Engineering checks

| Scope | Result | Evidence |
| --- | --- | --- |
| Structured messages, deep copy of tool arguments | pass | `TestCheckpointDeepCopyAndBoundaries` |
| Summary / compaction requests do not overwrite, complete reply and terminal state published, half-finished reply excluded | pass | `TestCheckpointDeepCopyAndBoundaries`, `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Actual model pool member identity | pass | `TestSnapshotExcludesPartialStreamAndSelectsPoolMember` |
| Tool pairing, 20-pair replay, budget trimming and over-limit error | pass | `TestBuildRequestCompactionToolPairingAndBudget` |
| Main/side-question parallelism, bidirectional cancellation isolation | pass | blocking Provider, `TestMainSideConcurrencyAndIndependentCancellation` |
| No tool execution, streaming / non-streaming, usage already present on failure | pass | `TestServiceNoToolsAndUsageOnFailure` |
| Real norma ChatAgent + local Read tool, main transcript / activity isolation | pass | `TestSideActualChatCheckpointToolResultAndTranscriptIsolation`, streaming and non-streaming subcases |
| Persistence, pagination, idempotency, restart preserving partial answer | pass | `TestSideHistoryIdempotencyPagingAndRecovery` |
| Clear vs late-write race, parent resource deletion, version comparison | pass | `TestSideClearLateWritersAndDeletedParent` |
| MainAgent / Worker archive and restore, v1/v2/v3 | pass | `TestSideTaskArchiveVersions` |
| Three parent interfaces, authentication, resource ownership, Worker logical delete | pass | `TestSideHTTPGlobalLimitTaskWorkerAndDeletion`, `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Side question allowed on a busy main session, independent SSE reconnect / disconnect, cancel, clear | pass | `TestSideHTTPBusyIsolationClearAndReconnect` |
| 1 per parent session / 4 global concurrency | pass | two `TestSideHTTP…` cases |
| Snapshot persisted before submission, resumed question after restart, old session cannot forge a snapshot | pass | `TestSideCheckpointPersistsBeforeAdmissionAndRestart` |
| Refuse to continue after the cached configuration is deleted or the model changes | pass | `TestSideRejectsDeletedOrChangedCachedProfile` |
| Cancel before archiving and wait for the final answer and usage to persist | pass | `TestSideTaskDrainPersistsBeforeArchive` |
| Usage recorded only once and attributed to the side question when the streaming consumer cancels early | pass | `TestSideUsageRecordedOnceOnConsumerCancellation` |
| An auto-restored Worker / deadline runtime context after restart continues publishing new snapshots | pass | `TestSideRestoredWorkerRuntimePublishesNewCheckpoint` |
| Race check of the relevant packages | pass | the commands below |
| TypeScript and production build | pass | `npx tsc --noEmit`, `npm run build` |
| Biome on new frontend modules | pass | `biome check`, 3 new modules |

After configuring `ARTEX_PG_DSN` in a separate disposable database, the automated checks can be reproduced (do not point it at a production database):

```sh
go test -race ./agent ./db ./server ./sidequestion ./llmrec ./llmpool \
  -run 'Test(Side|Checkpoint|Snapshot|BuildRequest|Service|MainSide|CaptureRun|TaskArchive|CompleteForwards|StopIntent|CancelIntent)' -count=1
cd web
npx tsc --noEmit
npx biome check src/lib/side-questions.ts src/hooks/use-side-questions.ts src/components/side-question-workspace.tsx
npm run build
```

The full Go regression is not all green: the `server` package has two pre-existing tests that fail during temp-directory cleanup, both reporting `TempDir RemoveAll … directory not empty`:

- `TestInheritedActivityDetailAndRelationDeletion`
- `TestTaskMetadataPatchReturnsRenameAndPin`

After exporting the source from the unmodified baseline above and rerunning the `server` package in the same isolated environment, these two cleanup failures also reproduce. The baseline run additionally showed a target-node-count assertion failure in `TestCoreTaskLifecyclePG`; the final modified `server` regression does not have that assertion failure. The other packages pass, and this change's side-question cases and race checks pass. Baseline issues were not marked as passing for this acceptance, and no existing assertion was modified to hide a problem.

The Next.js build emits the existing multi-lockfile / workspace-root inference warnings; the build completes and all pages are generated successfully.

## Browser checks

Using the Codex In-app Browser, connected to a standalone local Go service and the Next.js dev server. The following manual-automation operations were performed on the desktop and a 390 × 844 narrow screen, checking screenshots and browser logs:

- Typed `/btw` during an ordinary chat run; the main content and the side question are displayed at the same time; the desktop sidebar works.
- Consecutive follow-ups; the generated portion is retained after the side question stops; the main flow continues.
- The request continues when the panel is closed, and the completed answer is restored on reopening; after refreshing the page, an empty `/btw` restores the history.
- The narrow-screen Drawer's input, buttons, history, and close operation work with no horizontal overflow.
- Clear uses a confirmation dialog; after clearing, the history disappears while the main transcript and snapshot are retained.
- The task MainAgent and the two Workers were each asked questions and switched between; the Agent labels and history did not cross over.
- A blocking local model fixture kept the Worker running; after submitting `/btw` from the Worker main input box and stopping the side question, the Worker still shows live running and its own pause button, and the side question saves the partial answer.
- The browser error / warning log is empty.

A controllable fixture is used to precisely verify concurrency timing, without depending on the output speed of a real model. During debugging, two Worker runtime checks did not form a valid concurrency window (the task had already ended / the answer ended early); after fixing the fixture it was redone and passed; these initial operations are not recorded as valid passes.

## Real model conversations

`grok-4.6` was probed first, via the OpenAI-compatible interface `http://127.0.0.1:12580/tingly/openai`. The probe returned HTTP 200 with the model name `grok-4.6` and `READY`, taking 2.82 seconds. The preferred option was available, so the Tingly `glm` or Zhipu `glm-5.3` fallback chain was not enabled; those two fallback services were not validated this time.

| Scenario | Actual result |
| --- | --- |
| Asked about assets, goals, and markers during a main session run | Returned `redhaze.top`, the homepage read and goal summary, and `BTW-REAL-0910`; the side question completed, 16.97 seconds |
| Asked about the tool basis after the main session finished reading the homepage | Correctly cited WebFetch 200, the curl redirects 301 → 302 → 200, and the page title; 7.24 seconds |
| Side question asked Bash to create a test file | Refused to execute, the target file was not created; 7.74 seconds |
| A side question after completion does not change the main context | The main transcript SHA-256 stays consistent with the main activity log; the side-question tool execution count is 0 |
| Follow-up question after truly stopping / restarting the Go service | The previous 3 side-question history items are retained, answering assets, markers, and the title directly from the persisted snapshot, without rerunning the main Agent |
| New session using the Grok non-streaming configuration | Correctly answered the assets and `ATOMIC-0910`; returned and saved usage: input 11734, output 138, cache_read 11520 |

The main session of the asset case used WebFetch and Bash/curl to read the public homepage; the landing page is `https://id.redhaze.top/home`, with the title "RedHaze Group - Global Integrated Group Portal". Bash staged the response in a local test file; nothing was written to the remote. This fact is verified separately from "the side question executed no tools".

Main transcript checksum: `e7e61f135a4a120954b539f357e8c4205d7d5cd7460dcaf3dc0fd066463e1d00`.

**Usage limitation:** Tingly's Grok streaming response returned no usage. A separate direct request with `stream_options.include_usage=true` was used to verify: HTTP 200, 12 data frames, 0 usage frames. So the 0 in the streaming test means the endpoint provided no usage and cannot be interpreted as no billing. Non-streaming usage, as well as the fixture's failure / cancel usage, are saved correctly.

## Qwen review

The review model `qwen-flash`, via the OpenAI-compatible interface `https://dashscope.aliyuncs.com/compatible-mode/v1`, HTTP 200. It was given the first three real side-question conversations, the main-session tool basis, and the engineering assertions; it returned `verdict: accept`, `concerns: []`, judging that the answers were consistent with the asset, marker, and page-read evidence, and that the side question's tool refusal met the constraints. Review usage: prompt 6625, completion 312, total 6937.

This Qwen review did not cover the service restart and non-streaming tests added later. Qwen's generalization of "no writes" was too broad: the main-session curl did create a local response temp file, as clearly recorded above. Concurrency, zero tool execution, and transcript isolation are judged by the engineering assertions; the model review only assists in evaluating answer quality.
