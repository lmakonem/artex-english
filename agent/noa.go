package agent

import (
	"log"
	"path/filepath"

	"github.com/Autumn-27/norma/agentcore"
	"github.com/Autumn-27/norma/noaadapter"
)

// noaWarn returns a diagnostics sink tagging non-fatal noa messages with the
// session, routed through the package logger (agents have no per-instance one).
func noaWarn(session string) func(string) {
	return func(msg string) { log.Printf("[noa] %s: %s", session, msg) }
}

// noa is the "model-driven context compression" mechanism introduced in norma
// v0.4.0, exposed as a platform experimental feature that the user toggles in
// system settings. It is mutually exclusive with the built-in compaction:
// noaadapter.Enable is the only entry point, installing in one shot the context
// takeover handler (Compactor), the Compress tool and three resident prompt
// segments; not calling Enable means it is off (the built-in compaction works as
// usual). The toggle is resolved by the noaEnabledFn each agent injects, read once
// per run, so switching only affects runs started afterward and needs no agent rebuild.

// enableNoa wires noa into opts when the resolver reports it enabled. archiveRoot is
// the persistence base directory for the compressed originals (taken from the global
// workDir, so every agent lands uniformly under <workDir>/noa rather than being
// scattered across task/intent directories); sessionID names the archive
// subdirectory beneath it (globally unique, so there is no collision within the same base directory).
//
// noa is an experimental feature: a wiring failure must not interrupt a real task. On
// error, report via onWarn and fall back to the built-in compression. On successful
// enablement, clear opts.Compaction to avoid agentcore warning that "two context managers are set at once".
func enableNoa(opts *agentcore.Options, enabled func() bool, archiveRoot, sessionID string, onWarn func(string)) {
	if enabled == nil || !enabled() {
		return
	}
	if opts.OnWarn == nil {
		opts.OnWarn = onWarn
	}
	if err := noaadapter.Enable(opts, noaadapter.Options{
		ArchiveBaseDir: filepath.Join(archiveRoot, "noa"),
		SessionID:      sessionID,
		OnWarn:         onWarn,
	}); err != nil {
		if onWarn != nil {
			onWarn("noa compression failed to enable, falling back to built-in compression: " + err.Error())
		}
		return
	}
	// Compactor overrides Compaction, but agentcore warns every time both coexist; clear it explicitly.
	opts.Compaction = nil
}
