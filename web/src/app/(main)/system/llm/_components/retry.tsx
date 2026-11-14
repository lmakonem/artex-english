"use client";

// Shared component for LLM retry config: the "attempts + interval" of each of the five retry layers.
//
// The five layers, inner to outer: connect (SDK) → empty response (SDK) → same-provider safe window → rotation circuit breaker → intent rerun.
// The first three follow the endpoint, so each model profile can override the global default; the last two are process-level, with only one global copy.
//
// All inputs follow the same "empty = not configured" semantics, consistent with the backend db.RetryRule:
//   attempts   empty/0 = use the built-in default | -1 = disable this retry layer | >0 = use this count
//   interval   empty/0 = use this layer's original exponential backoff | >0 = use this fixed interval in milliseconds instead

import * as React from "react";

import { Loader2Icon, SaveIcon } from "lucide-react";
import { toast } from "sonner";

import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { api } from "@/lib/api";
import type { LLMRetryOverride, LLMRetryPolicy, LLMRetryRule } from "@/lib/types";

export const ZERO_RULE: LLMRetryRule = { attempts: 0, interval_ms: 0 };
export const ZERO_OVERRIDE: LLMRetryOverride = {
  connect: ZERO_RULE,
  empty: ZERO_RULE,
  stream: ZERO_RULE,
};
const ZERO_POLICY: LLMRetryPolicy = {
  ...ZERO_OVERRIDE,
  breaker: ZERO_RULE,
  intent: ZERO_RULE,
};

type LayerMeta = {
  title: string;
  /** where this retry happens and who runs it */
  where: string;
  /** what kind of error reaches this layer -- down to the status code, don't make people guess */
  trigger: string;
  /** errors that look similar but do NOT go through this layer, so people don't fill it in, see no effect, and think it's a bug */
  skips?: string;
  desc: string;
  attemptsLabel: string;
  /** default value when attempts is empty, used as the placeholder */
  defAttempts: number;
  /** default policy when interval is empty, used as the placeholder */
  defInterval: string;
  /** what filling attempts with -1 means */
  offHint: string;
};

export const RETRY_LAYERS = {
  connect: {
    title: "Connect retry",
    where: "SDK · before receiving 200",
    trigger:
      "Can't connect or hasn't received 200 yet: network-layer errors like connection reset / read-write timeout / DNS failure, and HTTP 408, 429, 500, 502, 503, 504.",
    skips: "All other status codes (400 / 401 / 403 / 404 / 413 / 422 etc.) are deterministic rejections; resending fails the same way, so it's thrown up directly.",
    desc: "Resends the same request as-is. Once the stream has started (200 already received), a mid-stream disconnect is no longer this layer's concern.",
    attemptsLabel: "Retry attempts",
    defAttempts: 3,
    defInterval: "0.5s->1s->2s exponential (capped at 8s)",
    offHint: "-1 = no retries at all; throw up immediately on failure",
  },
  empty: {
    title: "Empty response retry",
    where: "SDK · openai format only",
    trigger:
      "HTTP 200, finish_reason is a normal stop, but the whole response has not a single content block -- gateway empty frames, dropped thinking-field frames, and sampling hiccups all look like this.",
    skips: "Doesn't count when there's no content due to max_tokens truncation (that's fixed by raising the output limit; resending just hits it again).",
    desc: "Resends the entire prompt, so it's relatively expensive on long contexts; don't set the count high.",
    attemptsLabel: "Retry attempts",
    defAttempts: 2,
    defInterval: "0.5s->1s->2s exponential (capped at 8s)",
    offHint: "-1 = hand back the empty response as-is",
  },
  stream: {
    title: "Same-provider safe-window retry",
    where: "this project · before any output is delivered",
    trigger:
      "Problems that arise only after the stream is established (200 received): a mid-stream disconnect, the provider being overloaded, in-stream 429 / 5xx error events -- and not a single token has been handed to the caller yet.",
    skips:
      "Quota exhausted (402 / insufficient_quota, handed to rotation to switch profiles), context too long (413 / context length, handed to compression), and 400 / 401 / 403 / 404 / 422 deterministic rejections are all not retried.",
    desc: "Replays the same request on the same profile. Since no output has been delivered yet, the replay won't duplicate model output or tool execution.",
    attemptsLabel: "Retry attempts",
    defAttempts: 2,
    defInterval: "0.5s->1s exponential (capped at 4s)",
    offHint: "-1 = hand a broken stream straight to the outer intent rerun",
  },
  breaker: {
    title: "Rotation circuit breaker",
    where: "this project · process-level, one global copy",
    trigger:
      "Trips when transient failures (429, 5xx, network errors) accumulate consecutively to the threshold; deterministic failures like insufficient balance (402), invalid key (401 / 403), and nonexistent model (404) ignore the threshold and trip on the first occurrence.",
    skips: "A single success resets the count, so an occasionally flaky profile won't slowly accumulate its way to tripping.",
    desc: "After tripping it enters a cooldown, and during the cooldown rotation skips this profile entirely. State is persisted, so it survives restarts.",
    attemptsLabel: "Consecutive failures before tripping",
    defAttempts: 3,
    defInterval: "1min->5min->30min gradient",
    offHint: "-1 = transient failures never trip (deterministic failures still trip)",
  },
  intent: {
    title: "Intent rerun",
    where: "this project · process-level, one global copy",
    trigger:
      "When none of the earlier layers caught it: the worker ends in model_error -- all inner retries exhausted, or the stream broke after it had already started delivering output (replay isn't safe then, so the whole thing has to rerun).",
    skips: "Quota exhaustion is already handled by rotation switching profiles, not reran here; when the task is paused / terminated / entering wrap-up, it yields immediately and doesn't consume backoff time.",
    desc: "Reruns the whole intent from the start. It's the outermost layer, so one rerun means the counts of the inner layers get multiplied again.",
    attemptsLabel: "Rerun attempts",
    defAttempts: 2,
    defInterval: "fixed 3s",
    offHint: "-1 = no rerun; the intent is marked blocked directly",
  },
} satisfies Record<string, LayerMeta>;

type LayerKey = keyof typeof RETRY_LAYERS;

/** A human-readable form of milliseconds, used only for the echo next to the input box, to save counting zeros. */
function humanMs(ms: number) {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60_000) return `${Number((ms / 1000).toFixed(2))}s`;
  return `${Number((ms / 60_000).toFixed(2))}min`;
}

/** Controlled numeric input: empty string <-> 0, with intermediate states ("-", "1e") kept locally as-is without bothering the parent. */
function NumField({
  id,
  value,
  onChange,
  placeholder,
  min,
}: {
  id: string;
  value: number;
  onChange: (n: number) => void;
  placeholder: string;
  min: number;
}) {
  const [text, setText] = React.useState(value === 0 ? "" : String(value));
  // Keep up when the parent swaps in a whole new set of values (policy loaded, profile switched); typing yourself won't reach here, because by then value already equals the parsed result of the local text.
  React.useEffect(() => {
    const incoming = value === 0 ? "" : String(value);
    setText((cur) => (Number(cur || 0) === value ? cur : incoming));
  }, [value]);
  return (
    <Input
      id={id}
      type="number"
      min={min}
      className="w-28 shrink-0"
      value={text}
      placeholder={placeholder}
      onChange={(e) => {
        setText(e.target.value);
        const n = Number(e.target.value);
        onChange(e.target.value.trim() === "" || !Number.isFinite(n) ? 0 : Math.trunc(n));
      }}
    />
  );
}

/** The two knobs for one retry layer. idPrefix keeps the label's htmlFor unique when it appears multiple times on the same page. */
export function RetryRuleFields({
  layer,
  idPrefix,
  value,
  onChange,
  compact,
}: {
  layer: LayerKey;
  idPrefix: string;
  value: LLMRetryRule;
  onChange: (r: LLMRetryRule) => void;
  /** true = the compact version in the config drawer: drops the expanded notes and keeps only the "what errors reach this layer" line */
  compact?: boolean;
}) {
  const meta = RETRY_LAYERS[layer];
  const human = humanMs(value.interval_ms);
  return (
    <div className={compact ? "grid gap-2" : "grid gap-3 rounded-lg border p-3"}>
      <div className="grid gap-0.5">
        <div className="flex flex-wrap items-baseline gap-2">
          <Label className="text-sm">{meta.title}</Label>
          <span className="text-muted-foreground text-xs">{meta.where}</span>
        </div>
        {/* Which errors reach this layer, down to the status code -- if you set the knobs but see no effect, it's usually because the error doesn't land on this layer at all. */}
        <p className="text-muted-foreground text-xs">
          <span className="font-medium text-foreground">Triggers</span>: {meta.trigger}
        </p>
        {!compact && meta.skips && (
          <p className="text-muted-foreground text-xs">
            <span className="font-medium text-foreground">Skips this layer</span>: {meta.skips}
          </p>
        )}
        {!compact && <p className="text-muted-foreground text-xs">{meta.desc}</p>}
      </div>
      <div className="flex flex-wrap items-center gap-x-4 gap-y-2">
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-n`} className="text-muted-foreground text-xs">
            {meta.attemptsLabel}
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-n`}
            min={-1}
            value={value.attempts}
            placeholder={`Default ${meta.defAttempts}`}
            onChange={(n) => onChange({ ...value, attempts: n })}
          />
        </div>
        <div className="flex items-center gap-2">
          <Label htmlFor={`${idPrefix}-${layer}-ms`} className="text-muted-foreground text-xs">
            Interval ms
          </Label>
          <NumField
            id={`${idPrefix}-${layer}-ms`}
            min={0}
            value={value.interval_ms}
            placeholder="Default backoff"
            onChange={(n) => onChange({ ...value, interval_ms: n })}
          />
          <span className="text-muted-foreground text-xs">{human ? `Fixed ${human}` : meta.defInterval}</span>
        </div>
      </div>
      {!compact && <p className="text-muted-foreground text-xs">Empty = use default; {meta.offHint}.</p>}
    </div>
  );
}

/** The three-layer overrides in the model profile drawer (the three that follow the endpoint). */
export function ProfileRetryFields({
  value,
  onChange,
}: {
  value: LLMRetryOverride;
  onChange: (o: LLMRetryOverride) => void;
}) {
  return (
    <div className="grid gap-3 rounded-lg border p-3">
      <div className="grid gap-0.5">
        <Label className="text-sm">Retry overrides</Label>
        <p className="text-muted-foreground text-xs">
          Applies only to this profile, overriding the global defaults under "Retry & backoff". Leave a box empty = follow global; set attempts to -1 = turn off that retry layer;
          set an interval and it replaces exponential backoff with that fixed interval. The circuit breaker and intent rerun are process-level and can only be tuned on the global page.
        </p>
      </div>
      {(["connect", "empty", "stream"] as const).map((k) => (
        <div key={k} className="border-t pt-3 first:border-t-0 first:pt-0">
          <RetryRuleFields
            compact
            layer={k}
            idPrefix="pf"
            value={value[k]}
            onChange={(r) => onChange({ ...value, [k]: r })}
          />
        </div>
      ))}
    </div>
  );
}

/** The "Retry & backoff" tab: the global defaults for all five layers. */
export function RetryPolicyPanel() {
  const [policy, setPolicy] = React.useState<LLMRetryPolicy>(ZERO_POLICY);
  const [loading, setLoading] = React.useState(true);
  const [saving, setSaving] = React.useState(false);

  const load = React.useCallback(async () => {
    setLoading(true);
    try {
      const p = await api.llmRetryPolicy();
      setPolicy({ ...ZERO_POLICY, ...p });
    } catch (e) {
      toast.error(`Failed to load retry policy: ${(e as Error).message}`);
    } finally {
      setLoading(false);
    }
  }, []);

  React.useEffect(() => {
    void load();
  }, [load]);

  async function save() {
    if (saving) return;
    setSaving(true);
    try {
      // The backend clamps out-of-range values back into range and returns them; refresh directly from the returned values, so what you see is what's stored.
      const saved = await api.saveLLMRetryPolicy(policy);
      setPolicy({ ...ZERO_POLICY, ...saved });
      toast.success("Saved, effective immediately (the call currently in flight still uses the old parameters)");
    } catch (e) {
      toast.error(`Save failed: ${(e as Error).message}`);
    } finally {
      setSaving(false);
    }
  }

  const set = (k: LayerKey) => (r: LLMRetryRule) => setPolicy((p) => ({ ...p, [k]: r }));

  if (loading) {
    return (
      <div className="flex items-center gap-2 rounded-lg border border-dashed p-10 text-muted-foreground text-sm">
        <Loader2Icon className="size-4 animate-spin" /> Loading retry policy…
      </div>
    );
  }

  return (
    <div className="grid gap-4">
      <div className="rounded-lg border bg-muted/30 p-3 text-muted-foreground text-xs leading-relaxed">
        A failed model call passes through five retry layers in order, inner to outer:
        <span className="text-foreground"> connect → empty response → same-provider safe window → rotation circuit breaker → intent rerun</span>
        . The outer layer only comes into play once the inner is exhausted, so the counts
        <span className="text-foreground"> multiply</span>
        -- max out every layer and a single blip can burn dozens of requests.
        Leaving everything empty is the current default, exactly matching the behavior without this page. The first three layers can be overridden individually in each model profile.
      </div>

      <div className="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
        {(Object.keys(RETRY_LAYERS) as LayerKey[]).map((k) => (
          <RetryRuleFields key={k} layer={k} idPrefix="gl" value={policy[k]} onChange={set(k)} />
        ))}
      </div>

      <div className="flex gap-2">
        <Button onClick={save} disabled={saving}>
          {saving ? <Loader2Icon className="animate-spin" /> : <SaveIcon />}
          Save
        </Button>
        <Button variant="outline" onClick={() => setPolicy(ZERO_POLICY)} disabled={saving}>
          Reset all to default
        </Button>
      </div>
    </div>
  );
}
