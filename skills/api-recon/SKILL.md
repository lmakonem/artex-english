---
name: api-recon
description: Invoke this skill when collecting a website's API interfaces.
---

# API Recon (frontend interface recon)

Under an **authorized** premise, discover as completely as possible: the **backend APIs** (paths, methods, parameters, response bodies), the **frontend routes**, and the **UI feature triggers** (tabs, modals, table actions, etc.).

---

## Boundaries and prohibitions (Agent must read; violating is out of scope)

This skill performs **API / parameter-surface recon only**; it is not the vulnerability-hunting or exploitation stage.

### Task boundaries

| Scope | Allowed | Prohibited |
|---|---|---|
| **Target** | Enumerate path, method, parameters, routes, UI triggers | SQLi/XSS/privilege-escalation/brute-force/fuzz vulns, packet-tampering attacks, destructive operations |
| **Auth** | Hook + stub/mock to bypass the **client-side** login gate | Asking the user for or guessing username/password; attempting a real login-form submission |
| **Runtime** | Hook interfaces without credentials, use mock responses to drive the SPA into its post-login shell | Flows that can only continue with a real backend session |

### Credential-less dynamic analysis (Phase 3 default)

1. Use `preload.js` / `runtime_harvest.js` to **intercept and stub** bootstrap interfaces such as login, permission, and menu;
2. Return a mock body that is **structurally correct, has a success business code, and may have empty data** for business query interfaces;
3. Let the frontend still render post-login pages with no backend or under a 401 environment, thereby triggering more XHR/fetch/WebSocket;
4. **Empty data, blank tables, and placeholder UI are all expected** -- do not pivot to a real login or vulnerability testing because of them.

**In one sentence**: use mocks to prop open the frontend routes and component mounting, and **record outbound requests only**; what the backend returns does not matter, what matters is **which other interfaces the frontend will still call**.

### Hard process prohibitions

| Prohibited | Alternative |
|---|---|
| grep/curl/Read the main entry `index-*.js` to extract API paths before Phase 1 is complete | Run `OUTDIR/harvest_static.py` |
| Hand-write scripts like `extract_apis.py` that replace harvest | Modify `OUTDIR/harvest_static.py` and re-run |
| Repeat the same grep/command after it has failed >=2 times | Switch strategy: read tool_logs, modify harvest, check reference |
| Skip gates A/B and run the original `scripts/` directly | Copy them into OUTDIR and adapt to the target |
| Real username/password, OTP, OAuth and other auth | stub/mock (see above) |
| Skip the stub to "get real data" and do privilege-escalation/injection tests | Record outbound only; stays within the recon boundary |
| Irreversible operations such as deleting, exporting sensitive data, or bulk writes | Same applies to coverage clicks |
| Claim all pages and interfaces are captured without completing runtime + dynamic enumeration | See "Definition of done" or annotate the limitation |
| Claim all parameters are known without completing the parameter trigger matrix + diff | Phase 3b matrix + Phase 5 diff |
| Infer required/optional from a single runtime sample | Multi-sample diff, or inference from validation rules/errors |

---

## Two-layer model + run modes

| Layer | Output | Ceiling |
|---|---|---|
| **Static** (JS bundle) | Full endpoint paths, route draft, request-packing field candidates | No HTTP method; parameters need Phase 1b; misses runtime-concatenated URLs |
| **Runtime** (live session) | Method + body + response + dynamic URLs + WS/SSE; multi-sample diff to fill in parameters | A page must actually render before it sends a request; a single sample is not enough to pin required/optional |

| Run mode | Engine | Fits |
|---|---|---|
| **depth** | `runtime_harvest.js` (Puppeteer) | API inventory, METHOD/params/response body, WS/SSE, reproducible bulk runs |
| **coverage** | browser + `preload.js` | Click tabs/modals/tables, deeper feature-point coverage |
| **both** | depth first, then coverage | Most complete, longest running |

**Parameter methodology** (no universal script): use harvest/regex for paths; use **anchor window expansion + UI binding chain + multi-sample diff + error-driven inference** for parameters (grep recipes in section J of [reference.md](reference.md)).

---

## Definition of done

All must hold before you may claim recon is complete:

- [ ] **Static**: Phase 1 harvest produced `api_static.txt`, `routes.txt`, `js/`
- [ ] **Runtime**: at least one of depth or coverage; coverage/both require **Hook in effect + dynamic enumeration loop**
- [ ] **In the shell**: visiting a business path does not land on `/login` (mind hash routing)
- [ ] **Parameters**: coverage/both completed the parameter trigger matrix + `param_samples.json`; Phase 5 merged `params_merged.json`
- [ ] **Depth** (if module pages are blank): Phase 4 permission-tree reconstruction and re-run, until **module-level APIs** appear (not just locale/bootstrap)
- [ ] **Delivery**: Phase 5 outputs are complete (see the Phase 5 output table); `insert_assets` writes the service and endpoint assets

---

## Scripts and gates

`scripts/` are reference templates only; **do not** run the originals directly and treat them as the final result.

**Rule**: read first -> adapt to the target -> write into `OUTDIR` (e.g. `recon/`) -> record `CHANGES.md`; if they do not fit, rewrite per the methodology, borrowing only the structure.

| Gate | When | Reference script -> OUTDIR copy | Common must-change items |
|---|---|---|---|
| **A (static)** | After Phase 0, before the **first** harvest/spider run | `harvest_static.py` / `spider_mpa.py` | **Most sites run with the default regex as-is**; only when the manifest/dialect does not match, adjust the endpoint regex, webpack/Vite `publicPath`, MPA exclude/cookie |
| **B (runtime)** | After Phase 2, before running depth/coverage | `runtime_harvest.js` / `preload.js` + `config.json` | Cookie/localStorage keys, neutralize success values, stubs, login regex, api prefix, hash/history |

**SPA mandatory order** (not interchangeable; the Phase numbering takes precedence over "explore first, then script"):

| Step | Must | Prohibited |
|---|---|---|
| After Phase 0 is complete | Next Bash = `python3 OUTDIR/harvest_static.py <URL> OUTDIR` | curl/grep/Read the main entry `index-*.js` (usually >500KB) |
| Gate A | Copy the script -> small tweaks as needed -> **run immediately** | Extract APIs by hand first, then decide whether to harvest |
| Before Phase 1 is complete | `wc -l` to verify output; on 404, adjust harvest and retry | Hand-write an extract script; repeatedly grep a URL not yet downloaded |
| From Phase 1b | grep only `OUTDIR/js/*.js` | Use the main bundle instead of harvest |

- OK copy `harvest_static.py` -> (optional) adjust regex -> **run immediately**
- NO curl the main bundle -> grep many times -> write a temporary extract -> harvest only at the end
- **MPA**: after Phase 0, next Bash = `python3 OUTDIR/spider_mpa.py ...`

---

## Tooling and output constraints

| Constraint | Note |
|---|---|
| Large files | **Do not** Read/grep an `index-*.js` >100KB into context; batch-process with an OUTDIR script |
| grep output | Must use `\| head -20` or `-m 5`; keep only a path summary in the conversation, do not paste bundle fragments |
| Verification | Use `wc -l`, `ls \| wc -l`; do not Read a whole directory |
| regex first look | Optional, <=1 time, only on a <=50KB small chunk or HTML; the authoritative static source is harvest |
| reference | Recipes/templates/troubleshooting are in [reference.md](reference.md); do not repeat the full text inline |

---

## Execution roadmap

```
Phase 0 classify + OUTDIR
  -> Gate A -> Phase 1 harvest (* run immediately *)
  -> Phase 1b parameter reversing
  -> Phase 2 three auth gates -> config.json
  -> Gate B -> Phase 3 runtime + parameter matrix
  -> Phase 4 permission tree (when needed) -> re-run Phase 3
  -> Phase 5 merge report + insert_assets to bulk-insert every discovered service and endpoint API asset; no matter what, do not omit any discovered asset on insert
```

Check off in order; **you may not enter the next Phase until the previous item is complete**.

1. [ ] **Phase 0**: first look at SPA/MPA; create `OUTDIR` -> [Phase 0](#phase-0--classification)
2. [ ] **Gate A + Phase 1**: copy the script -> **immediately** harvest -> `wc -l` verify -> [Phase 1](#phase-1--static)
3. [ ] **Phase 1b**: anchor window expansion + binding layer -> `param_candidates.json` -> [Phase 1b](#phase-1b--parameter-reversing)
4. [ ] **Phase 2**: three auth gates -> `config.json` -> [Phase 2](#phase-2--three-auth-gates)
5. [ ] **Gate B**: adjust the runtime script -> [Phase 3](#phase-3--runtime)
6. [ ] **Phase 3**: depth / coverage / both; confirm you are in the shell; parameter trigger matrix -> `param_samples.json`
7. [ ] **Phase 4** (if needed): permission tree -> patch stubs -> re-run Phase 3 -> [Phase 4](#phase-4--permission-tree-reconstruction)
8. [ ] **Phase 5**: merge outputs + report + `insert_assets` -> [Phase 5](#phase-5--merge-and-report)

---

## Phase 0 — Classification

Fetch the entry HTML and **create `OUTDIR`** (do not modify `scripts/` inside the skill):

- **SPA**: empty shell + `<div id=app>` + chunk -> Phase 1-5
- **MPA**: SSR + `<form>`, no endpoint bundle -> after Gate A:

```bash
python3 recon/spider_mpa.py <BASE_URL> <OUTDIR> [--cookie "session=..."] [--max 300] [--depth 5] [--exclude "logout|delete|destroy"]
```

Produces `forms.txt`, `links.txt`, `api_inline.txt`. For an SPA, if forms ~= 0 -> switch to Phase 1.

---

## Phase 1 — Static

Follow [Scripts and gates](#scripts-and-gates) and [Tooling and output constraints](#tooling-and-output-constraints).

```bash
python3 recon/harvest_static.py <BASE_URL> <OUTDIR>
```

harvest: parse HTML script -> webpack/Vite manifest -> download all lazy chunks -> produce `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt`.

```bash
wc -l OUTDIR/api_static.txt OUTDIR/routes.txt
ls OUTDIR/js | wc -l
```

- chunk count vs manifest: on 404, adjust harvest and retry; do not curl each chunk by hand
- `api_static.txt` too small -> loosen the endpoint regex inside OUTDIR and re-run (see reference)

### Phase 1b — Parameter reversing

Paths come from Phase 1; the parameter fields must be reconned separately. grep rules are in [Tooling and output constraints](#tooling-and-output-constraints).

**Completion criterion**: for important interfaces you can answer -- field name, transport location, type inference, whether required, sample value, confidence.

#### 1b.0 — Transport shape

| Shape | Where the parameters are | Static priority to look at |
|---|---|---|
| REST JSON | body + query | `(params\|data\|body)\s*:\s*\{` near the path anchor |
| GraphQL | `variables` | gql template, `$page: Int` |
| Classic form | urlencoded | `<form>`, `FormData` |
| File upload | multipart | `FormData.append` |
| Path parameter | `/user/:id` | route table + `useParams` / `$route.params` |
| Encrypted/signed | packed into `sign`/`data` | Hook the encryption function arguments (reference section D) |

Output: annotate each interface with `transport: query|json|form|graphql|encrypted`.

#### 1b.1 — Anchor window expansion

Using a known path as the anchor, expand the window to find the request-packing object:

```bash
grep -n '"/api/user/list"' OUTDIR/js/*.js | head -20
grep -rhoaE '.{0,120}("/api[^"]+").{0,200}' OUTDIR/js/*.js | head -20
grep -rhoaE '(params|data|body|payload)\s*:\s*\{' OUTDIR/js/*.js | head -20
```

| Wrapping layer | Parameter clue |
|---|---|
| axios instance | `data` / `params` |
| unified request | interceptor injects global fields |
| OpenAPI client | generated method signature |
| React Query / SWR | hook second argument |
| Vue composable | composable arguments |

Type residue: `yup`/`zod`/rules, `Form.Item name=`, embedded Swagger.

-> `param_candidates.json`: `{ path, fields[], source: "static-callsite", confidence }`

#### 1b.2 — Binding layer

```
Form field → onFinish/handleSubmit → transform → API payload
```

| Binding source | Technique |
|---|---|
| Form submit | follow submit -> transform -> API |
| Table search | `getFieldsValue()` -> `params` |
| Route | `:id` / `?tab=` |
| Interceptor | global `tenantId`, pagination, sign |
| Enum select | `options` -> API enum values |

From the DevTools call stack, trace upward from `fetch`/`XHR.send` to the request-packing function.

#### 1b.3 — Three packing questions (!= the Phase 2 auth gates)

| Question | What to answer |
|---|---|
| **Assembly** | where the payload is built, signs of transform |
| **Validation** | required, pattern, enum |
| **Transport** | path / query / body / multipart / header |

The interceptor gate (Phase 2) also reads global injected fields in passing (Authorization, `X-Tenant-Id`, sign).

#### 1b.4 — Handoff to Phase 3

Candidate fields come from the static/binding layer; **required/optional/conditional dependencies** need the Phase 3 parameter matrix + diff + Phase 5 error-driven inference.

---

## Phase 2 — Three auth gates

grep in `OUTDIR/js/` (with `head`), write into `config.json` (recipes in reference):

| Gate | Question | Keywords |
|---|---|---|
| **Render gate** | How is "already logged in" decided? | `isLogin`, `getToken`, Cookie/localStorage |
| **Interceptor gate** | What triggers a jump to `/login`? | `response_code`, `errno`, axios interceptor |
| **Content gate** | Where do the menu/permissions come from? | `menu`, `permission`, `role`, `acl`, `routes` |

Do not treat a localStorage key name as a credential -- it must be confirmed from the chunk/request chain.

**Exit = Gate B**: land the conclusions in `config.json`, and modify `OUTDIR/runtime_harvest.js` / `preload.js`.

### Phase 2b — API observation (optional)

Use `preload.js` inside OUTDIR to confirm session key names, Authorization, and nested API URLs:

| Config | Output |
|---|---|
| `recordDetail: true` | `__API_RECON_DETAIL__` |
| `observe.xhrHeaders: true` | headers observation |
| `extractUrlsFromResponse: true` | sub-APIs inside responses |
| `observe.storageReads/cookieReads: true` | backfill config |
| `neutralizeVueRouter: true` | `__API_RECON_ROUTES__` |

Each coverage round exports: `__API_RECON_LOG__`, `__API_RECON_DETAIL__`, `__API_RECON_ROUTES__`, `__API_RECON_OBSERVE__`.

---

## Phase 3 — Runtime

Gate B must already be passed; follow [Boundaries and prohibitions](#boundaries-and-prohibitions-agent-must-read-violating-is-out-of-scope) and the credential-less mock strategy.

In `config.json` set `"runtimeMode": "depth" | "coverage" | "both"` (templates in reference).

### Hook and stub (shared by depth + coverage)

| Layer | Scope | Purpose |
|---|---|---|
| L1 exact | stub auth/permission/bootstrap | pass the first-screen auth |
| L2 negative correction | all JSON responses | not-logged-in code -> success |
| L3 fallback | `/api` and others not hit by L1 | empty success body, prop open the UI |

- **depth**: fake auth + `forward` to change the business code + `stubs`; traverse `routes` (hash/history); produce `runtime_api.json`
- **coverage**: inject `preload.js` at **document-start** (CDP `addScriptToEvaluateOnNewDocument` or Userscript)

Verify: `window.__API_RECON_PRELOAD__` exists; a business path does not return to `/login`.

```bash
cd recon && npm install
node runtime_harvest.js config.json
```

### 3b — coverage dynamic enumeration (required)

1. Main nav/sidebar -- click each item, wait 1-3s for the network
2. Tabs -- `role=tab`, `.ant-tabs-tab`
3. Tables -- first-row view/edit/detail
4. Toolbar -- export, filter, create (**avoid irreversible deletes**)
5. On entering each module -- merge API/routes
6. SPA -- controlled `pushState` for paths not covered by `routes.txt` (prohibited for MPA)

**Parameter trigger matrix** (required): for each module, record once per operation type, and **diff multiple samples**:

| Operation | Parameters usually added |
|---|---|
| List first screen | pagination + default filters |
| Click search | keyword, filter |
| Advanced filter | more optional |
| Create/edit | full entity |
| Bulk/export/sort | `ids[]`, `exportType`, `sortField` |

**Under a stub the outbound body/headers are still real** -- take the request as authoritative. Record -> `scan_raw.json`, `param_samples.json`, `api_detail.json`.

- **Vue**: `neutralizeVueRouter: true` + document-start preload
- **React**: `routes.txt` + sidebar clicks + `pushState`
- **both**: 3a depth first, then 3b coverage

---

## Phase 4 — Permission tree reconstruction

**Trigger**: module pages blank / each route only bootstrap (e.g. locale) -> the content gate was not passed.

| Symptom | Meaning |
|---|---|
| Entered the shell successfully | render gate + interceptor gate already passed |
| Sidebar missing items / click goes blank | stub shape or permission codes incomplete |
| Each route has the same, very few APIs | `v-if permission` not passing |
| `routes.txt` is far fewer than the bundle | must be completed from the auth module |

```bash
grep -rhoaE '"/api[^"]*(permission|perm|role|menu|acl)[^"]*"' OUTDIR/js/*.js | sort -u | head -30
grep -rhoaE 'userRouteAuth|getResultTree|routeMap|routeLink|menuList|authList' OUTDIR/js/*.js | head -20
```

Typical chain: `role_permissions` (flat codes) + `permissions/all` (tree) -> `getResultTree` -> `userRouteAuth[CODE].url`.

```bash
python3 recon/extract_route_map.py recon/js recon/
python3 recon/build_perm_tree.py recon/js recon/ --config recon/config.json
```

Intermediate output: `route_map.json`, `userRouteAuth.json`, `permissions_tree.json`, `*_stub.json`, `perm_codes_all.txt`.

stub check: the outer `response_code` matches the interceptor gate; the flat codes align with the tree; `routes` covers every link in `route_map`.

After updating `config.json`, **re-run Phase 3**. For a large SPA you can tune `waitUntil`, `routeTimeout`, `perRouteMs` (see reference sections A3/I).

---

## Phase 5 — Merge and report

### Output table

| File | Stage | Content |
|---|---|---|
| `js/`, `api_static.txt`, `routes.txt`, `chunkmap.txt` | 1 | static bundle and paths |
| `param_candidates.json` | 1b | static parameter field candidates |
| `config.json` | 2 | three gates + runtime config |
| `runtime_api.json` | 3a | depth detailed recording (incl. WS/SSE) |
| `param_samples.json`, `scan_raw.json`, `api_detail.json` | 3b | multi-sample, click log, detail |
| `route_map.json` etc. | 4 | permission-tree intermediate files (if run) |
| `params_merged.json` | 5 | merged parameter fields + confidence |
| `api_merged.txt` | 5 | `METHOD /path [params] [static\|runtime\|both]` |
| `site_map.json` | 5 | routes, APIs, params, feature points, limitations |
| **insert_assets** | 5 | write all service and endpoint assets into the asset store |

### 5b — Parameter merge

diff from `param_samples.json`; **no universal merge script**. Confidence rules in reference J7 (high/medium/low/to-be-triggered).

### 5c — Error-driven inference

Within the authorized scope you may send incomplete requests to read 400s (**this is parameter recon, not vulnerability testing**): `field 'x' is required`, enum errors, etc. Mind the `data` wrapper, `variables`, and the pre-encryption `bizData`.

The report must state: runtimeMode, static/runtime API counts, parameter confidence, uncovered modules, and a `CHANGES.md` summary relative to the reference scripts.

Suggested `site_map.json` structure:

```json
{
  "site": "https://example.com",
  "runtimeMode": "both",
  "appType": "vue-spa",
  "routeGuardStrategy": ["nav-neutralize", "L1-auth", "L2-patch", "forward"],
  "apisFromStatic": [],
  "apisFromRuntime": [],
  "apis": [],
  "params": [{ "method": "POST", "path": "/api/user/list", "transport": "json", "fields": [] }],
  "frontendRoutes": [],
  "routesVerifiedByClick": [],
  "featuresTriggered": [],
  "limitations": ""
}
```

More fields and grep recipes are in [reference.md](reference.md).

---

## General notes

- **Framework-agnostic**: webpack/Vite/Angular lazy load works the same way
- **Transport**: REST/JSON, GraphQL, WebSocket, SSE; gRPC-web is out of scope
- **SSR**: client-side fetch can be recorded; RSC/Server Actions are not fully enumerable
- **Blind spots**: JSVMP, WASM, HMAC/mTLS strong validation -> static + annotate the limitation
- **Parameter blind spots**: conditional linkage, hidden params, WASM request packing -> "to be triggered" / "unreachable"
- **Static is the safety net**: when runtime is blocked, static can still enumerate endpoints

---

## Additional resources

- Grep recipes, `config.json` templates, troubleshooting, Hook, parameter-reversing section J, site_map template: **[reference.md](reference.md)**
- Reference script paths are in the [Scripts and gates](#scripts-and-gates) table
