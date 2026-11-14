package agent

import (
	"context"
	"errors"
	"fmt"
)

// AbortCause names why an agent run's context was cancelled. Every cancellation
// site should attach one so the activity trace can report the real initiator.
type AbortCause struct {
	Code  string
	Short string
	Text  string
}

func (c *AbortCause) Error() string { return c.Text }

func cause(code, short, text string) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: text}
}

// Causef builds a cause that includes runtime-specific detail.
func Causef(code, short, format string, args ...any) *AbortCause {
	return &AbortCause{Code: code, Short: short, Text: fmt.Sprintf(format, args...)}
}

var (
	// Task-level execution context.
	AbortPausedByUser = cause("paused_by_user", "the user paused the task",
		"The user paused the task via the task control API (POST /api/tasks/{id}/control, action=pause). This Planner/Worker run was actively canceled; the running intent is returned to frontier(open), and after the task resumes it is re-claimed and executed from the start")
	AbortPausedByOrchestrator = cause("paused_by_orchestrator", "the orchestrator Agent paused the task",
		"The orchestrator Agent called the pause_task tool to pause this task. This Planner/Worker run was actively canceled; the running intent is returned to frontier(open) and re-executed after resuming")
	AbortTaskDeleted = cause("task_deleted", "the task was deleted",
		"The task is being deleted (DELETE /api/tasks/{id}); the deletion barrier canceled the task's running Planner, Worker and main Agent; this run's result will no longer be used")
	AbortPausedOnReload = cause("paused_on_reload", "backend restored the paused state",
		"On startup the backend restored the task's paused state from the state persisted in the database. This run was canceled; normally no Agent is running during the restore phase")
	AbortGoalMet = cause("goal_met", "the planner judged the task goal met",
		"The planner judged the task goal met and set the task to done, then canceled the still-running Workers; these intents are marked stopped rather than failed")
	AbortSettleDrainTimeout = cause("settle_drain_timeout", "timeout wrap-up wait exhausted",
		"After the task reached timeout it waited for the running Workers to wrap up gracefully, but the 90-second drain grace period was still not enough, so execution was hard-canceled; the intent is marked exhausted, and facts and assets already written during wrap-up are retained")

	// Per-work context.
	AbortKilledByPlanner = cause("killed_by_planner", "the planner terminated this intent",
		"The planner called kill_work to actively terminate this intent, usually meaning the direction went off track or there was no further value; the intent is marked stopped and is not re-claimed automatically")
	AbortWorkPausedByUser = cause("work_paused_by_user", "the user paused this Worker intent",
		"The user paused the running Worker. This call was canceled and the intent moved to paused; all already-recorded intents, facts, vulnerabilities and activity records are retained, and after resuming it is executed again from the start")
	AbortWorkCancelledByUser = cause("work_cancelled_by_user", "the user deleted this Worker intent",
		"The user deleted the running Worker. This call was canceled; after the Worker exits the write region, the server handles the intent according to the deletion mode the user chose -- a soft delete only marks it deleted and keeps all output, while a hard delete cascades to remove the intent and the downstream nodes supported only by it")
	AbortWorkFinished = cause("work_finished", "Worker finished normally",
		"The Worker finished normally, and the engine releases its context resources in detachWork. This is not a run interruption; if it appears in an interruption message, the cancellation and the teardown event raced")
	AbortPausedRaceGuard = cause("paused_race_guard", "new run refused while paused",
		"While the task is paused, the engine refuses to issue a new execution context, to prevent a race between claim and pause from letting a Worker start anyway; the claimed intent is returned to frontier")

	// Main Agent and standalone conversation contexts.
	AbortChatStoppedByUser = cause("chat_stopped_by_user", "the user stopped this conversation turn",
		"The user clicked stop, actively aborting this main Agent or conversation Agent run. The activity records already produced are retained, and you can send the next message")
	AbortChatPausedWithTask = cause("chat_paused_with_task", "task pause aborted main Agent chat",
		"When the user paused the task, the running main Agent conversation was also canceled in sync. The activity records already produced are retained; after the task resumes this turn's message is not replayed automatically")
	AbortChatTurnFinished = cause("chat_turn_finished", "conversation turn finished normally",
		"This conversation turn finished normally, and the server is releasing this turn's context resources. This is not a run interruption; if it appears in an interruption message, the cancellation and the teardown event raced")

	// Process-level and per-run hard backstop.
	AbortShutdown = cause("shutdown", "the backend process is shutting down",
		"The backend process received SIGINT or SIGTERM and is restarting, updating or shutting down. All running Agents are canceled; after restart, leftover running intents are reset to open and re-executed")
	AbortRunHardTimeout = cause("run_hard_timeout", "single-run hard-timeout backstop fired",
		"A single run exceeded the soft wall-clock budget plus the extra grace period, meaning a model request or some tool did not return for a long time, so the normal turn-boundary wrap-up could not run. Focus on the last tool call that did not return before the interruption")
)

// AbortReason resolves the named cause attached to a cancelled run context.
func AbortReason(ctx context.Context) (code, short, text string, ok bool) {
	c := context.Cause(ctx)
	if c == nil {
		return "", "", "", false
	}
	var ac *AbortCause
	if errors.As(c, &ac) {
		return ac.Code, ac.Short, ac.Text, true
	}
	switch {
	case errors.Is(c, context.DeadlineExceeded):
		return "deadline_exceeded", "upstream context reached deadline",
			"The upstream context reached its deadline, but the setter did not attach a named cause via WithTimeoutCause: " + c.Error(), true
	case errors.Is(c, context.Canceled):
		return "canceled_no_cause", "the canceler attached no named cause",
			"The upstream context was canceled, but the canceler did not attach a named cause via context.WithCancelCause; please register a cause in agent/cancelcause.go and wire it into that cancellation point", true
	default:
		return "other", firstLine(c.Error(), 80), c.Error(), true
	}
}
