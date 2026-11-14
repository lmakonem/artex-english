package agent

import (
	"strings"

	"github.com/Autumn-27/norma/harness"
)

// Wrap-up / settlement prompt: when an agent is terminated due to [step exhaustion
// (MaxTurns)] or [timeout (run_seconds/MaxDuration)], the SDK's settlement phase injects
// this prompt so the agent first persists content it has identified but not yet written
// back, then outputs a one-sentence summary, to avoid an abrupt ending.
//
// Each agent's wrap-up prompt can be overridden as needed in the backend (stored in
// agents.wrapup_prompt); if left empty, the built-in default here is used. Only the
// [prompt body] is editable; which tools are disabled and how many rounds the wrap-up
// itself gets are fixed code policy.

// WrapupOverride, if set, returns the stored wrap-up prompt for an agent key and
// whether a non-empty one exists. Wired by the server to the agents table (like
// PromptOverride for system prompts). nil / empty → the built-in default is used.
var WrapupOverride func(agentKey string) (string, bool)

// WrapupMaxTurnsOverride, if set, returns the admin-configured turn budget for the
// wrap-up phase of an agent and whether a positive one exists. Wired to the agents
// table. nil / ≤0 → the built-in per-agent default (wrapupTurnDefaults) is used.
var WrapupMaxTurnsOverride func(agentKey string) (int, bool)

// Built-in default wrap-up prompts, indexed by agent key. worker reuses the historically
// hardcoded settleWrapUpPrompt (defined in worker.go); planner/mainagent each have their
// own; anything unmatched (custom agents) uses the generic fallback.
var wrapupDefaults = map[string]string{
	"worker":    settleWrapUpPrompt,
	"planner":   plannerWrapUpDefault,
	"mainagent": mainAgentWrapUpDefault,
}

// wrapupTurnDefaults: built-in default round budget for each agent's [own] wrap-up phase
// (can be overridden by a backend value >0). Each gets 10 rounds, ensuring the wrap-up
// phase has enough steps to persist. Anything unmatched uses genericWrapupTurns.
var wrapupTurnDefaults = map[string]int{
	"worker":    10,
	"planner":   10,
	"mainagent": 10,
}

const genericWrapupTurns = 10

const plannerWrapUpDefault = "Your planning-round step budget is about to run out -- note that this is only the end of [this round]; the system will wake you again as the situation changes to keep planning, this is not task termination, and you do not need to wrap up the entire plan here. Land the conclusions you have already thought through this round so the round is not wasted, but also [do not force intents just to wrap up] (0 intents this round is still a perfectly normal result): (1) if you have determined exploration directions that [should be dispatched right now], submit them in one batched add_intent call (do not hold back ones you have already thought out); (2) for goals proven met by some finding/fact, call prove_goal to mark them met (do not miss any); (3) if you have identified a serial exploitation chain that needs to be done step by step, note it with TodoWrite so the next wake-up can continue dispatching. When done, end this round directly; no summary text is needed."

const mainAgentWrapUpDefault = "Your step budget is about to run out and this interaction is ending. Do not start any new exploration/operations. In **a single plain-text sentence on its own**, summarize for the user the current progress, the key conclusions, and the suggested next step."

const genericWrapUpDefault = "You are about to be terminated because the budget is exhausted. First write back results you have completed but not yet persisted, then in **a single plain-text sentence on its own** summarize what you did and which key conclusions you reached (this sentence will be shown as the result of this run)."

// WrapupDefault returns the built-in default wrap-up prompt for an agent key - 
// used by the admin UI as the "restore default" value and empty-field placeholder.
func WrapupDefault(agentKey string) string {
	if d, ok := wrapupDefaults[agentKey]; ok {
		return d
	}
	return genericWrapUpDefault
}

// WrapupTurnsDefault returns the built-in wrap-up turn budget for an agent key - 
// used by the admin UI as the "0 = default N" hint.
func WrapupTurnsDefault(agentKey string) int {
	if n, ok := wrapupTurnDefaults[agentKey]; ok {
		return n
	}
	return genericWrapupTurns
}

// resolveWrapup returns the effective wrap-up prompt: the DB override (if set and
// non-empty) over the built-in default.
func resolveWrapup(agentKey string) string {
	if WrapupOverride != nil {
		if t, ok := WrapupOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return WrapupDefault(agentKey)
}

// resolveWrapupTurns returns the effective wrap-up turn budget: a positive DB
// override over the built-in per-agent default.
func resolveWrapupTurns(agentKey string) int {
	if WrapupMaxTurnsOverride != nil {
		if v, ok := WrapupMaxTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return WrapupTurnsDefault(agentKey)
}

// wrapupSettlement builds the settlement config for an agent's run. Prompt and the
// turn budget are admin-editable per agent; disabled tools are code-owned policy so
// a user can't edit away the "stop probing" guardrail. Resolved fresh each run
// (reads DB live), so edits apply on the next run without a restart.
func wrapupSettlement(agentKey string, disabledTools []string) *harness.Settlement {
	return &harness.Settlement{
		Prompt:        resolveWrapup(agentKey),
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
}

// ---------- Task-level timeout wrap-up prompts (see docs/task-level-timeout-and-wrapup-design.md) ----------
//
// These are a [separate set] from the per-run wrap-up prompts: per-run means "your budget
// for this one run is used up"; a task timeout means "the whole task has reached its deadline
// and is about to end". The semantics are often opposite (especially for planner: per-run says
// "do not stop, keep planning", task timeout says "stop planning at the deadline and make a final
// judgment"). Configured only for worker/planner.

// WrapupTaskTimeoutOverride / ...TurnsOverride: DB overrides for the task-timeout wrap-up
// prompt and round count (wired to agents.task_timeout_wrapup_prompt / _max_turns, worker/planner only).
var (
	WrapupTaskTimeoutOverride      func(agentKey string) (string, bool)
	WrapupTaskTimeoutTurnsOverride func(agentKey string) (int, bool)
)

var taskTimeoutWrapupDefaults = map[string]string{
	"worker":  workerTaskTimeoutDefault,
	"planner": plannerTaskTimeoutDefault,
}

const workerTaskTimeoutDefault = "**The whole task has reached its timeout limit and is about to end** (not your budget for this run; the entire exploration has reached its deadline). This is the last chance: (1) persist [all] content you have identified but not yet written back -- new assets with insert_assets, exploration conclusions/facts with record_fact, confirmed vulnerabilities with report_finding; (2) do not start any new commands/probes; (3) **finally, in a single plain-text sentence on its own**, summarize your key conclusions on this intent."

const plannerTaskTimeoutDefault = "**The whole task has reached its timeout limit and is about to end** (not this round; the entire task is terminating). Based on [all] current facts and findings, make a final goal judgment: for goals proven met by evidence, call prove_goal to mark them met (do not miss any). **Do not generate any new intents** (dispatching intents now would no longer be executed). Once judged, wrap up; no summary text is needed."

// TaskTimeoutWrapupDefault returns an agent's built-in default task-timeout wrap-up prompt (for the backend placeholder / restore-default).
func TaskTimeoutWrapupDefault(agentKey string) string {
	return taskTimeoutWrapupDefaults[agentKey] // returns an empty string when not configured (mainagent/chat)
}

// resolveTaskTimeoutWrapup: DB override (non-empty) > built-in default. An empty string means
// this agent has no task-timeout prompt (not worker/planner), in which case the caller should
// fall back to the per-run prompt.
func resolveTaskTimeoutWrapup(agentKey string) string {
	if WrapupTaskTimeoutOverride != nil {
		if t, ok := WrapupTaskTimeoutOverride(agentKey); ok && strings.TrimSpace(t) != "" {
			return t
		}
	}
	return TaskTimeoutWrapupDefault(agentKey)
}

func resolveTaskTimeoutTurns(agentKey string) int {
	if WrapupTaskTimeoutTurnsOverride != nil {
		if v, ok := WrapupTaskTimeoutTurnsOverride(agentKey); ok && v > 0 {
			return v
		}
	}
	return resolveWrapupTurns(agentKey) // defaults to reusing the per-run round count
}

// wrapupSettlementForTask builds settlement for a worker/planner run that is aware
// of the task deadline. See §5 of the design doc:
//   - clamped=true  -> this run is squeezed by the task deadline: wrap-up due to Timeout = task
//     reached its deadline -> task-timeout prompt; wrap-up due to MaxTurns = steps ran out first
//     within the squeeze window while the task still has a few minutes left -> fall back to the per-run prompt.
//   - clamped=false -> the task deadline is still far off: both reasons use the per-run prompt (i.e. degenerates to wrapupSettlement).
//
// The PromptByReason handed to the harness picks by the [actual] reason at wrap-up time, so there is no build-time mismatch.
func wrapupSettlementForTask(agentKey string, disabledTools []string, clamped bool) *harness.Settlement {
	perRun := resolveWrapup(agentKey)
	st := &harness.Settlement{
		Prompt:        perRun, // fallback (also the value both reasons take when not clamped)
		DisabledTools: disabledTools,
		MaxTurns:      resolveWrapupTurns(agentKey),
	}
	if clamped {
		if tt := resolveTaskTimeoutWrapup(agentKey); tt != "" {
			st.PromptByReason = map[harness.TerminalReason]string{
				harness.ReasonTimeout:  tt,     // task reached its deadline
				harness.ReasonMaxTurns: perRun, // steps ran out first while the task still has time left
			}
			st.MaxTurns = resolveTaskTimeoutTurns(agentKey)
		}
	}
	return st
}
