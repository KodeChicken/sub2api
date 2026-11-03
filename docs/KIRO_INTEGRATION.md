# Native Kiro Integration

The `kiro` provider implements the Kiro protocol inside Sub2API. It does not
launch, embed, or forward requests through a kiro.rs proxy server.

Protocol reference: `zyphrzero/kiro.rs`, version 0.9.4, commit
`e768cef18b6aa4e614845712987f7cc087d49a0e`.
The original MIT attribution is in `backend/resources/kiro.rs.NOTICE`, included
in release archives and container runtime resources.

## Request Path

```text
Messages / Responses / Chat Completions
  -> existing authentication, group routing, account scheduling
  -> Kiro-only adapter
  -> conversationState + native IDE/CLI endpoint
  -> AWS EventStream decoding
  -> client protocol + existing Sub2API usage accounting
```

Only accounts with `platform=kiro` enter the adapter. Kiro credentials are
explicitly excluded from the generic Anthropic transport and billing probe.
Existing providers retain their forwarding implementations.

The adapter supports text, base64 images, conversation/tool-result history,
automatic client function tools, native reasoning, streaming and JSON output,
and Kiro MCP web search. Search runs up to five inference rounds and five
search calls; client-side tools are returned to the client, never executed
by the server. Search and compaction responses are buffered before delivery.

Responses remote compaction accepts a unique terminal `compaction_trigger`,
generates a continuation summary, and returns a `compaction` item. Subsequent
requests can replay that item. The `kiro-rs.compaction.v1:` payload is an opaque
protocol envelope containing plain text, **not encryption**. Truncated or
refused summaries are not reported as successful compaction.

## Accounts

Create a Kiro account in the admin account dialog. Keep the account name,
groups, proxy and concurrency on the first step, then continue to the Kiro
authorization panel. Sign-in is the default; JSON import and manual credentials
remain alternatives. Both camelCase exports and Sub2API snake_case fields are
accepted.

### Interactive Sign-In

- **GitHub / Google:** generate the portal link, open it and choose the provider
  in the official Kiro portal. After login, paste the **complete callback URL**
  and complete authorization. A bare authorization code is not accepted.
- **AWS Builder ID:** generate a device authorization, open the verification
  page and enter the displayed user code. The panel polls at the server's
  permitted interval and saves the account after authorization.
- **Enterprise AWS IAM Identity Center:** provide the organization's HTTPS
  SSO Start URL, then use the same device authorization flow.

Social authorization uses `http://127.0.0.1:3128` as its fixed redirect base.
Sub2API does not start a loopback listener: copy the browser's final URL even
if the local page cannot load. Only `/oauth/callback` or `/signin/callback`
with valid, matching parameters establishes an authorization result; an
inaccessible page alone does not. This also works when Sub2API runs remotely
or in Docker, without a callback domain, plugin or kiro.rs process.

Normal sign-in does not expose access/refresh tokens, PKCE or OIDC client
secrets. New interactive accounts default to IDE; advanced settings select
IDE/CLI, authentication/API regions and API domain. Import preserves its
transport settings. Kiro API keys retain the separate key-only path.
Microsoft Entra ID is import/manual-only, not an interactive option.

Use the existing account menu's reauthorization action for a Kiro OAuth
account. The same panel replaces its complete authentication tuple, removes
stale identity/profile metadata and preserves groups, proxy, concurrency,
pricing, transport and other account configuration. Inactive accounts remain
inactive and the persistent scheduling switch is not enabled. A concurrent
credential refresh/edit or target/proxy change rejects the stale login
instead of overwriting the newer credentials.

### Session And Save Semantics

Kiro-only admin endpoints live under `/api/v1/admin/kiro/oauth/sessions`:
start, `/:id/complete`, `/:id/poll`, `/:id/finalize`, GET and DELETE.
Restricted admin API keys cannot access these endpoints. All four POST
request bodies are omitted before audit capture; safe actor/status metadata
remain. Callback URLs are parsed, never fetched, and code/state ambiguity,
wrong loopback origins/paths and duplicate parameters are rejected.
Auth/token requests use fixed regional hosts, the captured proxy and no
redirect following. The SSO Start URL is sent as protocol data, not fetched.

Sessions are owner-bound in the existing Redis, with CAS transitions and
Redis-clock claim deadlines. Redis is required; there is no process-local
fallback. Backend restart and multiple replicas work while sharing retained
Redis state. Authorization lasts at most ten minutes; device/registration
expiry can shorten it. Device pending, cumulative `slow_down`, denial and
expiry are separate states, with bounded server-side polling/backoff.

Completion/polling stages credentials without writing an account. Only the
current panel generation finalizes. Closing/restarting cancels before a save
is accepted and fences late responses. During saving, ordinary close/restart
is blocked until the result is reconciled: an accepted or committed save
cannot honestly be reported as cancelled.

Account/groups or conditional reauthorization, scheduler outbox and a
nonsecret success receipt share one PostgreSQL transaction. The receipt's
unique row serializes retries, including stale savers on another replica.
Failed saves retain staged credentials for retry before authorization expiry;
they do not re-exchange a spent social code. A confirmed receipt replays the
original account after a lost response or Redis loss. Receipt retention is at
least one hour, the configured write-idempotency window and the authorization
deadline plus sixty seconds; missing/expired sessions never authorize new
writes from an absent receipt.

Request-driven stale-claim recovery needs no daemon. Starting uses a 25-second
operation budget/35-second claim; exchange/poll uses 20/30 seconds; saving uses
10/20 seconds. Saving recovery locks the SQL receipt before declaring retry
or cancellation safe. Database contention/uncertain commit remains an explicit
unknown result, not permission to create a second account. Transaction-local
timeouts bound healthy-server waits but do not guarantee progress during a
database partition.

| Authentication | Required Fields |
| --- | --- |
| GitHub / Google | `refresh_token`, `auth_method=social` |
| Builder ID / AWS IdC | `refresh_token`, `client_id`, `client_secret`, `auth_method=idc` |
| Microsoft Entra ID | `refresh_token`, `client_id`, `token_endpoint`, `auth_method=external_idp` |
| Kiro API key | `api_key` starting with `ksk_`, account type `apikey` |

Optional fields include `auth_region`, `api_region`, `profile_arn`, `machine_id`,
`endpoint` (`ide` or `cli`), and `api_domain` (`q` or `runtime`).
Entra token endpoints are restricted to HTTPS tenant endpoints under
`login.microsoftonline.com`.

OAuth refresh reuses Sub2API's local/distributed locking. Credential updates
compare the complete original credential document and proxy, and atomically
publish scheduler invalidation. A concurrent admin reauthorization cannot be
overwritten by a stale refresh result. Blank secrets on edit preserve stored
secrets. Replacing only a refresh token invalidates the old access token.

Enterprise profiles are discovered when needed; discovery results are cached
for one hour and tied to the credential document. Models and usage queries use
the same profile discovery and configured account proxy.

## Models And Billing

After creation, edit the account and synchronize the model whitelist. The
native `ListAvailableModels` result is authoritative for available model IDs.
No Kiro model list or account entitlement is invented from Claude defaults.
Imported/API-supplied `model_mapping` aliases remain supported.

Bind the account to a Kiro group, or an explicitly configured composite group,
and use the existing Sub2API key and public endpoints. No second API key or
proxy URL is required on the client.

The native quota query displays monthly credits, active trials and unexpired
bonuses. Unknown quota is not interpreted as zero usage.

The last `metadataEvent.tokenUsage` snapshot supplies exact uncached input,
output, cache-read and cache-write tokens. Calls without that snapshot use
local estimates. Multi-round search adds usage across provider calls.
`kiro_last_usage_source` records `provider` or `estimate`; credit consumption is
stored separately as `kiro_last_request_credits`. Credit units are not dollars,
and no automatic credit-to-dollar exchange rate is inferred. Configure normal
Sub2API model pricing/rate multipliers before exposing paid access.

## Explicit Limits

- HTTP JSON/SSE only; no Kiro WebSocket or realtime transport.
- Full history is required; `previous_response_id` storage is not supported.
- Assistant prefill and remote image URLs are rejected.
- Only automatic tool selection is supported. Forced/disabled `tool_choice`
  and schema-enforced structured output return a clear 400 response.
- Kiro has no native sampling/output-token fields in this envelope. Client
  `max_tokens`, sampling and stop-sequence controls are not strict upstream
  guarantees. Use a native provider when those controls are required.
- Compaction does not automatically truncate history or retry oversized tool
  outputs. Context overflow is surfaced to the client.
- Native model metadata includes reported token limits and descriptions.
  Missing reasoning/modality metadata remains incomplete, with the existing
  admin warning, rather than guessed capabilities.
- Kiro uses upstream-specific APIs; protocol changes or account permissions
  can break compatibility. This does not grant additional upstream entitlements.
- Redis restart/eviction without retained state loses pending authorization.
  A token issued upstream before a crash or failed durable staging cannot be
  reconstructed: restart sign-in rather than blindly re-exchanging it.
- There is no end-to-end exactly-once or availability guarantee across
  upstream issuance, Redis and PostgreSQL. Durable save receipts prevent
  duplicate local account effects within their retention and binding rules.

## Verification

The added tests cover all three client protocols in JSON/SSE modes, EventStream
length/CRC validation, tools, reasoning, images, credits, search, compaction,
refresh races, credential isolation, repository CAS predicates/outbox, and
admin form behavior. Run the existing backend unit and frontend suites too.
OAuth unit coverage additionally checks strict callbacks, fixed request
payloads, device backoff, Redis CAS/expiry/cancellation, late responses,
transaction-bound receipts, save retry/replay, selected-target reauthorization
and audit-body omission. SQL mocks and injected protocol responses are unit
evidence, not actual PostgreSQL/Redis multi-replica integration or an official
Kiro sign-in.

Official portal/Builder/enterprise login, account permissions and live
inference/refresh/quota validation require an authorized Kiro account and
remain unverified without it.
No real Kiro credentials are included in the repository or test fixtures.

The full backend unit run observed two existing failures, both reproduced at
the unmodified baseline commit `b2d1edca4b09b82cddc3263378c66bd84820651b`:

- `TestCompatibleImagesForwardGemini/account_mapping`: the compatible image
  path drops `custom_field` after account model mapping.
- `TestOllamaProbeCallback_StaleLongDoesNotOverrideNewShort`: the stale callback
  passes the test's CAS assertion.

Those image and Ollama implementations were deliberately left unchanged.
